package mcp

import (
	"fmt"
	"strings"
)

// maxIndexToolNames 是索引行内联的工具名上限：工具名是服务器自报的一手
// 元信息（模型判断"何时该用这台服务器"的主要依据，与 skills Tier2 索引行
// 内联全部技能名同款思路），但工具量大的服务器不能撑爆行——超出部分以
// … 收尾，完整清单仍由 discover_tools 渐进披露。
const maxIndexToolNames = 20

// RenderIndex 渲染 MCP 服务器级索引（系统消息静态前缀段，3.1 发现体系）：
// 每行只含"服务器名 [信息源类型]：一句话描述 (N tools: 工具名清单)"——
// description 由用户声明（可空），工具名来自探测缓存（服务器自报），
// 即使描述为空模型也能凭工具名判断服务器能力轮廓。
// 参数级 schema 不放索引——完整 schema 经 discover_tools 渐进披露。
// 与 skills 的 INDEX.md 不同：MCP 索引由配置+探测缓存直接渲染（无磁盘
// 生成物与生成时机问题）。空集返回空串（静默，不向模型提及 MCP 的存在
// ——能力默认关闭）。
func (m *Manager) RenderIndex() string {
	list := m.Enabled()
	if len(list) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("# Available MCP Servers\n\n")
	b.WriteString("Each line is an external tool server: name, source type, capability summary and its ")
	b.WriteString("tool names (self-reported by the server). Parameter schemas are NOT shown here — ")
	b.WriteString("use `discover_tools` to find a tool by need; it gets registered for direct call.\n\n")
	for _, srv := range list {
		fmt.Fprintf(&b, "- **%s**", srv.Name)
		if srv.SourceType != "" {
			fmt.Fprintf(&b, " [%s]", srv.SourceType)
		}
		if srv.Description != "" {
			fmt.Fprintf(&b, " — %s", srv.Description)
		}
		fmt.Fprintf(&b, " (%d tool", srv.ToolCount)
		if srv.ToolCount != 1 {
			b.WriteString("s")
		}
		if names := m.toolNames(srv.Name); len(names) > 0 {
			fmt.Fprintf(&b, ": %s", strings.Join(names, ", "))
			if srv.ToolCount > len(names) {
				b.WriteString(", …")
			}
		}
		b.WriteString(")\n")
	}
	return b.String()
}

// toolNames 返回服务器探测缓存中的工具名（最多 maxIndexToolNames 个，
// 按探测顺序）。未探测返回 nil（索引行只落 "(0 tools)"）。
func (m *Manager) toolNames(server string) []string {
	reg := m.GetRegistry()
	if reg == nil {
		return nil
	}
	st, ok := reg.FindServer(server)
	if !ok {
		return nil
	}
	names := make([]string, 0, min(maxIndexToolNames, len(st.Tools)))
	for i, t := range st.Tools {
		if i >= maxIndexToolNames {
			break
		}
		names = append(names, t.Name)
	}
	return names
}
