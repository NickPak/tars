package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// ExportConversation renders the conversation as Markdown, prompts the user
// for a destination via the OS save dialog, and writes the file.
// Returns the chosen path ("" if the user cancelled).
func (s *AgentService) ExportConversation(conversationID string) (string, error) {
	conv, ok := s.getConversation(conversationID)
	if !ok {
		return "", fmt.Errorf("conversation not found: %s", conversationID)
	}

	// 渲染 Markdown（只读快照，避免持锁期间做字符串拼接）
	s.mu.RLock()
	title := conv.Title
	msgs := append([]Message{}, conv.Messages...)
	s.mu.RUnlock()

	md := renderConversationMarkdown(title, msgs)

	// 弹出保存对话框，默认文件名从标题生成
	target, err := application.Get().Dialog.SaveFile().
		SetMessage("导出对话").
		SetFilename(sanitizeFilename(title) + ".md").
		AddFilter("Markdown 文件", "*.md").
		SetButtonText("导出").
		CanCreateDirectories(true).
		PromptForSingleSelection()
	if err != nil {
		return "", fmt.Errorf("save dialog: %w", err)
	}
	if target == "" {
		return "", nil // 用户取消
	}

	if err := os.WriteFile(target, []byte(md), 0644); err != nil {
		return "", fmt.Errorf("write export file: %w", err)
	}
	return target, nil
}

// renderConversationMarkdown 把会话消息渲染为可读的 Markdown 文档：
//   - user → ## 👤 用户
//   - assistant → ## 🤖 TARS（工具调用以状态行 + 折叠块呈现）
//   - tool 消息不单独渲染（其结果已通过 assistant 的 ToolCalls.Output 合并）
func renderConversationMarkdown(title string, msgs []Message) string {
	var b strings.Builder
	b.WriteString("# " + title + "\n\n")
	if len(msgs) > 0 {
		b.WriteString("> 导出于 " + time.Now().Format("2006-01-02 15:04") + "\n")
	}
	b.WriteString("\n---\n\n")

	for _, m := range msgs {
		switch m.Role {
		case RoleUser:
			b.WriteString("## 👤 用户\n\n")
			b.WriteString(strings.TrimSpace(m.Content) + "\n\n")

		case RoleAssistant:
			b.WriteString("## 🤖 TARS\n\n")
			if len(m.ToolCalls) > 0 {
				for _, tc := range m.ToolCalls {
					b.WriteString("**🔧 " + tc.Name + "**")
					if tc.Args != "" {
						args := tc.Args
						if len(args) > 120 {
							args = args[:120] + "…"
						}
						b.WriteString(" `" + args + "`")
					}
					b.WriteString("\n\n")
					if tc.Output != "" {
						out := tc.Output
						if len(out) > 2000 {
							out = out[:2000] + "\n…(已截断)"
						}
						b.WriteString("<details><summary>执行结果</summary>\n\n```\n")
						b.WriteString(out + "\n```\n\n</details>\n\n")
					}
				}
			}
			if strings.TrimSpace(m.Content) != "" {
				b.WriteString(strings.TrimSpace(m.Content) + "\n\n")
			}
		}
		// RoleTool / RoleSystem 不导出（工具结果已合并进 assistant 消息）
	}
	return b.String()
}

// sanitizeFilename 把会话标题转换为安全的文件名（去除非法字符、限长）。
var invalidFilenameChars = regexp.MustCompile(`[<>:"/\\|?*\x00-\x1f]`)

func sanitizeFilename(title string) string {
	name := invalidFilenameChars.ReplaceAllString(title, "")
	name = strings.TrimSpace(name)
	if name == "" {
		name = "conversation"
	}
	// Windows 文件名上限 255，给扩展名和路径留余量
	runes := []rune(name)
	if len(runes) > 60 {
		name = string(runes[:60])
	}
	// 去掉尾部点/空格（Windows 不允许）
	name = strings.TrimRight(name, ". ")
	if name == "" {
		name = "conversation"
	}
	return filepath.Base(name) // 双保险：去掉任何路径成分
}
