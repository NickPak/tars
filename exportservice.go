package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"tars/pkg/store"
	"time"

	"tars/internal/session"

	"github.com/cloudwego/eino/schema"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// ExportSession renders the session as Markdown, prompts the user
// for a destination via the OS save dialog, and writes the file.
// Returns the chosen path ("" if the user cancelled).
func (s *AgentService) ExportSession(sessionID string) (string, error) {
	// 渲染 Markdown（拷贝切片头做只读快照）
	sess, ok := session.GetManager().Find(sessionID)
	if !ok {
		return "", fmt.Errorf("session not found: %s", sessionID)
	}
	title := sess.Title
	msgs := append([]*store.Message{}, sess.Messages...)

	md := renderSessionMarkdown(title, msgs)

	// 弹出保存对话框，默认文件名从标题生成
	target, err := application.Get().Dialog.SaveFile().
		SetMessage("导出对话").
		SetFilename(sanitizeFilename(title)+".md").
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

// renderSessionMarkdown 把会话消息渲染为可读的 Markdown 文档：
//   - user → ## 👤 用户
//   - assistant → ## 🤖 TARS（工具调用以状态行 + 折叠块呈现）
//   - tool 消息不单独渲染（其结果已通过 assistant 的 ToolCalls.Output 合并）
func renderSessionMarkdown(title string, msgs []*store.Message) string {
	var b strings.Builder
	b.WriteString("# " + title + "\n\n")
	if len(msgs) > 0 {
		b.WriteString("> 导出于 " + time.Now().Format("2006-01-02 15:04") + "\n")
	}
	b.WriteString("\n---\n\n")

	for _, m := range msgs {
		switch m.Role {
		case schema.User:
			b.WriteString("## 👤 用户\n\n")
			b.WriteString(strings.TrimSpace(m.Content) + "\n\n")

		case schema.Assistant:
			b.WriteString("## 🤖 TARS\n\n")
			if m.Reasoning != "" {
				reasoning := m.Reasoning
				if len(reasoning) > 3000 {
					reasoning = reasoning[:3000] + "\n…(已截断)"
				}
				b.WriteString("<details><summary>💭 思考过程</summary>\n\n")
				b.WriteString(reasoning + "\n\n</details>\n\n")
			}
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
				}
			}
			if strings.TrimSpace(m.Content) != "" {
				b.WriteString(strings.TrimSpace(m.Content) + "\n\n")
			}
		}
		// tool/system 角色不导出（工具结果已合并进 assistant 消息）
	}
	return b.String()
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
