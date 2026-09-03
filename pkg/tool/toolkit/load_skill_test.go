package toolkit

import (
	"context"
	"encoding/json"
	"tars/pkg/skill"
	"testing"
)

// mockSkillRuntime 实现 skill.Provider：记录 Load 调用次数以验证幂等；
// 同时记录 WriteSkill 调用参数（write_skill 测试复用本 mock）。
type mockSkillRuntime struct {
	loaded       map[string]bool
	loadCount    int
	loadedNames  []string
	writeCalled  bool
	gotWriteName string
	gotOverwrite bool
	writeCreated bool
	writeErr     error
}

func newMockSkillRuntime() *mockSkillRuntime {
	return &mockSkillRuntime{loaded: map[string]bool{}}
}

func (m *mockSkillRuntime) Load(name string) (string, error) {
	m.loadCount++
	return "# SKILL " + name + "\n\nbody\n", nil
}

func (m *mockSkillRuntime) WriteSkill(name, description, category, body string, overwrite bool) (bool, error) {
	m.writeCalled = true
	m.gotWriteName = name
	m.gotOverwrite = overwrite
	return m.writeCreated, m.writeErr
}

func (m *mockSkillRuntime) IsSkillLoaded(name string) bool { return m.loaded[name] }
func (m *mockSkillRuntime) MarkSkillLoaded(name string)    { m.loaded[name] = true }
func (m *mockSkillRuntime) GetLoadedSkills() []string {
	m.loadedNames = m.loadedNames[:0]
	for n := range m.loaded {
		m.loadedNames = append(m.loadedNames, n)
	}
	return m.loadedNames
}
func (m *mockSkillRuntime) Search(query string, limit int) ([]skill.Summary, error) {
	return []skill.Summary{{Name: "pptx", Description: "slides", Category: "docs"}}, nil
}
func (m *mockSkillRuntime) SearchLimit() int { return 5 }

func callLoadSkill(t *testing.T, rt skill.Provider, name string) string {
	t.Helper()
	args, _ := json.Marshal(map[string]string{"name": name})
	out, err := NewSkillTool(rt).definition().Handler(context.Background(), args)
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
	if !rt.IsSkillLoaded("pptx") {
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
	if _, err := NewSkillTool(rt).definition().Handler(context.Background(), json.RawMessage(`{}`)); err == nil {
		t.Error("expected error for missing name")
	}
}

func TestLoadSkill_NoRuntime(t *testing.T) {
	args, _ := json.Marshal(map[string]string{"name": "x"})
	if _, err := NewSkillTool(nil).definition().Handler(context.Background(), args); err == nil {
		t.Error("expected error without a skill runtime")
	}
}
