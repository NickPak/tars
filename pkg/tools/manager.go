package tools

import (
	"context"
	"encoding/json"
)

// Handler 是工具的执行体。args 是模型生成的 JSON 参数原文，
// 返回的字符串会作为 tool 消息的 content 回填给模型。
type Handler func(ctx context.Context, args json.RawMessage) (string, error)

// Definition 描述一个可注册的工具：对模型的申明 + 本地执行体。
type Definition struct {
	// Name 工具名，模型按此发起调用。a-z、A-Z、0-9、下划线、连字符，最长 64。
	Name string
	// Description 给模型看的说明，是模型选择何时/如何调用工具的唯一依据。
	Description string
	// Parameters 参数的 JSON Schema，例如：
	// 	map[string]any{
	// 		"type": "object",
	// 		"properties": map[string]any{"path": map[string]any{"type": "string"}},
	// 		"required": []string{"path"},
	// 	}
	Parameters map[string]any
	// Handler 本地执行体。
	Handler Handler
}

// ToolResult 记录一次工具调用的执行结果。
type ToolResult struct {
	ID     string // 对应的 tool_call_id
	Name   string // 工具名
	Args   string // 模型生成的原始 JSON 参数
	Output string // 执行结果文本；失败时为失败原因，永远不为空
	// Error 非空表示执行失败（Handler 返回 error / panic / 工具不存在）。
	// 成功时为 nil——判断成败用这个字段，不要靠 Output 的字符串前缀。
	Error error
}
