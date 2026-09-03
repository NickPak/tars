package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newWriteTestManager(t *testing.T) *Manager {
	t.Helper()
	m := NewManager(t.TempDir(), NewConfig())
	if err := m.Startup(); err != nil {
		t.Fatalf("startup: %v", err)
	}
	return m
}

func TestWriteSkill_CreateDisabledByDefault(t *testing.T) {
	m := newWriteTestManager(t)

	created, err := m.WriteSkill("deploy-app", "Deploy the app: use when shipping", "", "# Deploy\n\n1. build\n2. ship", false)
	if err != nil {
		t.Fatalf("WriteSkill: %v", err)
	}
	if !created {
		t.Error("first write should report created=true")
	}

	// 文件落盘且 frontmatter 可回读（格式单一事实源：ParseFrontmatter）
	raw, err := os.ReadFile(filepath.Join(m.SkillDir("deploy-app"), skillFile))
	if err != nil {
		t.Fatalf("read SKILL.md: %v", err)
	}
	name, desc, err := ParseFrontmatter(raw)
	if err != nil {
		t.Fatalf("generated SKILL.md must round-trip through ParseFrontmatter: %v", err)
	}
	if name != "deploy-app" || desc != "Deploy the app: use when shipping" {
		t.Errorf("frontmatter mismatch: %q / %q", name, desc)
	}

	// 新建默认禁用、来源 agent
	info, ok := m.GetRegistry().FindSkill("deploy-app")
	if !ok {
		t.Fatal("registry entry missing")
	}
	if info.Enabled {
		t.Error("new skill must be disabled by default (human gate before visibility)")
	}
	if info.Source != "agent" {
		t.Errorf("source = %q, want agent", info.Source)
	}

	// 禁用技能不进索引（无启用技能时索引为空文件）
	if idx := m.RenderIndex(); idx != "" {
		t.Errorf("disabled skill must not appear in index, got: %q", idx)
	}
}

func TestWriteSkill_OverwritePreservesState(t *testing.T) {
	m := newWriteTestManager(t)
	if _, err := m.WriteSkill("deploy-app", "v1", "devops", "body v1", false); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := m.SetEnabled("deploy-app", true); err != nil {
		t.Fatalf("enable: %v", err)
	}

	created, err := m.WriteSkill("deploy-app", "v2", "", "body v2", true)
	if err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	if created {
		t.Error("overwrite should report created=false")
	}

	info, _ := m.GetRegistry().FindSkill("deploy-app")
	if !info.Enabled {
		t.Error("overwrite must preserve enabled state")
	}
	if info.Category != "devops" {
		t.Errorf("overwrite without category must preserve old category, got %q", info.Category)
	}
	raw, _ := os.ReadFile(filepath.Join(m.SkillDir("deploy-app"), skillFile))
	if !strings.Contains(string(raw), "body v2") {
		t.Error("SKILL.md body not updated")
	}
	// 启用中的技能覆盖后仍应出现在索引里（新 description）
	if idx := m.RenderIndex(); !strings.Contains(idx, "v2") {
		t.Errorf("index should reflect updated description, got: %q", idx)
	}
}

func TestWriteSkill_ExistingRequiresOverwrite(t *testing.T) {
	m := newWriteTestManager(t)
	if _, err := m.WriteSkill("deploy-app", "v1", "", "body", false); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := m.WriteSkill("deploy-app", "v2", "", "body", false); err == nil {
		t.Error("existing skill without overwrite=true must fail")
	}
}

func TestWriteSkill_Validation(t *testing.T) {
	m := newWriteTestManager(t)
	cases := []struct {
		name, desc, body string
	}{
		{"Bad Name", "d", "body"},                     // 非法名
		{"ok-name", "", "body"},                       // 空 description
		{"ok-name", "d", "   "},                       // 空 body
		{"ok-name", strings.Repeat("x", 501), "body"}, // 超长 description
	}
	for _, tc := range cases {
		if _, err := m.WriteSkill(tc.name, tc.desc, "", tc.body, false); err == nil {
			t.Errorf("expected validation error for %+v", tc)
		}
	}
}

func TestWriteSkill_FrontmatterEscapesSpecialChars(t *testing.T) {
	m := newWriteTestManager(t)
	desc := "Deploy: use when shipping \"production\" — 含中文与引号"
	if _, err := m.WriteSkill("deploy-app", desc, "", "body", false); err != nil {
		t.Fatalf("WriteSkill: %v", err)
	}
	raw, _ := os.ReadFile(filepath.Join(m.SkillDir("deploy-app"), skillFile))
	_, got, err := ParseFrontmatter(raw)
	if err != nil {
		t.Fatalf("round-trip: %v", err)
	}
	if got != desc {
		t.Errorf("special chars must survive yaml round-trip: %q", got)
	}
}
