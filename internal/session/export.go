package session

import (
	"fmt"
	"os"
	"strings"
	"time"

	"tars/pkg/schema"
)

// ExportMarkdown 会话导出机制的落盘实现：把会话渲染为可读的
// Markdown 文档并写入 target。交互职责（查找会话、保存对话框、
// 文件名生成）在服务层（main 包 ExportService），本函数只做
// 渲染与写文件。
func ExportMarkdown(sess *Data, target string) error {
	// 拷贝切片头做只读快照（导出期间会话可能有新消息追加）
	msgs := append([]*schema.Message{}, sess.Messages...)
	md := renderSessionMarkdown(sess.Title, msgs)
	if err := os.WriteFile(target, []byte(md), 0644); err != nil {
		return fmt.Errorf("write export file: %w", err)
	}
	return nil
}

// renderSessionMarkdown 把会话消息渲染为可读的 Markdown 文档：
//   - user → ## 👤 用户
//   - assistant → ## 🤖 TARS（工具调用以状态行 + 折叠块呈现）
//   - tool 消息不单独渲染（其结果已通过 assistant 的 ToolCalls.Output 合并）
func renderSessionMarkdown(title string, msgs []*schema.Message) string {
	var b strings.Builder
	b.WriteString("# " + title + "\n\n")
	if len(msgs) > 0 {
		b.WriteString("> 导出于 " + time.Now().Format("2006-01-02 15:04") + "\n")
	}
	b.WriteString("\n---\n\n")

	for _, m := range msgs {
		switch m.Role {
		case schema.RoleUser:
			b.WriteString("## 👤 用户\n\n")
			b.WriteString(strings.TrimSpace(m.Content) + "\n\n")

		case schema.RoleAssistant:
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
