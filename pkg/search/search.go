// Package search 是本地模糊检索引擎：BM25 排序 + CJK bigram 分词 +
// 拉丁词 edge n-gram 前缀索引。skills 与 mcp 的发现通道共用同一引擎，
// 保证"设置页搜索所见 = discover_tools 所得"在不同能力源间口径一致。
// 零依赖、纯函数、无状态。
package search

import (
	"math"
	"sort"
	"strings"
	"unicode"
)

const defaultLimit = 5

// Item 把检索文档（任意文本）与调用方载荷绑定。
type Item[T any] struct {
	Text    string // 参与索引的文本（name + description + 类别/服务器名等拼接）
	Payload T      // 命中后原样返回
}

// Search 对 items 做 BM25 检索，返回得分降序的前 limit 个载荷；无命中返回空。
// 引擎对载荷类型零感知（泛型），每次调用现场建索引——文档集小（数百量级），
// 无持久索引需求。
func Search[T any](items []Item[T], query string, limit int) []T {
	if limit <= 0 {
		limit = defaultLimit
	}

	type doc struct {
		item   Item[T]
		tokens []string
	}
	docs := make([]doc, 0, len(items))
	docLens := make([]int, 0, len(items))
	for _, it := range items {
		tokens := TokenizeForIndex(it.Text)
		if len(tokens) == 0 {
			continue
		}
		docs = append(docs, doc{item: it, tokens: tokens})
		docLens = append(docLens, len(tokens))
	}
	if len(docs) == 0 {
		return nil
	}

	// 文档频率
	df := map[string]int{}
	for _, d := range docs {
		for t := range unique(d.tokens) {
			df[t]++
		}
	}

	avgdl := 0.0
	for _, l := range docLens {
		avgdl += float64(l)
	}
	avgdl /= float64(len(docs))

	const k1, b = 1.5, 0.75
	N := float64(len(docs))
	queryTokens := Tokenize(query)

	type scored struct {
		item  Item[T]
		score float64
	}
	var results []scored
	for i, d := range docs {
		tf := map[string]int{}
		for _, t := range d.tokens {
			tf[t]++
		}
		var score float64
		for _, qt := range queryTokens {
			f := float64(tf[qt])
			if f == 0 {
				continue
			}
			idfv := idf(N, df[qt])
			dl := float64(docLens[i])
			score += idfv * (f * (k1 + 1)) / (f + k1*(1-b+b*dl/avgdl))
		}
		if score > 0 {
			results = append(results, scored{item: d.item, score: score})
		}
	}

	sort.Slice(results, func(i, j int) bool { return results[i].score > results[j].score })
	if len(results) > limit {
		results = results[:limit]
	}
	out := make([]T, len(results))
	for i, r := range results {
		out[i] = r.item.Payload
	}
	return out
}

// idf 采用 Lucene BM25 的非负形式：ln(1 + (N-df+0.5)/(df+0.5))。
// 常见词（df 接近 N）权重平滑趋近 0，而不是像 RSJ 原始形式那样变负——
// 前缀索引会制造大量遍布全库的 token（如 category 词 "documents" 的前缀
// "doc"），负 IDF 会把含这些词的文档整体惩罚到 score<=0 被过滤掉。
func idf(N float64, df int) float64 {
	return math.Log(1 + (N-float64(df)+0.5)/(float64(df)+0.5))
}

func unique(ts []string) map[string]struct{} {
	m := make(map[string]struct{}, len(ts))
	for _, t := range ts {
		m[t] = struct{}{}
	}
	return m
}

// TokenizeForIndex 在 Tokenize 基础上为每个拉丁词追加 edge n-gram 前缀
// （最小长度 2），使查询词可前缀命中文档词（如查询 "ppt" 命中技能名
// "pptx"）。查询侧不展开（经典非对称策略：索引侧展开、查询侧原样），
// 精确词命中与前缀命中在同一 BM25 框架内比较——前缀通常稀有、IDF 高，
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
