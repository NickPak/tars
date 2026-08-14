package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestDiscoverTools_ReturnsCandidates(t *testing.T) {
	rt := newMockSkillRuntime()
	ctx := WithSkillRuntime(context.Background(), rt)
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
	ctx := WithSkillRuntime(context.Background(), rtNoMatch)
	args, _ := json.Marshal(map[string]string{"query": "xyzzy"})

	out, err := DiscoverTools().Handler(ctx, args)
	if err != nil {
		t.Fatalf("discover_tools: %v", err)
	}
	if !strings.Contains(out, "未找到") {
		t.Errorf("expected no-match notice, got:\n%s", out)
	}
	_ = rt
}

func TestDiscoverTools_RequiresQuery(t *testing.T) {
	rt := newMockSkillRuntime()
	ctx := WithSkillRuntime(context.Background(), rt)
	if _, err := DiscoverTools().Handler(ctx, json.RawMessage(`{}`)); err == nil {
		t.Error("expected error for missing query")
	}
}

// mockSkillRuntimeSearch 允许自定义 Search 结果。
type mockSkillRuntimeSearch struct {
	mockSkillRuntime
	results []SkillSummary
}

func (m *mockSkillRuntimeSearch) Search(query string, limit int) ([]SkillSummary, error) {
	return m.results, nil
}
