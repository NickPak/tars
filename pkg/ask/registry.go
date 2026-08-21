package ask

import (
	"context"
	"sync"
	"tars/internal/event"
	"time"
)

// Registry 管理所有 in-flight 询问/审批的答复通道。
type Registry struct {
	mu      sync.Mutex
	pending map[string]chan *Answer
}

func NewRegistry() *Registry {
	return &Registry{pending: make(map[string]chan *Answer)}
}

// Wait 阻塞等待用户对 requestID 的答复（channel 容量 1，超时按 defaultValue 兜底）。
func (r *Registry) Wait(ctx context.Context, requestID string, timeoutSec int, defaultValue string) (*Answer, error) {
	ch := make(chan *Answer, 1)
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
		return &Answer{Value: defaultValue, Source: "timeout_default"}, nil
	}
}

// Resolve 路由用户答复到等待中的 goroutine（找不到或已答复返回 false）。
func (r *Registry) Resolve(requestID string, ans *Answer) bool {
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

// Ask 处理 ask_user 询问：答复路由键即当前工具调用 ID（执行器注入 ctx）。
func (r *Registry) Ask(ctx context.Context, requestID string, q *Question) (*Answer, error) {
	return r.Wait(ctx, requestID, q.TimeoutSeconds, "")
}

// Approve 实现 tools.Approver：发 agent:approval 事件并阻塞等待用户答复。
// 常允许表命中与 "allow_always" 记忆由 tools.Gate 负责（上游调用方）。
func (r *Registry) Approve(ctx context.Context, sink event.Sink, sessionID string, ar *ApprovalRequest) (*Answer, error) {
	sink.Emit(event.Event{Kind: event.KindApproval, Approval: &event.ApprovalEvent{
		SessionID:      sessionID,
		ToolCallID:     ar.ToolCallID,
		ToolName:       ar.ToolName,
		Summary:        ar.Summary,
		Reason:         ar.Reason,
		TimeoutSeconds: ar.TimeoutSeconds,
	}})

	return r.Wait(ctx, ar.ToolCallID, ar.TimeoutSeconds, "deny")
}
