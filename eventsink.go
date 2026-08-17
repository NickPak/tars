package main

import (
	"tars/internal/event"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// wailsSink 实现 event.Sink，把内核事件适配到 Wails 事件系统。
// 它是内核（runner/turn）与前端传输之间的唯一适配点；事件名与载荷类型
// 在此收敛，内核不 import Wails。
type wailsSink struct{}

var _ event.Sink = wailsSink{}

func (wailsSink) Chunk(e event.StreamChunk) {
	application.Get().Event.Emit("agent:chunk", e)
}

func (wailsSink) Reasoning(e event.ReasoningEvent) {
	application.Get().Event.Emit("agent:reasoning", e)
}

func (wailsSink) Tool(e event.ToolEvent) {
	application.Get().Event.Emit("agent:tool", e)
}

func (wailsSink) ToolResult(e event.ToolResultEvent) {
	application.Get().Event.Emit("agent:tool_result", e)
}

func (wailsSink) Done(e event.StreamDone) {
	application.Get().Event.Emit("agent:done", e)
}

func (wailsSink) Error(e event.StreamError) {
	application.Get().Event.Emit("agent:error", e)
}

func (wailsSink) Approval(e event.ApprovalEvent) {
	application.Get().Event.Emit("agent:approval", e)
}
