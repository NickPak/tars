package event

// Sink 是后端事件的输出端口。内核（runner/turn）只面向此接口发射事件，
// 前端（Wails service 层）实现它把事件适配到具体传输（Wails 事件系统）。
// 内核不 import Wails，事件如何到达前端由 Sink 实现决定。
type Sink interface {
	Chunk(StreamChunk)
	Reasoning(ReasoningEvent)
	Tool(ToolEvent)
	ToolResult(ToolResultEvent)
	Done(StreamDone)
	Error(StreamError)
	Approval(ApprovalEvent)
}

// nopSink 丢弃一切事件，用于测试 / headless 场景。
type nopSink struct{}

func (nopSink) Chunk(StreamChunk)          {}
func (nopSink) Reasoning(ReasoningEvent)   {}
func (nopSink) Tool(ToolEvent)             {}
func (nopSink) ToolResult(ToolResultEvent) {}
func (nopSink) Done(StreamDone)            {}
func (nopSink) Error(StreamError)          {}
func (nopSink) Approval(ApprovalEvent)     {}

// Discard 丢弃一切事件的 Sink。
var Discard Sink = nopSink{}
