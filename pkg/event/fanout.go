package event

import "sync"

// FanOut 把每个事件派发给所有已注册的 sink。
// 用于"一条事件流到达多个消费者"——如桌面 UI + jsonl 持久化 + 遥测。
type FanOut struct {
	mu    sync.Mutex
	sinks []Sink
}

// NewFanOut 返回一个空 FanOut。
func NewFanOut(sinks ...Sink) *FanOut {
	return &FanOut{sinks: sinks}
}

// Emit 把事件派发给所有已注册的 sink。
// 先快照订阅者列表再在锁外派发，避免派发期间 Add 造成死锁。
func (f *FanOut) Emit(e Event) {
	f.mu.Lock()
	sinks := append([]Sink(nil), f.sinks...)
	f.mu.Unlock()

	for _, s := range sinks {
		if s != nil {
			s.Emit(e)
		}
	}
}

// Add 追加一个订阅者。
func (f *FanOut) Add(s Sink) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sinks = append(f.sinks, s)
}

// Len 返回当前订阅者数量。
func (f *FanOut) Len() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sinks)
}
