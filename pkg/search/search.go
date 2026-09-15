// Package search 是本地模糊检索引擎：bleve 索引与打分 + 自管预分词
// （CJK 单字/bigram + 拉丁词 edge n-gram 前缀）。skills 与 mcp 的发现
// 通道共用同一引擎，保证"设置页搜索所见 = discover_tools 所得"在不同
// 能力源间口径一致。
//
// 与 pkg/memory 的检索同款模式：索引是纯内存派生品，语料量级小（数百
// 条），每次调用现建现用，零磁盘产物、零失效问题。
//
// 为什么不直接用 bleve 内置分析器：cjk 分析器不提供拉丁词前缀命中
// （查 "ppt" 命中技能 "pptx"），而该特性依赖"索引侧展开、查询侧不展开"
// 的非对称策略。故分词仍由本包完成（Tokenize/TokenizeForIndex），
// 预分词结果以空格连接后交给 bleve（whitespace 分析器）建索引与打分——
// 排序质量由专业库负责，本包只声明"索引什么、查什么"。
package search

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/blevesearch/bleve/v2"
	_ "github.com/blevesearch/bleve/v2/analysis/analyzer/custom"    // 注册 "custom" 分析器构造器
	_ "github.com/blevesearch/bleve/v2/analysis/tokenizer/whitespace" // 注册 "whitespace" 分词器
	"github.com/blevesearch/bleve/v2/mapping"
)

const (
	defaultLimit = 5
	// analyzerName 自定义分析器：仅 whitespace 切分。token 已由本包
	// 预分词（含小写归一），bleve 侧只需原样建倒排。
	analyzerName = "tars_ws"
)

// Item 把检索文档（任意文本）与调用方载荷绑定。
type Item[T any] struct {
	Text    string // 参与索引的文本（name + description + 类别/服务器名等拼接）
	Payload T      // 命中后原样返回
}

// newIndexMapping 构造索引映射：单一 text 字段，whitespace 分析器。
func newIndexMapping() mapping.IndexMapping {
	field := bleve.NewTextFieldMapping()
	field.Analyzer = analyzerName

	doc := bleve.NewDocumentMapping()
	doc.AddFieldMappingsAt("text", field)

	m := bleve.NewIndexMapping()
	m.DefaultMapping = doc
	if err := m.AddCustomAnalyzer(analyzerName, map[string]any{
		"type":      "custom",
		"tokenizer": "whitespace",
	}); err != nil {
		// 内置组件声明，构造期即定，不会失败；失败按编程错误暴露。
		panic(fmt.Sprintf("search: register analyzer: %v", err))
	}
	return m
}

// Search 对 items 做全文检索，返回得分降序的前 limit 个载荷；无命中返回空。
// 引擎对载荷类型零感知（泛型）。
func Search[T any](items []Item[T], query string, limit int) []T {
	if limit <= 0 {
		limit = defaultLimit
	}

	queryTokens := Tokenize(query)
	if len(queryTokens) == 0 {
		return nil
	}

	idx, err := bleve.NewMemOnly(newIndexMapping())
	if err != nil {
		return nil
	}
	defer func() { _ = idx.Close() }()

	indexed := make([]T, 0, len(items))
	for _, it := range items {
		tokens := TokenizeForIndex(it.Text)
		if len(tokens) == 0 {
			continue
		}
		if err := idx.Index(strconv.Itoa(len(indexed)), map[string]string{
			"text": strings.Join(tokens, " "),
		}); err != nil {
			return nil
		}
		indexed = append(indexed, it.Payload)
	}
	if len(indexed) == 0 {
		return nil
	}

	// 查询侧不展开前缀（经典非对称策略）：查询 token 原样参与匹配，
	// 索引侧已展开的 edge n-gram 使前缀查询自然命中。
	q := bleve.NewMatchQuery(strings.Join(queryTokens, " "))
	q.FieldVal = "text"
	req := bleve.NewSearchRequestOptions(q, limit, 0, false)
	res, err := idx.Search(req)
	if err != nil {
		return nil
	}

	out := make([]T, 0, len(res.Hits))
	for _, hit := range res.Hits {
		i, err := strconv.Atoi(hit.ID)
		if err != nil || i < 0 || i >= len(indexed) {
			continue
		}
		out = append(out, indexed[i])
	}
	return out
}

// TokenizeForIndex 在 Tokenize 基础上为每个拉丁词追加 edge n-gram 前缀
// （最小长度 2），使查询词可前缀命中文档词（如查询 "ppt" 命中技能名
// "pptx"）。查询侧不展开（经典非对称策略：索引侧展开、查询侧原样），
// 精确词命中与前缀命中在同一排序框架内比较——前缀通常稀有、权重高，
// 前缀命中的文档自然靠前。CJK token 已有单字+bigram 覆盖，不再展开。
func TokenizeForIndex(s string) []string {
	toks := Tokenize(s)
	for _, t := range toks {
		rs := []rune(t)
		if len(rs) < 3 || !isASCIIWord(rs[0]) {
			continue // 长度 2 的词前缀即自身；CJK 单字/bigram 不展开
		}
		for i := 2; i < len(rs); i++ {
			toks = append(toks, string(rs[:i]))
		}
	}
	return toks
}

// Tokenize 把文本切分为检索 token：CJK 连续段拆为单字 + bigram（模糊覆盖），
// 拉丁词整词切分（小写归一）。
func Tokenize(s string) []string {
	runes := []rune(strings.ToLower(s))
	var out []string

	for i := 0; i < len(runes); {
		r := runes[i]
		switch {
		case isCJK(r):
			j := i
			for j < len(runes) && isCJK(runes[j]) {
				j++
			}
			// 单字
			for k := i; k < j; k++ {
				out = append(out, string(runes[k]))
			}
			// bigram
			for k := i; k+1 < j; k++ {
				out = append(out, string(runes[k:k+2]))
			}
			i = j
		case isASCIIWord(r):
			j := i
			for j < len(runes) && isASCIIWord(runes[j]) {
				j++
			}
			out = append(out, string(runes[i:j]))
			i = j
		default:
			i++
		}
	}
	return out
}

func isCJK(r rune) bool {
	return unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r)
}

func isASCIIWord(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-'
}
