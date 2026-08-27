package compaction

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"

	"tars/pkg/schema"
)

// 标识符模式（02 篇 §6 红线 2 的代码校验器）。
// 宁可漏识别（校验放松）不可误识别（误报会让压缩连续失败直至熔断）：
// - 斜杠路径要求 ≥2 段或带扩展名（排除 "and/or" 这类散文误命中）；
// - 十六进制 hash 要求至少含一个数字（排除纯字母单词误命中，码内过滤）；
// - URL 剥离尾部标点（句点/逗号/分号/冒号）。
var identifierPatterns = []*regexp.Regexp{
	regexp.MustCompile(`https?://[^\s)<>"']+`),                                    // URL
	regexp.MustCompile(`\b[0-9a-fA-F]{8}(?:-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12}\b`), // UUID
	regexp.MustCompile(`\b\d{1,3}(?:\.\d{1,3}){3}(?::\d+)?\b`),                    // IP[:port]
	regexp.MustCompile(`\b(?:[\w.~-]+/)+[\w.~-]*\.\w+\b`),                         // 带扩展名的路径
	regexp.MustCompile(`\b[\w.~-]*(?:/[\w.~-]+){2,}\b`),                           // ≥2 段的斜杠路径
	regexp.MustCompile(`\b[A-Za-z]:\\[^\s"'<>|]+`),                                // Windows 路径
	regexp.MustCompile(`\b[0-9a-f]{7,40}\b`),                                      // 十六进制 hash（码内过滤）
}

var hexPattern = identifierPatterns[len(identifierPatterns)-1]

// extractIdentifiers 从文本中提取标识符集合。
func extractIdentifiers(text string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, re := range identifierPatterns {
		for _, m := range re.FindAllString(text, -1) {
			m = strings.TrimRight(m, ".,;:")
			if m == "" {
				continue
			}
			if re == hexPattern && !strings.ContainsAny(m, "0123456789") {
				continue // 无数字的纯 a-f 串按英文单词处理（宁可漏判，不可误判）
			}
			out[m] = struct{}{}
		}
	}
	return out
}

// MissingIdentifiers 返回压缩集中出现、但未在新条目与保留区文本中出现的
// 标识符（02 篇 §6 红线 2：标识符改错一位，后续工具调用直接失效）。
// 返回 nil 表示校验通过。
func MissingIdentifiers(batch []*schema.Message, entries []*ArchiveEntry, retained []*schema.Message) []string {
	batchIDs := extractIdentifiers(messagesText(batch))
	if len(batchIDs) == 0 {
		return nil
	}
	allowed := extractIdentifiers(messagesText(retained) + entriesText(entries))
	var missing []string
	for id := range batchIDs {
		if _, ok := allowed[id]; !ok {
			missing = append(missing, id)
		}
	}
	sort.Strings(missing)
	return missing
}

// messagesText 提取消息的可压缩文本（01 篇 §5 PartExtraction）。
func messagesText(msgs []*schema.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		b.WriteString(m.Content)
		b.WriteByte('\n')
		b.WriteString(m.Reasoning)
		b.WriteByte('\n')
		for _, tc := range m.ToolCalls {
			b.WriteString(tc.Args)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// entriesText 把条目区序列化为文本（与视图渲染同字节，校验口径一致）。
func entriesText(entries []*ArchiveEntry) string {
	data, _ := json.MarshalIndent(entries, "", "  ")
	return string(data)
}
