package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"tars/internal/boot"
	"tars/internal/config"
	"tars/internal/session"
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
// （Excalidraw 网页版的 #addLibrary 安装协议在我们的嵌入组件里不可用：
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

// ---- 副面板窗口（画板 + 编辑器的统一容器，Tab 页形态）----
// 画板和编辑器共用一个独立窗口：每次打开是一个 Tab。Tab 状态由后端持有
// （进程内内存），窗口关闭重开不丢；前端经 aux:tabs-changed 事件同步。
//
// 拖出与吸回：
//   - Tab 向下拖出（DetachAuxTab）成为独立小窗口；
//   - 拖回：独立窗口拖动停止后与副窗口重叠超阈值 → 自动吸回为 Tab；
//     也可点 Tab 条上的吸回按钮（ReattachAuxTab）。
// （曾实现过跟随主窗口右缘的粘附模式，效果不佳已移除，副窗口始终自由悬浮。）

// auxWindowName 副面板窗口的固定名；detached 窗口名为该前缀 + Tab ID。
const auxWindowName = "tars-aux"
const auxDetachedPrefix = "tars-aux-detached:"

// AuxTab 是副面板窗口中的一个标签页。
type AuxTab struct {
	ID        string `json:"id"`             // canvas / editor:<path>
	Kind      string `json:"kind"`           // "canvas" | "editor"
	SessionID string `json:"sessionId"`      // 归属会话（草稿/读写上下文）
	Path      string `json:"path,omitempty"` // editor 标签的工作区相对路径
	Title     string `json:"title"`          // 标签显示名
}

// auxState 副面板状态（CanvasService 进程内持有）。
type auxState struct {
	mu       sync.Mutex
	tabs     []*AuxTab
	active   string
	detached map[string]*AuxTab // 拖出的独立窗口 Tab
}

var aux = &auxState{detached: map[string]*AuxTab{}}

// auxSnapshot 当前完整状态（事件负载与查询返回值同构）。
func auxSnapshot() map[string]any {
	aux.mu.Lock()
	defer aux.mu.Unlock()
	detached := make([]*AuxTab, 0, len(aux.detached))
	for _, t := range aux.detached {
		detached = append(detached, t)
	}
	return map[string]any{
		"tabs":     aux.tabs,
		"activeId": aux.active,
		"detached": detached,
	}
}

// emitAuxChanged 广播最新副面板状态到所有窗口。
func emitAuxChanged() {
	if app := application.Get(); app != nil {
		app.Event.Emit("aux:tabs-changed", auxSnapshot())
	}
}

// DetachAuxTab 把 Tab 从副面板拖出为独立小窗口（无边框同款样式），
// 窗口出现在拖拽落点（屏幕坐标，以落点为窗口顶部中心）。
func (s *CanvasService) DetachAuxTab(tabID string, x, y int) error {
	app := application.Get()
	if app == nil {
		return fmt.Errorf("应用尚未就绪")
	}
	aux.mu.Lock()
	var tab *AuxTab
	for i, t := range aux.tabs {
		if t.ID == tabID {
			tab = t
			aux.tabs = append(aux.tabs[:i], aux.tabs[i+1:]...)
			break
		}
	}
	if tab == nil {
		aux.mu.Unlock()
		return fmt.Errorf("tab not found: %s", tabID)
	}
	aux.detached[tabID] = tab
	if aux.active == tabID && len(aux.tabs) > 0 {
		aux.active = aux.tabs[len(aux.tabs)-1].ID
	}
	aux.mu.Unlock()

	const w, h = 860, 720
	win := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:   auxDetachedPrefix + tabID,
		Title:  "TARS - " + tab.Title,
		Width:  w,
		Height: h,
		// 与副面板一致放开最小尺寸
		MinWidth:  320,
		MinHeight: 240,
		URL:       "/?view=aux&detached=" + url.QueryEscape(tabID),
		// 同副面板主窗口：不开 NonClientRegionSupport（详见 OpenAuxTab 注释）
		Frameless:        runtime.GOOS == "windows",
		BackgroundColour: application.NewRGB(19, 19, 20),
	})
	win.SetPosition(x-w/2, y-16)
	emitAuxChanged()
	return nil
}

// DropDetachedTab 独立窗口的 Tab 拖拽松开时调用：落点（屏幕坐标）在
// 副面板窗口内则吸回为 Tab，否则保持独立悬浮。
func (s *CanvasService) DropDetachedTab(tabID string, x, y int) {
	app := application.Get()
	if app == nil {
		return
	}
	auxWin, ok := app.Window.GetByName(auxWindowName)
	if !ok {
		return // 副面板未打开：保持悬浮
	}
	ax, ay := auxWin.Position()
	aw, ah := auxWin.Size()
	if x >= ax && x < ax+aw && y >= ay && y < ay+ah {
		s.ReattachAuxTab(tabID)
	}
}

// ReattachAuxTab 把独立窗口的 Tab 吸回副面板。
func (s *CanvasService) ReattachAuxTab(tabID string) {
	app := application.Get()
	aux.mu.Lock()
	tab, ok := aux.detached[tabID]
	if !ok {
		aux.mu.Unlock()
		return
	}
	delete(aux.detached, tabID)
	aux.tabs = append(aux.tabs, tab)
	aux.active = tabID
	aux.mu.Unlock()

	if app != nil {
		if win, ok := app.Window.GetByName(auxDetachedPrefix + tabID); ok {
			win.Close()
		}
		if w, ok := app.Window.GetByName(auxWindowName); ok {
			w.Show().Focus()
		}
	}
	emitAuxChanged()
}

// OpenAuxTab 在副面板窗口打开（或激活）一个 Tab：kind 为 canvas / editor，
// path 仅 editor 使用（工作区相对路径）。同 kind+path 的标签去重激活。
func (s *CanvasService) OpenAuxTab(sessionID, kind, path string) error {
	app := application.Get()
	if app == nil {
		return fmt.Errorf("应用尚未就绪")
	}
	id := kind
	title := "画板"
	if kind == "editor" {
		id = "editor:" + path
		title = filepath.Base(path)
	}

	aux.mu.Lock()
	// 若该 Tab 当前是拖出的独立窗口，先吸回再激活
	if t, ok := aux.detached[id]; ok {
		delete(aux.detached, id)
		aux.tabs = append(aux.tabs, t)
		t.SessionID = sessionID
		aux.active = id
		aux.mu.Unlock()
		if win, ok := app.Window.GetByName(auxDetachedPrefix + id); ok {
			win.Close()
		}
		emitAuxChanged()
		if w, ok := app.Window.GetByName(auxWindowName); ok {
			w.Show().Focus()
		}
		return nil
	}
	found := false
	for _, t := range aux.tabs {
		if t.ID == id {
			t.SessionID = sessionID // 换绑到最新会话（草稿归属跟随）
			found = true
		}
	}
	if !found {
		aux.tabs = append(aux.tabs, &AuxTab{ID: id, Kind: kind, SessionID: sessionID, Path: path, Title: title})
	}
	aux.active = id
	aux.mu.Unlock()

	if win, ok := app.Window.GetByName(auxWindowName); ok {
		win.Show().Focus()
	} else {
		const w, h = 720, 800 // 高度与主窗口默认一致
		opts := application.WebviewWindowOptions{
			Name:   auxWindowName,
			Title:  "TARS 面板",
			Width:  w,
			Height: h,
			// 最小尺寸尽量放开，由用户自由收缩
			MinWidth:  320,
			MinHeight: 240,
			URL:       "/?view=aux",
			// 与主窗口一致的无边框样式（Tab 条兼任标题栏）；macOS 保持
			// 原生 HiddenInset。
			// 注意：副面板不开 NonClientRegionSupport——其边框缩放 NC 循环
			// 与 app-region 拖拽区（Tab 条贴顶缘）互相吞掉松开事件，会偶发
			// "点一下边框后窗口跟随鼠标缩放"的卡死态。副面板改用 JS 拖拽
			//（Tab 条 --wails-draggable），边框缩放走 Wails 默认无边框路径。
			Frameless:        runtime.GOOS == "windows",
			BackgroundColour: application.NewRGB(19, 19, 20),
			// InitialPosition 默认为 WindowCentered——此时 X/Y 被忽略，
			// 必须显式 WindowXY 才能让下方计算的坐标生效。
			InitialPosition: application.WindowXY,
		}
		// 默认停靠主窗口右侧，避免遮挡：
		// 常规态贴主窗口右缘；主窗口最大化/全屏（或右侧空间不足）时
		// 改为右缘对齐工作区右缘——层叠在主窗口右侧，不遮左侧内容。
		if main, ok := app.Window.GetByName(MainWindowName); ok {
			if scr, err := main.GetScreen(); err == nil && scr != nil {
				wa := scr.WorkArea
				if main.IsMaximised() || main.IsFullscreen() {
					opts.X, opts.Y = wa.X+wa.Width-w, wa.Y
				} else {
					mx, my := main.Position()
					mw, _ := main.Size()
					opts.X, opts.Y = mx+mw+8, my
					if opts.X+w > wa.X+wa.Width { // 右侧放不下：右缘对齐层叠
						opts.X = wa.X + wa.Width - w
					}
					if opts.X < wa.X {
						opts.X = wa.X
					}
				}
			}
		}
		app.Window.NewWithOptions(opts)
	}
	emitAuxChanged()
	return nil
}

// GetAuxTabs 返回当前 Tab 列表与激活项（窗口加载时拉取）。
func (s *CanvasService) GetAuxTabs() (map[string]any, error) {
	return auxSnapshot(), nil
}

// ---- 编辑器行定位（聊天输入框的引用链接点击回流）----

// pendingReveals 待定位请求（path → [start,end]）：Tab 新打开时
// FileViewer 尚未挂载，事件可能错过，挂载后主动拉取兜底。
var pendingReveals = struct {
	mu sync.Mutex
	m  map[string][2]int
}{m: map[string][2]int{}}

// RevealEditorRange 打开工作区文件（复用编辑器 Tab），定位到起始行并
// 高亮 start~end 行。路径沿用 OpenAuxTab 的 confine 校验（在 FileViewer
// 读取内容时生效）。
func (s *CanvasService) RevealEditorRange(sessionID, path string, startLine, endLine int) error {
	if startLine < 1 || endLine < startLine {
		return fmt.Errorf("无效的行范围：%d-%d", startLine, endLine)
	}
	pendingReveals.mu.Lock()
	pendingReveals.m[path] = [2]int{startLine, endLine}
	pendingReveals.mu.Unlock()

	if err := s.OpenAuxTab(sessionID, "editor", path); err != nil {
		return err
	}
	if app := application.Get(); app != nil {
		app.Event.Emit("aux:editor-reveal", map[string]any{
			"path": path, "start": startLine, "end": endLine,
		})
	}
	return nil
}

// TakePendingReveal 拉取并清除某文件的待定位请求（FileViewer 挂载时调用）。
// 返回值 [start, end]；无待处理请求时 ok=false。
func (s *CanvasService) TakePendingReveal(path string) ([]int, bool, error) {
	pendingReveals.mu.Lock()
	defer pendingReveals.mu.Unlock()
	r, ok := pendingReveals.m[path]
	if ok {
		delete(pendingReveals.m, path)
	}
	return []int{r[0], r[1]}, ok, nil
}

// CloseAuxTab 关闭一个 Tab；最后一个 Tab 关闭时收起窗口（状态保留，
// 再打开时恢复剩余标签）。
func (s *CanvasService) CloseAuxTab(tabID string) error {
	aux.mu.Lock()
	for i, t := range aux.tabs {
		if t.ID == tabID {
			aux.tabs = append(aux.tabs[:i], aux.tabs[i+1:]...)
			if aux.active == tabID && len(aux.tabs) > 0 {
				aux.active = aux.tabs[len(aux.tabs)-1].ID
			}
			break
		}
	}
	empty := len(aux.tabs) == 0
	aux.mu.Unlock()

	app := application.Get()
	if app == nil {
		return nil
	}
	if empty {
		if win, ok := app.Window.GetByName(auxWindowName); ok {
			win.Close()
		}
		return nil
	}
	app.Event.Emit("aux:tabs-changed", auxSnapshot())
	return nil
}

// draftPath 会话画板草稿文件（随会话生命周期，删会话即清理）。
func draftPath(sessionID string) (string, error) {
	proj, ok := boot.GetApp().FindProject(sessionID)
	if !ok {
		return "", fmt.Errorf("session not found: %s", sessionID)
	}
	return filepath.Join(session.GetSessionDir(proj.GetProjectDir(), sessionID), "canvas_draft.json"), nil
}

// GetCanvasDraft 读取会话的画板草稿 JSON（不存在返回空串）。
func (s *CanvasService) GetCanvasDraft(sessionID string) (string, error) {
	path, err := draftPath(sessionID)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read canvas draft: %w", err)
	}
	return string(data), nil
}

// SetCanvasDraft 保存会话的画板草稿（JSON object，含 elements/files/
// 视口 appState；原子写；20MB 上限——图片走 files dataURL 可能较大）。
func (s *CanvasService) SetCanvasDraft(sessionID string, jsonStr string) error {
	if len(jsonStr) > 20<<20 {
		return fmt.Errorf("草稿过大（>20MB）")
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal([]byte(jsonStr), &probe); err != nil {
		return fmt.Errorf("canvas draft must be a JSON object: %w", err)
	}
	path, err := draftPath(sessionID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(jsonStr), 0644); err != nil {
		return fmt.Errorf("write canvas draft: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("commit canvas draft: %w", err)
	}
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

// ExportCanvasScene 另存为（Ctrl+Shift+S）：保存对话框导出 .excalidraw
// 场景文件（可分享/备份，Excalidraw 原生格式）。返回保存路径，取消返回空串。
func (s *CanvasService) ExportCanvasScene(jsonStr string) (string, error) {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal([]byte(jsonStr), &probe); err != nil {
		return "", fmt.Errorf("scene must be a JSON object: %w", err)
	}
	app := application.Get()
	if app == nil {
		return "", fmt.Errorf("应用尚未就绪")
	}
	target, err := app.Dialog.SaveFile().
		SetMessage("另存画板场景").
		SetFilename("canvas.excalidraw").
		AddFilter("Excalidraw 场景", "*.excalidraw").
		SetButtonText("保存").
		CanCreateDirectories(true).
		PromptForSingleSelection()
	if err != nil {
		if isDialogCancelled(err) {
			return "", nil // 用户取消
		}
		return "", fmt.Errorf("save dialog: %w", err)
	}
	if !strings.HasSuffix(target, ".excalidraw") {
		target += ".excalidraw"
	}
	if err := os.WriteFile(target, []byte(jsonStr), 0644); err != nil {
		return "", fmt.Errorf("write scene file: %w", err)
	}
	return target, nil
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
