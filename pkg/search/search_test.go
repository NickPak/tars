package search

import (
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
		got := Tokenize(in)
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("Tokenize(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestTokenizeForIndex_Prefix(t *testing.T) {
	got := TokenizeForIndex("pptx")
	// 原词 + 前缀 pp/ppt
	joined := strings.Join(got, "|")
	for _, want := range []string{"pptx", "pp", "ppt"} {
		if !strings.Contains(joined, want) {
			t.Errorf("TokenizeForIndex(pptx) missing %q: %v", want, got)
		}
	}
	// CJK token 不展开
	got = TokenizeForIndex("股票")
	joined = strings.Join(got, "|")
	if strings.Contains(joined, "股|股|") || len(got) != 3 { // 股 票 股票
		t.Errorf("CJK tokens should not get prefixes: %v", got)
	}
}

func TestSearch_Generic(t *testing.T) {
	items := []Item[string]{
		{Text: "pptx Create and edit PowerPoint presentations documents", Payload: "pptx"},
		{Text: "deploy-app Deploy web applications to cloud devops", Payload: "deploy-app"},
		{Text: "sql-query Run SQL queries against databases data", Payload: "sql-query"},
	}
	got := Search(items, "make a powerpoint presentation", 5)
	if len(got) == 0 || got[0] != "pptx" {
		t.Fatalf("expected pptx first, got %v", got)
	}
	// 前缀命中
	got = Search(items, "ppt", 5)
	if len(got) == 0 || got[0] != "pptx" {
		t.Fatalf("prefix ppt should hit pptx, got %v", got)
	}
	// 无命中
	if got := Search(items, "zzz", 5); len(got) != 0 {
		t.Fatalf("zzz should match nothing, got %v", got)
	}
}
