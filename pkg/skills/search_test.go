package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTokenize(t *testing.T) {
	cases := map[string][]string{
		"pptx":          {"pptx"},
		"deploy-app":    {"deploy-app"},
		"create slides": {"create", "slides"},
		// 中文：单字 + bigram
		"查询股价": {"查", "询", "股", "价", "查询", "询股", "股价"},
		"股票行情": {"股", "票", "行", "情", "股票", "票行", "行情"},
	}
	for in, want := range cases {
		got := tokenize(in)
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("tokenize(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestSearch_English(t *testing.T) {
	s := mustInit(t)
	addSkill(t, s, "pptx", "Create and edit PowerPoint presentations", "documents")
	addSkill(t, s, "deploy-app", "Deploy web applications to cloud", "devops")
	addSkill(t, s, "sql-query", "Run SQL queries against databases", "data")

	hits, err := s.Search("make a powerpoint presentation", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || hits[0].Name != "pptx" {
		t.Fatalf("expected pptx first, got %+v", hits)
	}
}

// 前缀命中：查询词是文档词的前缀时应命中（edge n-gram 索引）
func TestSearch_Prefix(t *testing.T) {
	s := mustInit(t)
	addSkill(t, s, "pptx", "Create and edit PowerPoint presentations", "documents")
	addSkill(t, s, "docx", "Create and edit Word documents", "documents")
	addSkill(t, s, "xlsx", "Create and edit Excel spreadsheets", "documents")
	addSkill(t, s, "pdf", "Read and extract PDF files", "documents")

	cases := map[string]string{
		"ppt":   "pptx",
		"doc":   "docx",
		"xls":   "xlsx",
		"power": "pptx", // powerpoint 的前缀
	}
	for q, want := range cases {
		hits, err := s.Search(q, 5)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, h := range hits {
			if h.Name == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Search(%q): %q not in results %+v", q, want, hits)
		}
	}

	// 不相关前缀仍应无结果
	hits, _ := s.Search("zzz", 5)
	if len(hits) != 0 {
		t.Errorf("Search(zzz) = %+v, want empty", hits)
	}
}

func TestSearch_Chinese(t *testing.T) {
	s := mustInit(t)
	addSkill(t, s, "stock-data", "查询股票行情与财经数据", "data")
	addSkill(t, s, "ppt-gen", "生成演示文稿幻灯片", "documents")

	hits, err := s.Search("我需要股价信息", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || hits[0].Name != "stock-data" {
		t.Fatalf("expected stock-data, got %+v", hits)
	}
}

func TestSearch_NoMatch(t *testing.T) {
	s := mustInit(t)
	addSkill(t, s, "pptx", "Create presentations", "documents")

	hits, err := s.Search("xyzzy quantum frobnicate", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("expected no match, got %+v", hits)
	}
}

func TestGenerateIndex_Tiers(t *testing.T) {
	s := mustInit(t)

	// 第一档：全量清单
	addSkill(t, s, "a-skill", "first", "")
	addSkill(t, s, "b-skill", "second", "")
	s.SetTiers(50, 500)
	if err := s.GenerateIndex(); err != nil {
		t.Fatal(err)
	}
	idx := s.RenderIndex()
	if !strings.Contains(idx, "## a-skill") || !strings.Contains(idx, "## b-skill") {
		t.Errorf("tier1 should list all skills inline:\n%s", idx)
	}
	if _, err := os.Stat(filepath.Join(s.RootDir(), "index")); !os.IsNotExist(err) {
		t.Error("tier1 should not create index/ dir")
	}

	// 第二档：类别目录 + index/<category>.md
	addSkill(t, s, "a-skill", "first", "documents")
	s.SetTiers(1, 500) // 1 个技能即超全量档
	if err := s.GenerateIndex(); err != nil {
		t.Fatal(err)
	}
	idx = s.RenderIndex()
	if strings.Contains(idx, "## a-skill") {
		t.Error("tier2 INDEX should not inline skill bodies")
	}
	if !strings.Contains(idx, "**documents**") {
		t.Errorf("tier2 INDEX should list category:\n%s", idx)
	}
	page, err := os.ReadFile(filepath.Join(s.RootDir(), "index", "documents.md"))
	if err != nil {
		t.Fatalf("tier2 should generate index/documents.md: %v", err)
	}
	if !strings.Contains(string(page), "## a-skill") {
		t.Errorf("category page should list skill:\n%s", page)
	}

	// 第三档：只留提示
	s.SetTiers(1, 1)
	if err := s.GenerateIndex(); err != nil {
		t.Fatal(err)
	}
	idx = s.RenderIndex()
	if !strings.Contains(idx, "discover_tools") {
		t.Errorf("tier3 should mention discover_tools:\n%s", idx)
	}
	if strings.Contains(idx, "## a-skill") || strings.Contains(idx, "**documents**") {
		t.Errorf("tier3 should not inline anything:\n%s", idx)
	}
}

func TestGenerateIndex_Empty(t *testing.T) {
	s := mustInit(t)
	if err := s.GenerateIndex(); err != nil {
		t.Fatal(err)
	}
	if s.RenderIndex() != "" {
		t.Error("empty skills should produce empty index (skip prefix injection)")
	}
}
