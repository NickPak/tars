// Package schema 定义 Agent 会话中的核心消息类型（全项目的领域词汇表）。
// 零依赖：不 import eino 等任何第三方包。
// 与 eino 消息视图的互转在 pkg/llm（模型调用的适配边界）。
package schema

// Role 表示消息的角色。
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// ToolCall 是 assistant 消息里的一个工具调用（存储视图：函数名与参数原文拍平）。
type ToolCall struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Args string `json:"args"`
}

// ToolSchema 是发给模型的工具描述（JSON Schema）。
// 定义在此而非 tools 包，因为它是模型请求的一部分（llm 包需要引用它），
// 属于领域类型而非可执行工具的附属。
type ToolSchema struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// UsageInfo 是一次助手回复的 token 用量。
type UsageInfo struct {
	PromptTokens     int `json:"promptTokens"`
	CompletionTokens int `json:"completionTokens"`
	TotalTokens      int `json:"totalTokens"`
	CachedTokens     int `json:"cachedTokens,omitempty"`
	// EntryID 是产生该用量的本地配置条目 ID（llm.ModelConfig.EntryID，
	// 如 "gemini/gemini-3.1-flash-lite"），用于按条目价格表核算费用；
	// 不是发给 API 的真实模型名（ModelConfig.ModelId）——同一真实模型
	// 可配多个条目、价格不同，故费用按条目核算。
	EntryID string `json:"entryId,omitempty"`
}

// MessagePart 是 assistant 消息在一个 ReAct 迭代内的产出：本轮思考 + 文本 + 工具调用。
// 前端据此把各迭代的思考/文本/工具卡片按时间顺序交错渲染；
// Content/Reasoning/ToolCalls 聚合字段（全迭代拼接）继续供 LLM 上下文、
// 导出、统计与旧数据回退渲染使用。
type MessagePart struct {
	Content   string     `json:"content,omitempty"`
	Reasoning string     `json:"reasoning,omitempty"`
	ToolCalls []ToolCall `json:"toolCalls,omitempty"`
}

// Message 是会话中的一条消息（内存态与持久化共用同一结构）。
type Message struct {
	ID         string     `json:"id"`
	Role       Role       `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"toolCalls,omitempty"`
	ToolCallID string     `json:"toolCallId,omitempty"`
	CreatedAt  int64      `json:"createdAt"`
	Reasoning  string     `json:"reasoning,omitempty"`
	Usage      *UsageInfo `json:"usage,omitempty"`
	ElapsedMs  int64      `json:"elapsedMs,omitempty"`
	// Parts 按 ReAct 迭代记录 assistant 消息的产出顺序（仅 assistant 角色）。
	Parts []MessagePart `json:"parts,omitempty"`
}
