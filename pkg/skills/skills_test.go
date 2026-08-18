package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mustInit(t *testing.T) *Manager {
	t.Helper()
	dir := t.TempDir()
	cfg := &Config{}
	cfg.Validate() // 与生产路径一致：零值修正为默认阈值
	s, err := NewManager(dir, cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return s
}

// addSkill 往 registry 登记一个完整技能（等价于安装后的内存状态）。
func addSkill(t *testing.T, s *Manager, name, desc, category string) {
	t.Helper()
	if err := s.AddSkill(name, &SkillMeta{
		Name:        name,
		Description: desc,
		Category:    category,
		Source:      "local",
		InstalledAt: "2026-08-13",
		Enabled:     true,
	}); err != nil {
		t.Fatalf("AddSkill: %v", err)
	}
}

func TestSetCategory(t *testing.T) {
	s := mustInit(t)
	addSkill(t, s, "pptx", "Create presentations", "documents")

	// 改分类：归一化（大小写/空白）+ 内存与磁盘同步
	if err := s.SetCategory("pptx", "  Data "); err != nil {
		t.Fatal(err)
	}
	if got := s.GetRegistry().Skills["pptx"].Category; got != "data" {
		t.Fatalf("memory category = %q, want data", got)
	}
	fresh := NewRegistry(s.RootDir())
	if err := fresh.Load(); err != nil {
		t.Fatal(err)
	}
	if got := fresh.Skills["pptx"].Category; got != "data" {
		t.Fatalf("disk category = %q, want data", got)
	}

	// 空值归一为 misc
	if err := s.SetCategory("pptx", ""); err != nil {
		t.Fatal(err)
	}
	if got := s.GetRegistry().Skills["pptx"].Category; got != "misc" {
		t.Fatalf("empty category = %q, want misc", got)
	}

	// 不存在的技能报错
	if err := s.SetCategory("nope", "data"); err == nil {
		t.Fatal("expected error for unknown skill")
	}
}

func TestSetEnabled(t *testing.T) {
	s := mustInit(t)
	addSkill(t, s, "pptx", "Create presentations", "documents")
	addSkill(t, s, "docx", "Edit Word documents", "documents")
	if err := s.GenerateIndex(); err != nil {
		t.Fatal(err)
	}

	// 禁用：内存与磁盘同步，索引/检索/加载均排除
	if err := s.SetEnabled("pptx", false); err != nil {
		t.Fatal(err)
	}
	if s.GetRegistry().Skills["pptx"].Enabled {
		t.Fatal("memory: pptx should be disabled")
	}
	fresh := NewRegistry(s.RootDir())
	if err := fresh.Load(); err != nil {
		t.Fatal(err)
	}
	if fresh.Skills["pptx"].Enabled {
		t.Fatal("disk: pptx should be disabled")
	}
	if len(s.Enabled()) != 1 {
		t.Fatalf("Enabled() = %d, want 1", len(s.Enabled()))
	}
	if strings.Contains(s.RenderIndex(), "pptx") {
		t.Fatal("index should not contain disabled skill")
	}
	if _, err := s.LoadSkill("pptx"); err == nil {
		t.Fatal("LoadSkill should reject disabled skill")
	}
	if got, _ := s.Search("presentations", 5); len(got) != 0 {
		t.Fatalf("Search should skip disabled skill, got %d", len(got))
	}
	// List 仍返回全部（GUI 展示用）
	if len(s.List()) != 2 {
		t.Fatalf("List() = %d, want 2", len(s.List()))
	}

	// 重新启用后恢复可见
	if err := s.SetEnabled("pptx", true); err != nil {
		t.Fatal(err)
	}
	if len(s.Enabled()) != 2 || !strings.Contains(s.RenderIndex(), "pptx") {
		t.Fatal("re-enable should restore visibility")
	}

	// 不存在的技能报错
	if err := s.SetEnabled("nope", false); err == nil {
		t.Fatal("expected error for unknown skill")
	}
}

// 旧版 registry.yaml 无 enabled 键：加载后缺省为启用（升级不能静默禁用已装技能）
func TestLegacyRegistryDefaultsEnabled(t *testing.T) {
	dir := t.TempDir()
	yamlText := "skills:\n  legacy:\n    category: documents\n    source: local\n    installed_at: 2026-08-13\n"
	if err := os.WriteFile(filepath.Join(dir, "registry.yaml"), []byte(yamlText), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry(dir)
	if err := reg.Load(); err != nil {
		t.Fatal(err)
	}
	if !reg.Skills["legacy"].Enabled {
		t.Fatal("legacy entry without enabled key should default to enabled")
	}
}

func TestParseFrontmatter(t *testing.T) {
	raw := []byte("---\nname: pptx\ndescription: Create and edit presentations\n---\n# Body\n")
	name, desc, err := ParseFrontmatter(raw)
	if err != nil {
		t.Fatalf("ParseFrontmatter: %v", err)
	}
	if name != "pptx" || desc != "Create and edit presentations" {
		t.Errorf("got name=%q desc=%q", name, desc)
	}
}

func TestParseFrontmatter_Errors(t *testing.T) {
	cases := []string{
		"# no frontmatter",
		"---\ndescription: x\n---\n",             // 缺 name
		"---\nname: PPTX\ndescription: x\n---\n", // 大写名
		"---\nname: pptx\n---\n",                 // 缺 description
		"---\nname: pptx\ndescription: x\n",      // 未闭合
	}
	for _, c := range cases {
		if _, _, err := ParseFrontmatter([]byte(c)); err == nil {
			t.Errorf("expected error for %q", c)
		}
	}
}

func TestList(t *testing.T) {
	s := mustInit(t)
	addSkill(t, s, "pptx", "slides", "documents")
	addSkill(t, s, "deploy-app", "deploy", "devops")

	list := s.List()
	if len(list) != 2 {
		t.Fatalf("want 2 skills, got %d", len(list))
	}
	if list[0].Name != "deploy-app" || list[1].Name != "pptx" {
		t.Errorf("unexpected order: %+v", list)
	}
	if list[1].Category != "documents" || list[1].Source != "local" || list[1].Description != "slides" {
		t.Errorf("skill fields wrong: %+v", list[1])
	}
}

func TestRegistry_RoundTrip(t *testing.T) {
	s := mustInit(t)
	addSkill(t, s, "x", "desc", "devops")

	info, ok := s.GetRegistry().FindSkill("x")
	if !ok || info.Category != "devops" {
		t.Errorf("roundtrip failed: %+v", info)
	}

	if err := s.RemoveSkill("x"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.GetRegistry().FindSkill("x"); ok {
		t.Error("RemoveSkill did not delete")
	}
}

func TestCategories_MergesRecommendedAndCustom(t *testing.T) {
	s := mustInit(t)

	// 空注册表：返回内置推荐集（固定顺序）
	base := s.Categories()
	if len(base) == 0 || base[0] != "documents" {
		t.Fatalf("expected recommended categories first, got %v", base)
	}

	// 混入：一个内置分类 + 一个自定义分类
	addSkill(t, s, "a", "d", "devops")
	addSkill(t, s, "b", "d", "my-custom")

	got := s.Categories()
	if !contains(got, "devops") || !contains(got, "my-custom") {
		t.Errorf("expected devops and my-custom, got %v", got)
	}
	if got[len(got)-1] != "my-custom" {
		t.Errorf("custom category should be last, got %v", got)
	}
	seen := map[string]bool{}
	for _, c := range got {
		if seen[c] {
			t.Errorf("duplicate category %q", c)
		}
		seen[c] = true
	}
}

// TestInit_RestoresFromDisk 验证核心语义：Init 以 registry.yaml 条目为准，
// 读磁盘 SKILL.md 补全规范内元信息（description）并缓存进内存。
func TestInit_RestoresFromDisk(t *testing.T) {
	workDir := t.TempDir()
	root := filepath.Join(workDir, skillsDir)

	// 磁盘目录：pptx 有 SKILL.md，deploy 缺失目录（幽灵条目应保留但 description 空）
	if err := os.MkdirAll(filepath.Join(root, "pptx"), 0755); err != nil {
		t.Fatal(err)
	}
	md := "---\nname: pptx\ndescription: Create presentations\n---\n# Body\n"
	if err := os.WriteFile(filepath.Join(root, "pptx", skillFile), []byte(md), 0644); err != nil {
		t.Fatal(err)
	}

	// registry.yaml：两个条目（skills: 顶层键）
	regYAML := "skills:\n" +
		"  pptx:\n    category: documents\n    source: local\n    installed_at: \"2026-08-13\"\n" +
		"  deploy:\n    category: devops\n    source: local\n    installed_at: \"2026-08-13\"\n"
	if err := os.WriteFile(filepath.Join(root, registryFile), []byte(regYAML), 0644); err != nil {
		t.Fatal(err)
	}

	s, err := NewManager(workDir, &Config{})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	list := s.List()
	if len(list) != 2 {
		t.Fatalf("want 2 skills (registry entries), got %d: %+v", len(list), list)
	}
	byName := map[string]*SkillMeta{}
	for _, sk := range list {
		byName[sk.Name] = sk
	}

	// 目录存在：description 从 SKILL.md 补全
	if byName["pptx"].Description != "Create presentations" {
		t.Errorf("pptx description not restored from disk: %+v", byName["pptx"])
	}
	if byName["pptx"].Category != "documents" {
		t.Errorf("pptx category not restored from registry: %+v", byName["pptx"])
	}
	// 目录缺失：条目保留但 description 为空
	if byName["deploy"].Description != "" {
		t.Errorf("deploy should have empty description (dir missing): %+v", byName["deploy"])
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
