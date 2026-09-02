package session

import (
	"strings"

	"tars/pkg/schema"
)

// SyntheticMessageID 合成归档消息的固定 ID（视图投影用，不持久化）。
const SyntheticMessageID = "compaction"

// LoadSkillTool 是 load_skill 工具名（镜像 pkg/tool/toolkit 的常量，
// 避免跨层依赖）。02 篇 §5.1 一致性红线：压缩集含其调用时同步清除
// 会话 loaded 记录。
const LoadSkillTool = "load_skill"

// ArchiveScheme 是归档原文的虚拟路径前缀（镜像 pkg/tool/toolkit 的常量，
// 避免跨层依赖——同 LoadSkillTool 的处理）。条目 Pointer 直接取
// "archive://<file>.md" 形态，模型把它原样填进 read_file 的 path 即可，
// 无需任何转换。
const ArchiveScheme = "archive://"

// CompactionData 是会话的压缩态：视图投影的规则参数。由 session 独立持久化
// （compaction.json，03 篇）。nil 或 CutoffMessageID 为空 = 恒等投影。
type CompactionData struct {
	// Entries 归档条目区（按时间升序，append-only），渲染进视图时整体拼接。
	Entries []*ArchiveEntry `json:"entries"`
	// CutoffMessageID 压缩边界：该 ID 及其之前的轨迹消息已被条目替代。
	CutoffMessageID string `json:"cutoffMessageId"`
	// TimesCompressed 累计压缩次数（观测用，06 篇）。
	TimesCompressed int `json:"timesCompressed,omitempty"`
	// Stats 最近一次压缩的计量（观测用，06 篇）。
	Stats *CompactionStats `json:"stats,omitempty"`
}

// ArchiveEntry 一条归档条目：键值对，不写散文（master plan §5.2 形态规定）。
//
// 双形态（刻意解耦）：落盘用 JSON（程序可追加、可校验、可驱动 UI），进上下文
// 用紧凑文本（renderEntries）。JSON 的引号/括号/缩进对模型没有信息价值，却占
// 条目 20%+ 的体量——这两个字段除渲染外没有任何代码消费者，付语法税无回报。
//
// 除 Range/Pointer 外全部 omitempty：既避免空字段渲染成 null 白占 token，
// 也让 Stub() 退化后的条目自然只剩三项。
type ArchiveEntry struct {
	Range string `json:"range"` // "turn_3-5"（展示用标签）
	Goal  string `json:"goal"`  // 该阶段目标（转述用户意图）
	// UserAsks 用户原话，逐字。用户意图是唯一无法从工作区重建的信息——文件能
	// 重读、报错能复现，但"用户当时要什么、优先级如何"一旦转述就永久漂移，
	// 且后续每次压缩都在前一次的转述上再转述。Claude Code 的摘要同样单列
	// "All user messages" 原话照录。
	UserAsks []string `json:"userAsks,omitempty"`
	Actions  []string `json:"actions,omitempty"` // 关键动作（工具+对象，一行一条）
	Result   string   `json:"result,omitempty"`  // 成功/失败(原因)；失败路径必须保留
	// Facts 必须逐字保留的事实：文件路径、URL、hash、IP:端口、ID、commit。
	// 原 artifacts + identifiers 合并——提示词把 file paths 同时列进两者，
	// 定义相交，模型必然重复填同样内容。
	Facts   []string `json:"facts,omitempty"`
	Pointer string   `json:"pointer"` // 原文指针：archive/turn_3-5.md
}

// IsStub 报告条目是否已退化为存根（只剩 range/goal/pointer）。
func (e *ArchiveEntry) IsStub() bool {
	return len(e.UserAsks) == 0 && len(e.Actions) == 0 && e.Result == "" && len(e.Facts) == 0
}

// Stub 把条目退化为存根：丢细节，保留"有这么一段、目标是什么、原文在哪"。
//
// 为什么不直接删除条目：原文并不随之消失（archive/*.md 与 messages.jsonl 都
// 永久保留），删条目销毁的不是信息而是**索引**——模型从此不知道该归档文件
// 存在，pointer 这条逃生通道就废了。存根约 20 token，比完整条目省九成，
// 可发现性完整保住。
func (e *ArchiveEntry) Stub() {
	e.UserAsks, e.Actions, e.Facts, e.Result = nil, nil, nil, ""
}

// CompactionStats 是一次压缩的计量。
type CompactionStats struct {
	BeforeTokens int    `json:"beforeTokens"`
	AfterTokens  int    `json:"afterTokens"`
	DurationMs   int64  `json:"durationMs"`
	ExtractModel string `json:"extractModel,omitempty"`
	At           int64  `json:"at"`
}

// archiveHeader 是归档区的开头指引。三件事必须都说到，缺一条通道就失效：
//
//	存在性——不说"这里是被压掉的历史"，模型只当是普通背景资料，
//	         不会想到还有原文可捞；
//	读法  ——不给出确切调用形态，模型会拿指针去猜普通路径（archive://
//	         不是合法相对路径，猜必失败）；
//	节制  ——不说"仅当摘要不足时"，模型会把所有指针都读一遍，
//	         压缩省下的上下文被读回成本原样吃掉。
//
// 指引与数据相邻（而非塞进标签属性）：属性里写长文本要转义引号，可读性差，
// 且模型对"块内首段说明"的遵从度高于对属性值的遵从度。
const archiveHeader = `<context_archive>
Earlier turns of this conversation were compacted to save context. Every entry below
begins with "### <turns> → <pointer>"; that pointer resolves to the FULL original text
of those turns and is read with read_file(path="archive://<name>.md").
Read a pointer ONLY when an entry's summary is genuinely insufficient for what you are
doing now — re-reading spends back the context that compaction just reclaimed.
`

// renderEntries 把条目区渲染为进上下文的紧凑文本。视图消息与经济性校验共用
// 此函数，保证"压缩后"一侧只有一个口径。
// 冻结：同输入必输出同字节（KV Cache 友好）。
func renderEntries(entries []*ArchiveEntry) string {
	var b strings.Builder
	b.Grow(len(entries) * 256)
	for _, e := range entries {
		if e == nil {
			continue
		}
		b.WriteString("### ")
		b.WriteString(e.Range)
		if e.Pointer != "" {
			b.WriteString(" → ")
			b.WriteString(e.Pointer)
		}
		b.WriteByte('\n')
		if e.Goal != "" {
			b.WriteString("goal: ")
			b.WriteString(e.Goal)
			b.WriteByte('\n')
		}
		for _, u := range e.UserAsks {
			b.WriteString("user: ")
			b.WriteString(u)
			b.WriteByte('\n')
		}
		if len(e.Actions) > 0 {
			b.WriteString("did:\n")
			for _, a := range e.Actions {
				b.WriteString("- ")
				b.WriteString(a)
				b.WriteByte('\n')
			}
		}
		if e.Result != "" {
			b.WriteString("result: ")
			b.WriteString(e.Result)
			b.WriteByte('\n')
		}
		if len(e.Facts) > 0 {
			b.WriteString("keep: ")
			b.WriteString(strings.Join(e.Facts, ", "))
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// Message 把归档条目区渲染为合成视图消息（01 篇投影）。
// nil/空条目返回 nil。role=user：这是"提供给模型的背景资料"，不是模型自己
// 说过的话——用 assistant 角色会让模型误认为是自己的输出。
func (c *CompactionData) Message() *schema.Message {
	if c == nil || len(c.Entries) == 0 {
		return nil
	}
	body := renderEntries(c.Entries)
	if body == "" {
		return nil
	}
	var b strings.Builder
	b.Grow(len(body) + len(archiveHeader) + 24)
	b.WriteString(archiveHeader) // 末尾自带换行
	b.WriteString(body)          // 末尾自带换行
	b.WriteString("</context_archive>")
	return &schema.Message{ID: SyntheticMessageID, Role: schema.RoleUser, Content: b.String()}
}
