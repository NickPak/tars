package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"tars/internal/config"
)

// CanvasService —— 画板素材库（Excalidraw Library）的持久化。
// Excalidraw 组件默认把素材库存进 WebView 的 localStorage（埋在 WebView2
// 用户数据目录深处，且随缓存清理丢失）；这里接管为 TARS 工作目录下的
// 常规文件，与技能/记忆等数据同处。
type CanvasService struct{}

// libraryPath 素材库文件路径：<workDir>/canvas/library.json（应用级，
// 不按项目/会话区分——素材是用户的通用资产）。
func libraryPath() string {
	return filepath.Join(config.Get().WorkDir, "canvas", "library.json")
}

// GetCanvasLibrary 返回素材库 JSON（Excalidraw LibraryItems 数组）。
// 文件不存在返回 ""（前端视为空库）。
func (s *CanvasService) GetCanvasLibrary() (string, error) {
	data, err := os.ReadFile(libraryPath())
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read canvas library: %w", err)
	}
	return string(data), nil
}

// SetCanvasLibrary 保存素材库 JSON（原子写：tmp + rename）。
// 入参必须是合法 JSON 数组（Excalidraw LibraryItems）。
func (s *CanvasService) SetCanvasLibrary(jsonStr string) error {
	var items []json.RawMessage
	if err := json.Unmarshal([]byte(jsonStr), &items); err != nil {
		return fmt.Errorf("canvas library must be a JSON array: %w", err)
	}
	return writeLibraryFile(jsonStr)
}

func writeLibraryFile(jsonStr string) error {
	path := libraryPath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create canvas dir: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(jsonStr), 0644); err != nil {
		return fmt.Errorf("write canvas library: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("commit canvas library: %w", err)
	}
	return nil
}

// ImportCanvasLibrary 拉取 .excalidrawlib URL 的素材并合并进本地素材库
//（Excalidraw 网页版的 #addLibrary 安装协议在我们的嵌入组件里不可用：
// 它把安装链接指向应用自身 origin，前端拦截后由这里代抓——避开
// WebView 的 CORS 限制）。按素材 id 去重（已有素材保留本地版本）。
// 返回合并后的完整素材库 JSON 与新增条数。
func (s *CanvasService) ImportCanvasLibrary(sourceURL string) (string, int, error) {
	u, err := url.Parse(sourceURL)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return "", 0, fmt.Errorf("素材库地址必须是 https URL")
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(sourceURL)
	if err != nil {
		return "", 0, fmt.Errorf("下载素材库失败：%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("下载素材库失败：HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20)) // 10MB 上限
	if err != nil {
		return "", 0, fmt.Errorf("读取素材库失败：%w", err)
	}

	// .excalidrawlib 格式：{ "type": "excalidrawlib", "version": N, "libraryItems": [...] }
	var lib struct {
		Type         string            `json:"type"`
		LibraryItems []json.RawMessage `json:"libraryItems"`
	}
	if err := json.Unmarshal(body, &lib); err != nil || len(lib.LibraryItems) == 0 {
		return "", 0, fmt.Errorf("素材库文件格式无效（缺少 libraryItems）")
	}

	// 读出现有库，按素材 id 去重（新素材与本地同 id 时保留本地版本）
	existingJSON, err := s.GetCanvasLibrary()
	if err != nil {
		return "", 0, err
	}
	var existing []json.RawMessage
	if existingJSON != "" {
		if err := json.Unmarshal([]byte(existingJSON), &existing); err != nil {
			return "", 0, fmt.Errorf("本地素材库损坏：%w", err)
		}
	}
	seen := map[string]bool{}
	itemID := func(raw json.RawMessage) string {
		var probe struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(raw, &probe)
		return probe.ID
	}
	for _, raw := range existing {
		if id := itemID(raw); id != "" {
			seen[id] = true
		}
	}
	added := 0
	for _, raw := range lib.LibraryItems {
		if id := itemID(raw); id != "" && seen[id] {
			continue
		}
		existing = append(existing, raw)
		added++
	}

	merged, err := json.Marshal(existing)
	if err != nil {
		return "", 0, err
	}
	if err := writeLibraryFile(string(merged)); err != nil {
		return "", 0, err
	}
	// 广播给所有窗口：主窗口画板若开着可即时刷新素材面板
	if app := application.Get(); app != nil {
		app.Event.Emit("canvas:library-changed")
	}
	return string(merged), added, nil
}

// libraryBrowserWindowName 素材库浏览窗口的固定名（重复打开时聚焦复用）。
const libraryBrowserWindowName = "excalidraw-library"

// canvasWindowName 画板窗口的固定名（重复打开时聚焦复用）。
const canvasWindowName = "tars-canvas"

// OpenCanvasWindow 在独立窗口打开画板（加载本应用 ?view=canvas 极简视图，
// 由前端 CanvasWindow 全屏渲染 Excalidraw）。重复打开时聚焦既有窗口。
func (s *CanvasService) OpenCanvasWindow() error {
	app := application.Get()
	if app == nil {
		return fmt.Errorf("应用尚未就绪")
	}
	if win, ok := app.Window.GetByName(canvasWindowName); ok {
		win.Show().Focus()
		return nil
	}
	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:   canvasWindowName,
		Title:  "TARS 画板",
		Width:  1200,
		Height: 840,
		URL:    "/?view=canvas",
	})
	return nil
}

// CanvasInsertImage 画板窗口产出 PNG（dataURL）→ 广播给主窗口插入输入框。
func (s *CanvasService) CanvasInsertImage(dataURL string) error {
	if !strings.HasPrefix(dataURL, "data:image/") {
		return fmt.Errorf("非法的图片数据")
	}
	if app := application.Get(); app != nil {
		app.Event.Emit("canvas:insert-image", dataURL)
	}
	return nil
}

// CanvasInsertMermaid 画板窗口产出 Mermaid 代码 → 广播给主窗口插入输入框。
func (s *CanvasService) CanvasInsertMermaid(code string) error {
	if strings.TrimSpace(code) == "" {
		return fmt.Errorf("内容为空")
	}
	if app := application.Get(); app != nil {
		app.Event.Emit("canvas:insert-mermaid", code)
	}
	return nil
}

// OpenLibraryBrowser 在应用内窗口打开 Excalidraw 素材站点。
// 一键安装链路的另一半：站点上点 "Add to Excalidraw" 会把窗口重定向到
// <应用origin>/#addLibrary=<url>&token=...——外链在系统浏览器里不可达，
// 但在本窗口里会加载回我们的前端，由启动早期的导入门（LibraryImportGate）
// 接管 hash、完成导入并自动关窗。
func (s *CanvasService) OpenLibraryBrowser(siteURL string) error {
	if !strings.HasPrefix(siteURL, "https://libraries.excalidraw.com") {
		return fmt.Errorf("仅支持打开 libraries.excalidraw.com")
	}
	app := application.Get()
	if app == nil {
		return fmt.Errorf("应用尚未就绪")
	}
	if win, ok := app.Window.GetByName(libraryBrowserWindowName); ok {
		win.SetURL(siteURL).Show().Focus()
		return nil
	}
	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:   libraryBrowserWindowName,
		Title:  "Excalidraw 素材库",
		Width:  1100,
		Height: 800,
		URL:    siteURL,
	})
	return nil
}
