package store

import "github.com/cloudwego/eino/schema"

type SessionMeta struct {
	Title         string `json:"title"`
	CreatedAt     int64  `json:"createdAt"`
	UpdatedAt     int64  `json:"updatedAt"`
	CustomWorkDir string `json:"customWorkDir,omitempty"`
}

type UsageInfo struct {
	PromptTokens     int    `json:"promptTokens"`
	CompletionTokens int    `json:"completionTokens"`
	TotalTokens      int    `json:"totalTokens"`
	CachedTokens     int    `json:"cachedTokens,omitempty"`
	ModelEntry       string `json:"modelEntry,omitempty"`
}

type ToolCall struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Args string `json:"args"`
}

type Message struct {
	ID         string          `json:"id"`
	Role       schema.RoleType `json:"role"`
	Content    string          `json:"content"`
	ToolCalls  []ToolCall      `json:"toolCalls,omitempty"`
	ToolCallID string          `json:"toolCallId,omitempty"`
	CreatedAt  int64           `json:"createdAt"`
	Reasoning  string          `json:"reasoning,omitempty"`
	Usage      *UsageInfo      `json:"usage,omitempty"`
	ElapsedMs  int64           `json:"elapsedMs,omitempty"`
}

// ToSchemaMessage 转换为 Eino schema 视图。纯函数、每次新建：
// LLM 上下文每轮开始时发现派生，无缓存即无失效同步问题。
func (m *Message) ToSchemaMessage() *schema.Message {
	sm := &schema.Message{
		Content:          m.Content,
		ReasoningContent: m.Reasoning,
		ToolCallID:       m.ToolCallID,
		ToolCalls:        toSchemaToolCalls(m.ToolCalls),
	}
	switch m.Role {
	case schema.Assistant:
		sm.Role = schema.Assistant
	case schema.System:
		sm.Role = schema.System
	case schema.Tool:
		sm.Role = schema.Tool
	default:
		sm.Role = schema.User
	}
	return sm
}

func toSchemaToolCalls(calls []ToolCall) []schema.ToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]schema.ToolCall, len(calls))
	for i, tc := range calls {
		out[i] = schema.ToolCall{
			ID:   tc.ID,
			Type: "function",
			Function: schema.FunctionCall{
				Name:      tc.Name,
				Arguments: tc.Args,
			},
		}
	}
	return out
}
