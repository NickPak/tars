package llm

import (
	"tars/pkg/schema"

	eino "github.com/cloudwego/eino/schema"
)

// 本文件是领域消息（pkg/schema）与 eino 消息视图的互转适配。
// eino 类型只允许出现在 llm 包边界内，不向内核（agent/session/runner）渗漏。

// ToEinoMessage 转换为 Eino 消息视图（供模型调用）。纯函数、每次新建：
// LLM 上下文每轮开始时现派生，无缓存即无失效同步问题。
func ToEinoMessage(m *schema.Message) *eino.Message {
	sm := &eino.Message{
		Content:          m.Content,
		ReasoningContent: m.Reasoning,
		ToolCallID:       m.ToolCallID,
		ToolCalls:        ToEinoToolCalls(m.ToolCalls),
	}
	switch m.Role {
	case schema.RoleAssistant:
		sm.Role = eino.Assistant
	case schema.RoleSystem:
		sm.Role = eino.System
	case schema.RoleTool:
		sm.Role = eino.Tool
	default:
		sm.Role = eino.User
	}
	return sm
}

// FromEinoMessage 把 eino 消息（ReAct 循环产出）转换为领域消息视图。
// 仅承载内容字段（Role/Content/Reasoning/ToolCalls）；ID/CreatedAt 等
// 会话级字段由 session 管理，不在此转换。
func FromEinoMessage(m *eino.Message) *schema.Message {
	out := &schema.Message{
		Content:    m.Content,
		Reasoning:  m.ReasoningContent,
		ToolCallID: m.ToolCallID,
	}
	switch m.Role {
	case eino.Assistant:
		out.Role = schema.RoleAssistant
	case eino.System:
		out.Role = schema.RoleSystem
	case eino.Tool:
		out.Role = schema.RoleTool
	default:
		out.Role = schema.RoleUser
	}
	for _, tc := range m.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, schema.ToolCall{
			ID: tc.ID, Name: tc.Function.Name, Args: tc.Function.Arguments,
		})
	}
	return out
}

// ToEinoToolCalls 转换为 Eino 工具调用视图。
func ToEinoToolCalls(calls []schema.ToolCall) []eino.ToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]eino.ToolCall, len(calls))
	for i, tc := range calls {
		out[i] = eino.ToolCall{
			ID:   tc.ID,
			Type: "function",
			Function: eino.FunctionCall{
				Name:      tc.Name,
				Arguments: tc.Args,
			},
		}
	}
	return out
}
