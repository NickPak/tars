package event

import "sync"

const (
	DefaultBufferSize = 16
)

// Iterator 是事件流的拉取端（对齐 eino adk 的 AsyncIterator）：
// Agent.Run 立即返回，调用方 Next() 消费直到 ok=false（轮结束）。
// 契约只有这一个方法——新增事件种类只是 Kind 枚举加值，接口不变。
type Iterator struct {
	ch chan Event
}

// Next 取下一条事件；ok=false 表示流已关闭且缓冲耗尽（轮结束）。
func (it *Iterator) Next() (Event, bool) {
	e, ok := <-it.ch
	return e, ok
}

// Generator 是事件流的生产端。主生产者是 agent 循环 goroutine；
// 工具并发回调也可 Send（内部加锁，并发安全）。Close 后 Send 静默丢弃
// （晚到的并发回调兜底）。
type Generator struct {
	mu     sync.Mutex
	ch     chan Event
	closed bool
}

// Send 生产一条事件；流已关闭时丢弃。
func (g *Generator) Send(e Event) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return
	}
	g.ch <- e
}

// Close 关闭流（幂等）。Close 后 Next 耗尽缓冲即返回 ok=false。
func (g *Generator) Close() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.closed {
		g.closed = true
		close(g.ch)
	}
}

// NewIteratorPair 返回一对共享缓冲 channel 的 Iterator/Generator。
// buf 需覆盖生产突发（流式 chunk 高频）；消费方应紧凑 drain——
// 缓冲打满后 Send 会阻塞生产者（与此前同步 Sink.Emit 的背压语义一致）。
func NewIteratorPair(bufSize ...int) (*Iterator, *Generator) {
	size := DefaultBufferSize
	if len(bufSize) > 0 {
		size = bufSize[0]
	}
	ch := make(chan Event, size)
	return &Iterator{ch: ch}, &Generator{ch: ch}
}
