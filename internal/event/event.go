// Package event 定义后端通过 Wails 事件系统发往前端的事件载荷。
// 事件名以字符串散落在各发射点；main.go 的 init 统一 RegisterEvent
// 注册类型，绑定生成器据此产出强类型 TS API。前端另有一份手写的
// 镜像类型（frontend/src/types），两者需保持字段一致。
//
// 统一事件流模型（对齐 plan/react 设计）：
//   - 内核只依赖单方法 Sink.Emit(Event)，按 Kind 读取对应载荷字段；
//   - 事件流是纯通知——Emit 无返回值，不参与控制流；
//   - 需要"一对多"（UI + 日志 + 遥测）时用 FanOut 组合多个 Sink。
package event

import "tars/pkg/schema"

// Kind 标识事件的类型。
type Kind int

const (
	// KindStreamChunk assistant 正文内容的流式增量（Chunk 字段有效）。
	KindStreamChunk Kind = iota
	// KindReasoning 思考链内容的流式增量（Reasoning 字段有效）。
	KindReasoning
	// KindToolDispatch 一个工具调用开始执行（Tool 字段有效）。
	KindToolDispatch
	// KindToolResult 一个工具调用执行完成（ToolResult 字段有效）。
	KindToolResult
	// KindDone 一轮正常结束，含用户取消的干净停止（Done 字段有效）。
	KindDone
	// KindError 一轮以错误结束（Error 字段有效）。
	KindError
	// KindApproval 危险工具调用的安全审批请求（Approval 字段有效）。
	KindApproval
	// KindMessageAppended 一条消息被追加（或聚合更新）到会话（Message 字段有效）。
	// 纯内核通知：供持久化/遥测等订阅者使用；不透出到 Wails 前端。
	KindMessageAppended
	// KindIterationStart 一次 ReAct 迭代（LLM 调用）开始（Iteration 字段有效）。
	// 纯内核通知（trace 订阅）；不透出到 Wails 前端。
	KindIterationStart
	// KindIterationEnd 一次 ReAct 迭代完成——LLM 调用加该轮工具执行
	// （Iteration 字段有效）。纯内核通知（trace 订阅）；不透出前端。
	KindIterationEnd
	// KindTurnStarted 一轮对话开始（Turn 字段有效）。纯内核通知：
	// 轮级元信息（模型/系统提示词/工具描述）经此传给 trace 等订阅者；
	// 不透出到 Wails 前端。
	KindTurnStarted
)

// Event 是后端事件流中的一个增量。按 Kind 读取对应的非 nil 载荷字段，
// 其余字段为 nil。载荷用指针：避免大结构拷贝，且非目标字段不占空间。
type Event struct {
	Kind       Kind
	Chunk      *StreamChunk          // KindStreamChunk
	Reasoning  *ReasoningEvent       // KindReasoning
	Tool       *ToolEvent            // KindToolDispatch
	ToolResult *ToolResultEvent      // KindToolResult
	Done       *StreamDone           // KindDone
	Error      *StreamError          // KindError
	Approval   *ApprovalEvent        // KindApproval
	Message    *MessageAppendedEvent // KindMessageAppended
	Iteration  *IterationEvent       // KindIterationStart / KindIterationEnd
	Turn       *TurnEvent            // KindTurnStarted
}

// TurnEvent 一轮对话的开始（内核事件，不透出前端）。trace 等订阅者据此
// 建立轮级状态（turn span），无需 Controller 感知它们的存在。
type TurnEvent struct {
	SessionID string
	MessageID string
	UserText  string
	// ModelID 本轮激活模型的真实名称（trace 展示用）。
	ModelID string
	// System 静态系统提示词全文（trace 展示用）。
	System string
	// ToolSchemas 本轮工具描述的 JSON 序列（trace 展示用）。
	ToolSchemas []string
}

// IterationEvent 一次 ReAct 迭代的边界（内核事件，不透出前端）。
type IterationEvent struct {
	SessionID string
	MessageID string
	Iteration int
	// Messages 是 KindIterationStart 的完整输入（system + 历史 + 状态栏）。
	Messages []*schema.Message
	// Assistant 是 KindIterationEnd 的本轮 assistant 完整消息（含 Usage）。
	Assistant *schema.Message
}

// MessageAppendedEvent 一条消息被追加（或聚合更新快照）到会话。
// 纯内核通知，不透出前端；供持久化/遥测等订阅者使用。
type MessageAppendedEvent struct {
	SessionID string          `json:"sessionId"`
	Message   *schema.Message `json:"message"`
}

// StreamChunk "agent:chunk"：assistant 正文内容的流式增量片段。
type StreamChunk struct {
	SessionID string `json:"sessionId"`
	MessageID string `json:"messageId"`
	Chunk     string `json:"chunk"`
}

// StreamDone "agent:done"：一轮正常结束（含用户取消的干净停止）。
type StreamDone struct {
	SessionID string            `json:"sessionId"`
	MessageID string            `json:"messageId"`
	Usage     *schema.UsageInfo `json:"usage,omitempty"`
	ElapsedMs int64             `json:"elapsedMs"`
	// FinalOutput 本轮最终回复全文。内核字段：trace 订阅者用它做
	// turn span 的 output.value；不透出前端（json:"-"）。
	FinalOutput string `json:"-"`
}

// StreamError "agent:error"：一轮以错误结束；kind 区分超时与普通错误。
type StreamError struct {
	SessionID string `json:"sessionId"`
	MessageID string `json:"messageId"`
	Error     string `json:"error"`
	Kind      string `json:"kind,omitempty"`
	// ElapsedMs 本轮总耗时。内核字段：trace 订阅者用；不透出前端（json:"-"）。
	ElapsedMs int64 `json:"-"`
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

// ApprovalEvent "agent:approval"：执行层拦截危险工具调用后发起的安全审批请求。
// 答复键即 ToolCallID；答复经 AgentService.AnswerAskUser 回写，
// 终态（允许/拒绝/超时默认拒绝）随 agent:tool_result 到达。
type ApprovalEvent struct {
	SessionID      string `json:"sessionId"`
	ToolCallID     string `json:"toolCallId"`
	ToolName       string `json:"toolName"`
	Summary        string `json:"summary"` // 待批准的危险内容（如完整命令）
	Reason         string `json:"reason"`  // 命中的风险规则说明
	TimeoutSeconds int    `json:"timeoutSeconds"`
}
