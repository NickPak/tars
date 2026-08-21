package tools

import (
	"context"
	"encoding/json"
	"tars/pkg/skills"
	"testing"
)

// mockSkillRuntime 实现 SkillRuntime：记录 Load 调用次数以验证幂等。
type mockSkillRuntime struct {
	loaded      map[string]bool
	loadCount   int
	loadedNames []string
}

func newMockSkillRuntime() *mockSkillRuntime {
	return &mockSkillRuntime{loaded: map[string]bool{}}
}

func (m *mockSkillRuntime) Load(name string) (string, error) {
	m.loadCount++
	return "# SKILL " + name + "\n\nbody\n", nil
}

func (m *mockSkillRuntime) IsLoaded(name string) bool { return m.loaded[name] }
func (m *mockSkillRuntime) MarkLoaded(name string)    { m.loaded[name] = true }
func (m *mockSkillRuntime) Loaded() []string {
	m.loadedNames = m.loadedNames[:0]
	for n := range m.loaded {
		m.loadedNames = append(m.loadedNames, n)
	}
	return m.loadedNames
}
func (m *mockSkillRuntime) Search(query string, limit int) ([]skills.SkillSummary, error) {
	return []skills.SkillSummary{{Name: "pptx", Description: "slides", Category: "docs"}}, nil
}
func (m *mockSkillRuntime) SearchLimit() int { return 5 }

func callLoadSkill(t *testing.T, rt skills.SkillRuntime, name string) string {
	t.Helper()
	ctx := WithEnv(context.Background(), &Env{Skills: rt})
	args, _ := json.Marshal(map[string]string{"name": name})
	out, err := LoadSkill().Handler(ctx, args)
	if err != nil {
		t.Fatalf("load_skill(%q): %v", name, err)
	}
	return out
}

func TestLoadSkill_Idempotent(t *testing.T) {
	rt := newMockSkillRuntime()

	out1 := callLoadSkill(t, rt, "pptx")
	if rt.loadCount != 1 {
		t.Fatalf("first load should read file once, got %d", rt.loadCount)
	}
	if !rt.IsLoaded("pptx") {
		t.Error("pptx should be marked loaded")
	}

	// 二次调用：不再读文件，返回短结果
	out2 := callLoadSkill(t, rt, "pptx")
	if rt.loadCount != 1 {
		t.Fatalf("second load must not re-read, loadCount=%d", rt.loadCount)
	}
	if out2 == out1 {
		t.Error("second call should return the short 'already loaded' notice, not full content")
	}
}

func TestLoadSkill_RequiresName(t *testing.T) {
	rt := newMockSkillRuntime()
	ctx := WithEnv(context.Background(), &Env{Skills: rt})
	if _, err := LoadSkill().Handler(ctx, json.RawMessage(`{}`)); err == nil {
		t.Error("expected error for missing name")
	}
}

func TestLoadSkill_NoRuntime(t *testing.T) {
	args, _ := json.Marshal(map[string]string{"name": "x"})
	if _, err := LoadSkill().Handler(context.Background(), args); err == nil {
		t.Error("expected error without a skill runtime in ctx")
	}
}
