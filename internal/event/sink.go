package event

// Sink 是后端事件的输出端口。内核（runner/turn）只面向此接口发射事件，
// 前端（Wails service 层）实现它把事件适配到具体传输（Wails 事件系统）。
// 内核不 import Wails，事件如何到达前端由 Sink 实现决定。
//
// Emit 不应无限阻塞——channel 型 sink 应有缓冲或活跃读者。
type Sink interface {
	Emit(e Event)
}

// FuncSink 把一个普通函数适配成 Sink。
type FuncSink func(Event)

// Emit 调用包装的函数。
func (f FuncSink) Emit(e Event) {
	if f != nil {
		f(e)
	}
}

// Discard 丢弃一切事件，用于测试 / headless 场景。
var Discard Sink = FuncSink(func(Event) {})
