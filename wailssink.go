package main

import (
	"tars/internal/event"

	"github.com/baidubce/bce-sdk-go/util/log"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// WailsSink 实现 event.Sink，把内核事件适配到 Wails 事件系统。
// 它是内核（runner/turn）与前端传输之间的唯一适配点；事件名与载荷类型
// 在此收敛，内核不 import Wails。
//
// Wails 事件名与载荷结构是前端契约，保持不变——前端零感知。
type WailsSink struct{}

func NewWailsSink() *WailsSink {
	return &WailsSink{}
}

func (s *WailsSink) Emit(e event.Event) {
	app := application.Get()
	switch e.Kind {
	// 发射与注册类型一致：载荷指针直通（main.go 以指针类型注册）。
	case event.KindStreamChunk:
		app.Event.Emit("agent:chunk", e.Chunk)
	case event.KindReasoning:
		app.Event.Emit("agent:reasoning", e.Reasoning)
	case event.KindToolDispatch:
		app.Event.Emit("agent:tool", e.Tool)
	case event.KindToolResult:
		app.Event.Emit("agent:tool_result", e.ToolResult)
	case event.KindDone:
		app.Event.Emit("agent:done", e.Done)
	case event.KindError:
		app.Event.Emit("agent:error", e.Error)
	case event.KindApproval:
		app.Event.Emit("agent:approval", e.Approval)
	case event.KindMessageAppended, event.KindIterationStart,
		event.KindIterationEnd, event.KindTurnStarted:
		// 内核事件：不透出前端（trace 等其他订阅者经 FanOut 消费）。
	default:
		log.Errorf("unknown event kind: %d", e.Kind)
	}
}

var _ event.Sink = NewWailsSink()
