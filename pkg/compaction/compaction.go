// Package compaction 实现上下文压缩管线（plan/context 02 篇）：
// 触发判定、归档条目提取、压缩态写回与熔断。压缩改写的是"投影规则"，
// 原始轨迹（messages.jsonl）一个字节不动（01 篇不变量 1）。
package compaction

import (
	"encoding/json"
	"strings"

	"tars/pkg/schema"
)

// SyntheticMessageID 合成归档消息的固定 ID（视图投影用，不持久化）。
const SyntheticMessageID = "compaction"

// LoadSkillTool 是 load_skill 工具名（镜像 pkg/tool/toolkit 的常量，
// 避免跨层依赖）。02 篇 §5.1 一致性红线：压缩集含其调用时同步清除
// 会话 loaded 记录。
const LoadSkillTool = "load_skill"

// Compaction 是会话的压缩态：视图投影的规则参数。由 session 独立持久化
// （compaction.json，03 篇）。nil 或 CutoffMessageID 为空 = 恒等投影。
type Compaction struct {
	// Entries 归档条目区（按时间升序，append-only），渲染进视图时整体拼接。
	Entries []*ArchiveEntry `json:"entries"`
	// CutoffMessageID 压缩边界：该 ID 及其之前的轨迹消息已被条目替代。
	CutoffMessageID string `json:"cutoffMessageId"`
	// Stats 最近一次压缩的计量（观测用，06 篇）。
	Stats *CompactionStats `json:"stats,omitempty"`
}

// ArchiveEntry 一条归档条目：键值对，不写散文（master plan §5.2 形态规定）。
type ArchiveEntry struct {
	Range       string   `json:"range"`       // "turn 3-5"（展示用标签）
	Goal        string   `json:"goal"`        // 该阶段目标（引用用户消息意图）
	Actions     []string `json:"actions"`     // 关键动作（工具+对象，一行一条）
	Result      string   `json:"result"`      // 成功/失败(原因)；失败路径必须保留
	Artifacts   []string `json:"artifacts"`   // 产物：文件路径等
	Identifiers []string `json:"identifiers"` // 标识符原样：URL/hash/IP/端口/ID/commit
	Pointer     string   `json:"pointer"`     // 原文指针：archive/turn_3-5.md
}

// CompactionStats 是一次压缩的计量。
type CompactionStats struct {
	BeforeTokens int    `json:"beforeTokens"`
	AfterTokens  int    `json:"afterTokens"`
	DurationMs   int64  `json:"durationMs"`
	ExtractModel string `json:"extractModel,omitempty"`
	At           int64  `json:"at"`
}

// Message 把归档条目区渲染为合成视图消息（01 篇投影）。
// 冻结：同输入必输出同字节（KV Cache 友好）；nil/空条目返回 nil。
func (c *Compaction) Message() *schema.Message {
	if c == nil || len(c.Entries) == 0 {
		return nil
	}
	data, err := json.MarshalIndent(c.Entries, "", "  ")
	if err != nil {
		return nil
	}
	var b strings.Builder
	b.Grow(len(data) + 160)
	b.WriteString(`<context_archive note="Earlier turns were archived to save context; read a pointer file only when its summary is insufficient.">`)
	b.WriteByte('\n')
	b.Write(data)
	b.WriteString("\n</context_archive>")
	return &schema.Message{ID: SyntheticMessageID, Role: schema.RoleUser, Content: b.String()}
}
