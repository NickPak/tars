package session

import (
	"encoding/json"
	"fmt"
	"strings"

	"tars/pkg/schema"
)

// RenderArchive 把压缩集原文渲染为 Markdown（02 篇统一原则：摘要 + 磁盘原文）。
// 纯函数——落盘由 CompactStore.WriteArchive 负责（目录创建在 StoreManager 自闭合）。
func RenderArchive(batch []*schema.Message) []byte {
	var b strings.Builder
	b.WriteString("# Archived turns\n\n")
	for _, m := range batch {
		fmt.Fprintf(&b, "## [%s] %s\n\n", m.ID, m.Role)
		if m.Reasoning != "" {
			b.WriteString("### Reasoning\n\n")
			b.WriteString(m.Reasoning)
			b.WriteString("\n\n")
		}
		if m.Content != "" {
			b.WriteString(m.Content)
			b.WriteString("\n\n")
		}
		for _, tc := range m.ToolCalls {
			fmt.Fprintf(&b, "### ToolCall %s (%s)\n\n```json\n%s\n```\n\n", tc.ID, tc.Name, tc.Args)
		}
		if m.ToolCallID != "" {
			fmt.Fprintf(&b, "> tool_call_id: %s\n\n", m.ToolCallID)
		}
	}
	return []byte(b.String())
}

// RangeLabel 计算压缩集在轨迹中的轮序标签（"turn_3-5"，展示用）。
// 轮序 = 轮起点序号（1 起，轮划分见 turn.go）。
func RangeLabel(raw, batch []*schema.Message) string {
	if len(batch) == 0 {
		return "turn_0-0"
	}
	firstIdx := indexOfID(raw, batch[0].ID)
	lastIdx := indexOfID(raw, batch[len(batch)-1].ID)
	start, end := 0, 0
	for ord, s := range TurnStarts(raw) {
		if s <= firstIdx {
			start = ord + 1
		}
		if s <= lastIdx {
			end = ord + 1
		}
	}
	if start == 0 {
		start = 1
	}
	if end == 0 {
		end = start
	}
	return fmt.Sprintf("turn_%d-%d", start, end)
}

// LoadSkillCalls 扫描压缩集中的 load_skill 调用，返回涉及的 skill 名
// （02 篇 §5.1 一致性红线：这些 skill 的正文随压缩离开上下文，
// 会话 loaded 记录必须同步清除）。
func LoadSkillCalls(batch []*schema.Message) []string {
	var names []string
	seen := map[string]struct{}{}
	for _, m := range batch {
		if m.Role != schema.RoleAssistant {
			continue
		}
		for _, tc := range m.ToolCalls {
			if tc.Name != LoadSkillTool {
				continue
			}
			var args struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal([]byte(tc.Args), &args); err != nil || args.Name == "" {
				continue
			}
			if _, ok := seen[args.Name]; !ok {
				seen[args.Name] = struct{}{}
				names = append(names, args.Name)
			}
		}
	}
	return names
}

// messagesBytes 统计消息的可压缩文本体量（01 篇 §5 PartExtraction 口径：
// Content + Reasoning + 工具调用参数）——经济性校验的"压缩前"一侧。
func messagesBytes(msgs []*schema.Message) int {
	n := 0
	for _, m := range msgs {
		n += len(m.Content) + len(m.Reasoning)
		for _, tc := range m.ToolCalls {
			n += len(tc.Args)
		}
	}
	return n
}

// lastPromptTokens 反扫轨迹中最后一条带实测用量的 assistant 消息
// （触发信号，01 篇 §6：跨轮有效——首轮迭代即可感知上一轮结束时的规模）。
func lastPromptTokens(raw []*schema.Message) int {
	for i := len(raw) - 1; i >= 0; i-- {
		if m := raw[i]; m.Role == schema.RoleAssistant && m.Usage != nil && m.Usage.PromptTokens > 0 {
			return m.Usage.PromptTokens
		}
	}
	return 0
}

// estimateProjectedTokens 估算压缩后视图规模（bytes/4，01 篇 §6 估算回退）。
// 仅用于计量（Stats.AfterTokens）与阶梯选档，不作触发判定——它不含
// system prompt、工具 schema 与状态栏（可达 5-15k token），系统性偏小。
func estimateProjectedTokens(entries []*ArchiveEntry, raw []*schema.Message, cutoffID string) int {
	n := 0
	if syn := (&CompactionData{Entries: entries}).Message(); syn != nil {
		n += len(syn.Content)
	}
	idx := indexOfID(raw, cutoffID)
	for _, m := range raw[idx+1:] {
		n += len(m.Content) + len(m.Reasoning)
		for _, tc := range m.ToolCalls {
			n += len(tc.Args)
		}
	}
	return n / 4
}
