package runner

import (
	"context"
	"errors"
	"sync"
	"time"

	"tars/internal/event"
	"tars/internal/session"
	"tars/pkg/tools"
)

// ============================================================================
// Asker 实现与答复注册表
//
// ask_user（模型主动询问）与审批门（框架拦截危险调用）共用同一套
// "阻塞等待用户答复"机制：handler 阻塞在此，前端提交后经 Runner.ResolveAsk
// 回写。答复键即工具调用 ID——agent:tool/agent:tool_result 事件已携带它，
// 前端据此渲染卡片与终态，无需额外事件（审批除外：问题负载由框架生成，
// 经 agent:approval 事件下发）。
// ============================================================================

// asker 把一次轮的询问/审批桥接到前端。
type asker struct {
	sess *session.Info
	asks *askRegistry
	sink event.Sink
}

func newAsker(sess *session.Info, asks *askRegistry, sink event.Sink) *asker {
	return &asker{sess: sess, asks: asks, sink: sink}
}

// Ask 处理 ask_user 的模型询问。问题负载经 agent:tool 事件的参数直达
// 前端，这里只需登记答复通道并阻塞等待。
func (a *asker) Ask(ctx context.Context, q *tools.Question) (*tools.Answer, error) {
	id := tools.ToolCallIDFromCtx(ctx)
	if id == "" {
		return nil, errors.New("asker: missing tool call id in context")
	}
	return a.asks.wait(ctx, id, q.TimeoutSeconds, q.Default)
}

// Approve 处理危险调用审批：先查会话级常允许表，未命中则发
// agent:approval 事件并阻塞等待；"常允许"答复记入会话。
func (a *asker) Approve(ctx context.Context, r *tools.ApprovalRequest) (*tools.Answer, error) {
	if a.sess.RiskAllowed(r.RiskKey) {
		return &tools.Answer{Value: "allow", Source: "rule"}, nil
	}

	a.sink.Approval(event.ApprovalEvent{
		SessionID:      a.sess.ID,
		ToolCallID:     r.ToolCallID,
		ToolName:       r.ToolName,
		Summary:        r.Summary,
		Reason:         r.Reason,
		TimeoutSeconds: r.TimeoutSeconds,
	})

	ans, err := a.asks.wait(ctx, r.ToolCallID, r.TimeoutSeconds, "deny")
	if err != nil {
		return nil, err
	}
	if ans.Value == "allow_always" {
		a.sess.AllowRisk(r.RiskKey)
		ans.Value = "allow"
	}
	return ans, nil
}

// --- 答复注册表 ---

// askRegistry 持有询问/审批答复通道注册表。它是 Runner 的字段，
// 不再使用包级全局单例。
type askRegistry struct {
	mu      sync.Mutex
	pending map[string]chan *tools.Answer // 等待中的答复通道
	early   map[string]*tools.Answer      // 先到的答复（用户手快于登记）
}

func newAskRegistry() *askRegistry {
	return &askRegistry{
		pending: make(map[string]chan *tools.Answer),
		early:   make(map[string]*tools.Answer),
	}
}

// resolve 回写一次答复。无等待方则暂存（先到先得）。
func (a *askRegistry) resolve(toolCallID string, ans *tools.Answer) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if ch, ok := a.pending[toolCallID]; ok {
		delete(a.pending, toolCallID)
		ch <- ans
		return true
	}
	a.early[toolCallID] = ans
	return true
}

func (a *askRegistry) wait(ctx context.Context, id string, timeoutSec int, defaultVal string) (*tools.Answer, error) {
	a.mu.Lock()
	if ans, ok := a.early[id]; ok {
		delete(a.early, id)
		a.mu.Unlock()
		return ans, nil
	}
	ch := make(chan *tools.Answer, 1)
	a.pending[id] = ch
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		delete(a.pending, id)
		a.mu.Unlock()
	}()

	if timeoutSec <= 0 {
		timeoutSec = 120
	}
	timer := time.NewTimer(time.Duration(timeoutSec) * time.Second)
	defer timer.Stop()

	select {
	case ans := <-ch:
		return ans, nil
	case <-timer.C: // 超时：应用保守默认
		return &tools.Answer{Value: defaultVal, Source: "timeout_default"}, nil
	case <-ctx.Done(): // 轮被取消
		return nil, ctx.Err()
	}
}
