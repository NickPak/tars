// Package event 定义后端通过 Wails 事件系统发往前端的事件载荷。
// 事件名以字符串散落在各发射点；main.go 的 init 统一 RegisterEvent
// 注册类型，绑定生成器据此产出强类型 TS API。前端另有一份手写的
// 镜像类型（frontend/src/types），两者需保持字段一致。
package event

import "tars/pkg/store"

// StreamChunk "agent:chunk"：assistant 正文内容的流式增量片段。
type StreamChunk struct {
	SessionID string `json:"sessionId"`
	MessageID string `json:"messageId"`
	Chunk     string `json:"chunk"`
}

// StreamDone "agent:done"：一轮正常结束（含用户取消的干净停止）。
type StreamDone struct {
	SessionID string           `json:"sessionId"`
	MessageID string           `json:"messageId"`
	Usage     *store.UsageInfo `json:"usage,omitempty"`
	ElapsedMs int64            `json:"elapsedMs"`
}

// StreamError "agent:error"：一轮以错误结束；kind 区分超时与普通错误。
type StreamError struct {
	SessionID string `json:"sessionId"`
	MessageID string `json:"messageId"`
	Error     string `json:"error"`
	Kind      string `json:"kind,omitempty"`
}

// ToolEvent "agent:tool"：一个工具调用开始执行。
type ToolEvent struct {
	SessionID  string `json:"sessionId"`
	MessageID  string `json:"messageId"`
	ToolCallID string `json:"toolCallId"`
	ToolName   string `json:"toolName"`
	Args       string `json:"args"`
}

// ToolResultEvent "agent:tool_result"：一个工具调用执行完成。
type ToolResultEvent struct {
	SessionID  string `json:"sessionId"`
	MessageID  string `json:"messageId"`
	ToolCallID string `json:"toolCallId"`
	Output     string `json:"output"`
}

// ReasoningEvent "agent:reasoning"：思考链内容的流式增量片段。
type ReasoningEvent struct {
	SessionID string `json:"sessionId"`
	MessageID string `json:"messageId"`
	Content   string `json:"content"`
}

// SessionRenamedEvent "session:renamed"：会话标题变更（手动或首条消息自动命名）。
type SessionRenamedEvent struct {
	SessionID string `json:"sessionId"`
	Title     string `json:"title"`
}
