package memory

import (
	"testing"
)

// bleve 检索行为（冻结）：CJK bigram 中文子串召回、英文词召回、
// BM25 排序上限、空语料/空查询语义。

func searchCorpus() []*Fact {
	return []*Fact{
		{Type: FactUser, Subject: "prefer-dark-theme", Body: "偏好深色主题的编辑器配色"},
		{Type: FactFeedback, Subject: "use-pnpm", Body: "以后都用 pnpm 安装依赖"},
		{Type: FactProject, Subject: "gofmt-before-commit", Body: "提交前必须运行 gofmt"},
	}
}

func TestSearchFactsChineseBigram(t *testing.T) {
	// "深色" 是正文中间的两字子串：只有 bigram 切分能命中
	// （整句成词的 unicode 分词打不中）
	hits, err := SearchFacts(searchCorpus(), "深色", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Subject != "prefer-dark-theme" {
		t.Fatalf("bigram recall failed: %+v", hits)
	}
}

func TestSearchFactsEnglish(t *testing.T) {
	hits, err := SearchFacts(searchCorpus(), "gofmt", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Subject != "gofmt-before-commit" {
		t.Fatalf("english recall failed: %+v", hits)
	}
}

func TestSearchFactsLimitAndEmpty(t *testing.T) {
	// 无命中返回空集而非错误
	hits, err := SearchFacts(searchCorpus(), "不存在的关键词甲乙", 5)
	if err != nil || len(hits) != 0 {
		t.Fatalf("no-match should be empty: %+v, %v", hits, err)
	}
	// 空语料 / 空查询
	if hits, _ := SearchFacts(nil, "深色", 5); len(hits) != 0 {
		t.Error("empty corpus must yield empty")
	}
	if hits, _ := SearchFacts(searchCorpus(), "  ", 5); len(hits) != 0 {
		t.Error("blank query must yield empty")
	}
	// limit 生效
	dup := append(searchCorpus(), searchCorpus()...)
	hits, err = SearchFacts(dup, "pnpm gofmt 深色", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) > 2 {
		t.Errorf("limit not respected: %d hits", len(hits))
	}
}
