package skills

import (
	"math"
	"sort"
	"strings"
	"unicode"
)

type searchDoc struct {
	skill  *SkillMeta
	tokens []string
}

func (s *Manager) Search(query string, limit int) ([]*SkillMeta, error) {
	list := s.List()
	if limit <= 0 {
		limit = 5
	}

	docs := make([]searchDoc, 0, len(list))
	docLens := make([]int, 0, len(list))
	for _, sk := range list {
		text := sk.Name + " " + sk.Description + " " + sk.Category
		tokens := tokenize(text)
		if len(tokens) == 0 {
			continue
		}
		docs = append(docs, searchDoc{skill: sk, tokens: tokens})
		docLens = append(docLens, len(tokens))
	}
	if len(docs) == 0 {
		return nil, nil
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

	queryTokens := tokenize(query)

	type scored struct {
		skill *SkillMeta
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
			idf := idf(N, df[qt])
			dl := float64(docLens[i])
			score += idf * (f * (k1 + 1)) / (f + k1*(1-b+b*dl/avgdl))
		}
		if score > 0 {
			results = append(results, scored{skill: d.skill, score: score})
		}
	}

	sort.Slice(results, func(i, j int) bool { return results[i].score > results[j].score })
	if len(results) > limit {
		results = results[:limit]
	}
	out := make([]*SkillMeta, len(results))
	for i, r := range results {
		out[i] = r.skill
	}
	return out, nil
}

func idf(N float64, df int) float64 {
	x := (N - float64(df) + 0.5) / (float64(df) + 0.5)
	if x <= 0 {
		return 0
	}
	return math.Log(x) + 1
}

func unique(ts []string) map[string]struct{} {
	m := make(map[string]struct{}, len(ts))
	for _, t := range ts {
		m[t] = struct{}{}
	}
	return m
}

func tokenize(s string) []string {
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
