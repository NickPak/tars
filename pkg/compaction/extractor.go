package compaction

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"tars/pkg/llm"
	"tars/pkg/schema"
)

// Extractor 把压缩集提炼为归档条目（02 篇 §6：LLM-as-Extractor）。
type Extractor interface {
	// Extract 输出 1..n 条新归档条目；Range/Pointer 留空由 Compactor 回填。
	// 失败即 error（调用方计一次压缩失败并走熔断）。
	Extract(ctx context.Context, provider llm.Provider, req *ExtractRequest) ([]*ArchiveEntry, error)
}

// ExtractRequest 是一次提取的完整输入（02 篇 §6）。
type ExtractRequest struct {
	Prefix  []*schema.Message // 已缓存的视图前缀（原始轨迹），直接复用以命中 KV Cache
	Batch   []*schema.Message // 本次压缩集（Prefix 的子切片）
	OldTail []*ArchiveEntry   // 旧条目尾部（增量衔接，最多 2 条）
	Range   string            // 轮序标签（turn_3-5）
	Pointer string            // 归档原文指针（模型逐字复制到每条新条目）
}

// LLMExtractor 是提取器的 LLM 实现（02 篇 §6）。
//
// 追加式构造（缓存技巧）：提取指令追加到已缓存前缀末尾，不另起对话——
// 前缀部分继续命中 KV Cache，只有指令与输出是新 prefill。
type LLMExtractor struct{}

func (LLMExtractor) Extract(ctx context.Context, provider llm.Provider, req *ExtractRequest) ([]*ArchiveEntry, error) {
	if provider == nil {
		return nil, fmt.Errorf("extractor: nil provider")
	}
	msgs := make([]*schema.Message, 0, len(req.Prefix)+1)
	msgs = append(msgs, req.Prefix...)
	msgs = append(msgs, &schema.Message{Role: schema.RoleUser, Content: buildExtractPrompt(req)})

	stream, err := provider.Stream(ctx, &llm.ChatRequest{Messages: msgs}) // 不带工具
	if err != nil {
		return nil, fmt.Errorf("extractor stream: %w", err)
	}
	defer stream.Close()
	for {
		if _, err := stream.Recv(); err == io.EOF {
			break
		} else if err != nil {
			return nil, fmt.Errorf("extractor recv: %w", err)
		}
	}
	full, err := stream.Final()
	if err != nil {
		return nil, fmt.Errorf("extractor final: %w", err)
	}
	if full == nil {
		return nil, fmt.Errorf("extractor: empty response")
	}
	return parseEntries(full.Content)
}

// buildExtractPrompt 构造提取指令（模型可见文本，统一英文）。
// 五条硬约束逐字写入，不允许实现者删减（02 篇 §6）。
func buildExtractPrompt(req *ExtractRequest) string {
	var b strings.Builder
	b.Grow(2048)
	b.WriteString("[CONTEXT COMPRESSION TASK]\n\n")
	b.WriteString("The conversation above has grown too long. Turns ")
	b.WriteString(req.Range)
	b.WriteString(" (all messages from the beginning up to the most recent retained turns) ")
	b.WriteString("must be replaced by structured archive entries.\n\n")

	b.WriteString("<previous_archive_tail>\n")
	if len(req.OldTail) == 0 {
		b.WriteString("(none — this is the first compression)\n")
	} else {
		data, _ := json.MarshalIndent(req.OldTail, "", "  ")
		b.Write(data)
		b.WriteByte('\n')
	}
	b.WriteString("</previous_archive_tail>\n\n")

	b.WriteString("Write 1..n new archive entries covering those turns, continuing the style and ")
	b.WriteString("granularity of the previous entries. Output a JSON array ONLY (no prose, no markdown ")
	b.WriteString("fences); each element:\n")
	b.WriteString(`{"range":"...","goal":"...","actions":["..."],"result":"...","artifacts":["..."],`)
	b.WriteString(`"identifiers":["..."],"pointer":"`)
	b.WriteString(req.Pointer)
	b.WriteString("\"}\n\n")

	b.WriteString("Hard requirements:\n")
	b.WriteString("1. NEVER drop: architectural decisions & key constraints; the list of modified files ")
	b.WriteString("and key changes; verification status (pass/fail); unresolved TODOs and rollback notes; ")
	b.WriteString("conclusions (tool outputs may be reduced to their conclusion only).\n")
	b.WriteString("2. Copy identifiers VERBATIM into \"identifiers\": UUIDs, hashes, IPs with ports, URLs, ")
	b.WriteString("file paths, PR numbers, commit hashes — a single wrong character breaks later tool calls.\n")
	b.WriteString("3. Keep failed paths: what was tried, which approach failed and why the plan changed — ")
	b.WriteString("this prevents repeating mistakes.\n")
	b.WriteString("4. Key-value style, no prose paragraphs; each entry <= 200 tokens.\n")
	b.WriteString("5. Set \"pointer\" of every entry to exactly: ")
	b.WriteString(req.Pointer)
	b.WriteString(" — it points to the full original text of these turns on disk.\n")
	return b.String()
}

// parseEntries 从模型输出解析条目数组：容忍 markdown 围栏与前后散文。
func parseEntries(content string) ([]*ArchiveEntry, error) {
	s := strings.TrimSpace(content)
	if i := strings.IndexByte(s, '['); i >= 0 {
		if j := strings.LastIndexByte(s, ']'); j > i {
			s = s[i : j+1]
		}
	}
	var entries []*ArchiveEntry
	if err := json.Unmarshal([]byte(s), &entries); err != nil {
		return nil, fmt.Errorf("extractor: parse entries: %w", err)
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("extractor: empty entry array")
	}
	return entries, nil
}

// StaticExtractor 是期 A 的占位实现：不调 LLM，产出一条机械统计条目，
// 供管线贯通与开发自测；生产装配使用 LLMExtractor。
type StaticExtractor struct{}

func (StaticExtractor) Extract(_ context.Context, _ llm.Provider, req *ExtractRequest) ([]*ArchiveEntry, error) {
	if req == nil || len(req.Batch) == 0 {
		return nil, fmt.Errorf("empty batch")
	}
	counts := map[schema.Role]int{}
	tools := map[string]int{}
	for _, m := range req.Batch {
		counts[m.Role]++
		for _, tc := range m.ToolCalls {
			tools[tc.Name]++
		}
	}
	actions := make([]string, 0, len(tools))
	for name, n := range tools {
		actions = append(actions, fmt.Sprintf("%s×%d", name, n))
	}
	sort.Strings(actions)
	return []*ArchiveEntry{{
		Goal:    "(static extractor placeholder)",
		Actions: actions,
		Result: fmt.Sprintf("archived %d messages (user=%d assistant=%d tool=%d)",
			len(req.Batch), counts[schema.RoleUser], counts[schema.RoleAssistant], counts[schema.RoleTool]),
	}}, nil
}
