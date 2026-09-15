package memory

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/analysis/lang/cjk"
	"github.com/blevesearch/bleve/v2/mapping"
)

// search.go —— 统一检索基础设施（bleve）。
//
// 决策：全仓检索类功能统一用 bleve（记忆 recall 在本文件；工具/skill 检索
// 在 pkg/search，同一引擎）。不手写 BM25——打分、字段权重交给专业库，
// 业务侧只声明"索引什么、查什么"。
//
// 索引是纯内存派生品：语料量级小（记忆几十~几百条），每次检索现建现用，
// 零磁盘产物、零失效问题，md 文件仍是唯一权威（"索引派生可重建"原则）。

// newSearchMapping 构造记忆检索的索引映射：
// - 分析器用内置 CJK（bigram）：中文无空格，整句会被当做一个词元，子串级
//   召回必须靠 bigram 切分（"偏好深色主题" → 偏好/好深/深色/色主/主题）；
//   英文词不受影响（CJK 分词器对非 CJK 按空白/标点切词）
func newSearchMapping() mapping.IndexMapping {
	subj := bleve.NewTextFieldMapping()
	subj.Analyzer = cjk.AnalyzerName
	body := bleve.NewTextFieldMapping()
	body.Analyzer = cjk.AnalyzerName

	doc := bleve.NewDocumentMapping()
	doc.AddFieldMappingsAt("subject", subj)
	doc.AddFieldMappingsAt("body", body)

	m := bleve.NewIndexMapping()
	m.DefaultMapping = doc
	m.DefaultAnalyzer = cjk.AnalyzerName
	return m
}

// SearchFacts 对事实语料做全文检索（BM25 排序），返回前 limit 条。
// 空语料/空查询返回空集；limit<=0 默认 5。
func SearchFacts(facts []*Fact, query string, limit int) ([]*Fact, error) {
	if limit <= 0 {
		limit = 5
	}
	query = strings.TrimSpace(query)
	if len(facts) == 0 || query == "" {
		return nil, nil
	}

	// 内存索引（无磁盘产物；语料小，重建成本可忽略）
	idx, err := bleve.NewMemOnly(newSearchMapping())
	if err != nil {
		return nil, fmt.Errorf("memory: build search index: %w", err)
	}
	defer func() { _ = idx.Close() }()

	// 文档 ID 用序号而非 subject：subject 只保证单根目录内唯一，
	// 项目级+全局混合检索时可能撞名。
	for i, f := range facts {
		err := idx.Index(strconv.Itoa(i), map[string]string{
			"subject": f.Subject,
			"body":    f.Body,
		})
		if err != nil {
			return nil, fmt.Errorf("memory: index fact %q: %w", f.Subject, err)
		}
	}

	q := bleve.NewMatchQuery(query)
	req := bleve.NewSearchRequestOptions(q, limit, 0, false)
	res, err := idx.Search(req)
	if err != nil {
		return nil, fmt.Errorf("memory: search: %w", err)
	}

	out := make([]*Fact, 0, len(res.Hits))
	for _, hit := range res.Hits {
		i, err := strconv.Atoi(hit.ID)
		if err != nil || i < 0 || i >= len(facts) {
			continue
		}
		out = append(out, facts[i])
	}
	return out, nil
}
