package boot

import (
	"context"
	"sync"
	"time"

	"tars/internal/event"
	"tars/internal/session"
	"tars/pkg/tools"
)

// askRegistry 管理所有 in-flight 询问/审批的答复通道。
type askRegistry struct {
	mu      sync.Mutex
	pending map[string]chan *tools.Answer
}

func newAskRegistry() *askRegistry {
	return &askRegistry{pending: make(map[string]chan *tools.Answer)}
}

// wait 阻塞等待用户对 requestID 的答复（channel 容量 1，超时按 defaultValue 兜底）。
func (r *askRegistry) wait(ctx context.Context, requestID string, timeoutSec int, defaultValue string) (*tools.Answer, error) {
	ch := make(chan *tools.Answer, 1)
	r.mu.Lock()
	r.pending[requestID] = ch
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.pending, requestID)
		r.mu.Unlock()
	}()

	select {
	case ans := <-ch:
		return ans, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(time.Duration(timeoutSec) * time.Second):
		return &tools.Answer{Value: defaultValue, Source: "timeout_default"}, nil
	}
}

// resolve 路由用户答复到等待中的 goroutine（找不到或已答复返回 false）。
func (r *askRegistry) resolve(requestID string, ans *tools.Answer) bool {
	r.mu.Lock()
	ch, ok := r.pending[requestID]
	r.mu.Unlock()
	if !ok {
		return false
	}
	select {
	case ch <- ans:
	default: // 已有答复（重复点击兜底）
	}
	return true
}

// asker 实现 tools.Asker 与 tools.Approver：把工具的询问/审批请求
// 桥接到前端（事件 + 等待答复），工具包不认识会话与事件系统。
type asker struct {
	sess *session.Info
	asks *askRegistry
	sink event.Sink
}

func newAsker(sess *session.Info, asks *askRegistry, sink event.Sink) *asker {
	return &asker{sess: sess, asks: asks, sink: sink}
}

// Ask 处理 ask_user 询问：答复路由键即当前工具调用 ID（执行器注入 ctx）。
func (a *asker) Ask(ctx context.Context, q *tools.Question) (*tools.Answer, error) {
	return a.asks.wait(ctx, tools.ToolCallIDFromCtx(ctx), q.TimeoutSeconds, "")
}

// Approve 实现 tools.Approver：发 agent:approval 事件并阻塞等待用户答复。
// 常允许表命中与 "allow_always" 记忆由 tools.Gate 负责（上游调用方）。
func (a *asker) Approve(ctx context.Context, r *tools.ApprovalRequest) (*tools.Answer, error) {
	a.sink.Emit(event.Event{Kind: event.KindApproval, Approval: &event.ApprovalEvent{
		SessionID:      a.sess.ID,
		ToolCallID:     r.ToolCallID,
		ToolName:       r.ToolName,
		Summary:        r.Summary,
		Reason:         r.Reason,
		TimeoutSeconds: r.TimeoutSeconds,
	}})

	return a.asks.wait(ctx, r.ToolCallID, r.TimeoutSeconds, "deny")
}
