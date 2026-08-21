package tools

import (
	"context"
	"encoding/json"
	"strings"
	"tars/pkg/skills"
	"testing"
)

func TestDiscoverTools_ReturnsCandidates(t *testing.T) {
	rt := newMockSkillRuntime()
	ctx := WithEnv(context.Background(), &Env{Skills: rt})
	args, _ := json.Marshal(map[string]string{"query": "make a presentation"})

	out, err := DiscoverTools().Handler(ctx, args)
	if err != nil {
		t.Fatalf("discover_tools: %v", err)
	}
	if !strings.Contains(out, "pptx") {
		t.Errorf("expected pptx candidate, got:\n%s", out)
	}
	if !strings.Contains(out, "load_skill") {
		t.Errorf("should hint load_skill, got:\n%s", out)
	}
}

func TestDiscoverTools_NoMatch(t *testing.T) {
	rt := &mockSkillRuntime{loaded: map[string]bool{}}
	// 让 Search 返回空：临时替换——用带 searchResults 的 mock
	rtNoMatch := &mockSkillRuntimeSearch{results: nil}
	ctx := WithEnv(context.Background(), &Env{Skills: rtNoMatch})
	args, _ := json.Marshal(map[string]string{"query": "xyzzy"})

	out, err := DiscoverTools().Handler(ctx, args)
	if err != nil {
		t.Fatalf("discover_tools: %v", err)
	}
	if !strings.Contains(out, "No matching capabilities") {
		t.Errorf("expected no-match notice, got:\n%s", out)
	}
	_ = rt
}

// MCP 命中：Materialize 被调用、输出标注已注册、与技能候选同屏。
func TestDiscoverTools_MCPHit(t *testing.T) {
	mcpRt := &mockMCPRuntime{
		hits: []MCPToolHit{{
			Server: "yahoo-finance", Name: "get_stock_price",
			FullName:    "mcp__yahoo-finance__get_stock_price",
			Description: "current stock price", SourceType: "query",
		}},
	}
	rt := newMockSkillRuntime()
	ctx := WithEnv(context.Background(), &Env{Skills: rt, MCP: mcpRt})
	args, _ := json.Marshal(map[string]string{"query": "stock price"})

	out, err := DiscoverTools().Handler(ctx, args)
	if err != nil {
		t.Fatalf("discover_tools: %v", err)
	}
	if !strings.Contains(out, "mcp__yahoo-finance__get_stock_price") {
		t.Errorf("expected MCP tool in output, got:\n%s", out)
	}
	if !strings.Contains(out, "now registered") {
		t.Errorf("should note registration, got:\n%s", out)
	}
	if len(mcpRt.materialized) != 1 || mcpRt.materialized[0] != "mcp__yahoo-finance__get_stock_price" {
		t.Errorf("Materialize should be called once with the hit, got %v", mcpRt.materialized)
	}
	// 与技能候选同屏（mock skills 总返回 pptx）
	if !strings.Contains(out, "pptx") {
		t.Errorf("skill candidates should coexist, got:\n%s", out)
	}
}

// mockMCPRuntime 记录 Materialize 调用。
type mockMCPRuntime struct {
	hits         []MCPToolHit
	materialized []string
}

func (m *mockMCPRuntime) Search(query string, limit int) ([]MCPToolHit, error) {
	return m.hits, nil
}
func (m *mockMCPRuntime) Materialize(hit MCPToolHit) error {
	m.materialized = append(m.materialized, hit.FullName)
	return nil
}
func (m *mockMCPRuntime) MaterializedNames() []string { return m.materialized }

func TestDiscoverTools_RequiresQuery(t *testing.T) {
	rt := newMockSkillRuntime()
	ctx := WithEnv(context.Background(), &Env{Skills: rt})
	if _, err := DiscoverTools().Handler(ctx, json.RawMessage(`{}`)); err == nil {
		t.Error("expected error for missing query")
	}
}

// mockSkillRuntimeSearch 允许自定义 Search 结果。
type mockSkillRuntimeSearch struct {
	mockSkillRuntime
	results []skills.SkillSummary
}

func (m *mockSkillRuntimeSearch) Search(query string, limit int) ([]skills.SkillSummary, error) {
	return m.results, nil
}
