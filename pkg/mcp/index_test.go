package mcp

import (
	"fmt"
	"strings"
	"testing"
)

func TestRenderIndex(t *testing.T) {
	// 无启用服务器：静默（不向模型提及 MCP 的存在）
	m := newTestManager(t, map[string]*ServerConfig{
		"off": {Command: "x"},
	})
	if got := m.RenderIndex(); got != "" {
		t.Fatalf("no enabled servers should render empty, got %q", got)
	}

	// 启用服务器：name [type] — desc (N tools)；禁用项不出现
	m2 := newTestManager(t, map[string]*ServerConfig{
		"yahoo-finance": {
			Command: "npx", Enabled: true, SourceType: "query",
			Description: "stock quotes and financial data",
		},
		"off": {Command: "x"},
	})
	got := m2.RenderIndex()
	if !strings.Contains(got, "**yahoo-finance** [query] — stock quotes and financial data (0 tools)") {
		t.Errorf("server line malformed:\n%s", got)
	}
	if strings.Contains(got, "off") {
		t.Errorf("disabled server should not appear:\n%s", got)
	}
	if !strings.Contains(got, "discover_tools") {
		t.Errorf("header should point at discover_tools:\n%s", got)
	}
}

// TestRenderIndex_ToolNames 探测缓存的工具名内联进索引行（skills Tier2 索引
// 行内联技能名同款）：description 为空时模型也能凭工具名判断能力轮廓；
// 超过上限打 …；未探测维持 "(0 tools)"。
func TestRenderIndex_ToolNames(t *testing.T) {
	m := newTestManager(t, map[string]*ServerConfig{
		"fetch": {Command: "x", Enabled: true, SourceType: "read"}, // 无 description
		"big":   {Command: "x", Enabled: true},
		"fresh": {Command: "x", Enabled: true}, // 未探测
	})
	seedCache(t, m, "fetch", &ToolInfo{Name: "fetch"})
	big := make([]*ToolInfo, 0, maxIndexToolNames+5)
	for i := range maxIndexToolNames + 5 {
		big = append(big, &ToolInfo{Name: fmt.Sprintf("tool_%02d", i)})
	}
	seedCache(t, m, "big", big...)

	got := m.RenderIndex()

	// 无 description：行 = name [type] (N tools: names)
	if !strings.Contains(got, "**fetch** [read] (1 tool: fetch)") {
		t.Errorf("tool name should be inlined:\n%s", got)
	}
	// 上限截断：前 maxIndexToolNames 个 + …（tool_19 在，tool_20 不在）
	if !strings.Contains(got, "tool_19") || strings.Contains(got, "tool_20") {
		t.Errorf("tool names should be capped at %d:\n%s", maxIndexToolNames, got)
	}
	if !strings.Contains(got, ", …)") {
		t.Errorf("truncation marker missing:\n%s", got)
	}
	// 未探测：无名字，维持 (0 tools)
	if !strings.Contains(got, "**fresh** (0 tools)") {
		t.Errorf("unprobed server should render (0 tools):\n%s", got)
	}
}
