package mcp

import (
	"testing"
)

// seedCache 往缓存注册表追加一个服务器的工具清单（不走 Probe，避免子进程
// 依赖）。基于当前缓存 Clone 累加：Registry 整体替换语义下，全新表会覆盖
// 已有条目。
func seedCache(t *testing.T, m *Manager, server string, tools ...*ToolInfo) {
	t.Helper()
	reg := m.GetRegistry().Clone()
	reg.Servers[server] = &ServerTools{ProbedAt: "2026-08-19 18:00:00", Tools: tools}
	if err := reg.Save(); err != nil {
		t.Fatal(err)
	}
	m.registry.Store(reg)
}

func TestSearch_Tools(t *testing.T) {
	m := newTestManager(t, map[string]*ServerConfig{
		"yahoo-finance": {Command: "x", Enabled: true, SourceType: "query", Description: "stock data"},
		"disabled-srv":  {Command: "x"},
	})
	seedCache(t, m, "yahoo-finance",
		&ToolInfo{Name: "get_stock_price", Description: "current stock price quote", InputSchema: map[string]any{"type": "object"}},
		&ToolInfo{Name: "get_news", Description: "latest financial news"},
	)
	seedCache(t, m, "disabled-srv",
		&ToolInfo{Name: "get_stock_chart", Description: "stock chart image"},
	)

	hits := m.Search("stock price", 5)
	if len(hits) == 0 || hits[0].FullName != "mcp__yahoo-finance__get_stock_price" {
		t.Fatalf("expected stock price tool first, got %+v", hits)
	}
	h := hits[0]
	if h.Server != "yahoo-finance" || h.Name != "get_stock_price" || h.SourceType != "query" {
		t.Errorf("hit metadata wrong: %+v", h)
	}
	if h.InputSchema == nil {
		t.Error("hit should carry the cached input schema")
	}

	// 禁用服务器的工具不参与命中
	for _, h := range hits {
		if h.Server == "disabled-srv" {
			t.Errorf("disabled server tool leaked into results: %+v", h)
		}
	}

	// 服务器名参与索引：按服务器名搜也能命中其工具
	hits = m.Search("yahoo finance", 5)
	if len(hits) == 0 {
		t.Fatal("server name should be searchable")
	}

	// 无命中
	if hits := m.Search("zzz nothing", 5); len(hits) != 0 {
		t.Errorf("unexpected hits: %+v", hits)
	}
}

func TestSearch_UnprobedServer(t *testing.T) {
	m := newTestManager(t, map[string]*ServerConfig{
		"fresh": {Command: "x", Enabled: true},
	})
	// 未探测：无缓存工具，检索为空（而非报错）
	if hits := m.Search("anything", 5); len(hits) != 0 {
		t.Errorf("unprobed server should have no searchable tools: %+v", hits)
	}
}

func TestFullToolName(t *testing.T) {
	if got := FullToolName("yahoo-finance", "get_stock_price"); got != "mcp__yahoo-finance__get_stock_price" {
		t.Errorf("FullToolName = %q", got)
	}
}
