package main

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"

	"tars/internal/boot"
	"tars/internal/session"
	"tars/pkg/schema"
)

// ExportService —— 导出交互层：查找目标、弹系统对话框；
// 渲染与落盘机制在 session 包（ExportMarkdown）与 schema 包（WriteImage）。
type ExportService struct{}

// ExportSession 导出会话为 Markdown：弹出系统保存对话框，
// 用户确认后委托 session.ExportMarkdown 渲染并写盘。
// 返回保存路径（"" 表示用户取消）。
func (s *ExportService) ExportSession(sessionID string) (string, error) {
	sess, ok := boot.GetApp().FindSession(sessionID)
	if !ok {
		return "", fmt.Errorf("session not found: %s", sessionID)
	}

	// 弹出保存对话框，默认文件名从标题生成
	target, err := application.Get().Dialog.SaveFile().
		SetMessage("导出对话").
		SetFilename(sanitizeFilename(sess.Title)+".md").
		AddFilter("Markdown 文件", "*.md").
		SetButtonText("导出").
		CanCreateDirectories(true).
		PromptForSingleSelection()
	if err != nil {
		if isDialogCancelled(err) {
			return "", nil // 用户取消
		}
		return "", fmt.Errorf("save dialog: %w", err)
	}
	if target == "" {
		return "", nil // 用户取消
	}

	if err := session.ExportMarkdown(sess, target); err != nil {
		return "", err
	}
	return target, nil
}

// SaveImage 把 data URL 图片保存到用户选择的位置（消息图片右键"保存图片"）。
// 解析扩展名用于对话框默认文件名；解析与落盘机制在 schema 包（WriteImage）。
// 返回保存路径（"" 表示用户取消）。
func (s *ExportService) SaveImage(dataURL string) (string, error) {
	_, ext, _, err := schema.ParseImageDataURL(dataURL)
	if err != nil {
		return "", err
	}

	target, err := application.Get().Dialog.SaveFile().
		SetMessage("保存图片").
		SetFilename("image"+ext).
		AddFilter("图片", "*"+ext).
		AddFilter("所有文件", "*.*").
		SetButtonText("保存").
		CanCreateDirectories(true).
		PromptForSingleSelection()
	if err != nil {
		if isDialogCancelled(err) {
			return "", nil // 用户取消
		}
		return "", fmt.Errorf("save dialog: %w", err)
	}
	if target == "" {
		return "", nil // 用户取消
	}

	if _, err := schema.WriteImage(dataURL, target); err != nil {
		return "", err
	}
	return target, nil
}

// sanitizeFilename 把会话标题转换为安全的文件名（去除非法字符、限长）。
var invalidFilenameChars = regexp.MustCompile(`[<>:"/\\|?*\x00-\x1f]`)

func sanitizeFilename(title string) string {
	name := invalidFilenameChars.ReplaceAllString(title, "")
	name = strings.TrimSpace(name)
	if name == "" {
		name = "session"
	}
	// Windows 文件名上限 255，给扩展名和路径留余量
	runes := []rune(name)
	if len(runes) > 60 {
		name = string(runes[:60])
	}
	// 去掉尾部点/空格（Windows 不允许）
	name = strings.TrimRight(name, ". ")
	if name == "" {
		name = "session"
	}
	return filepath.Base(name) // 双保险：去掉任何路径成分
}
