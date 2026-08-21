package ask

import (
	"context"
	"tars/internal/event"
)

// ApproverProvider 征询用户是否放行一次危险调用。由宿主（GUI/CLI）实现；
// nil 表示非交互（危险调用一律拒绝，安全默认）。
type ApproverProvider interface {
	Approve(ctx context.Context, sink event.Sink, sessionID string, ar *ApprovalRequest) (*Answer, error)
}

// ApprovalRequest 是执行层拦截危险调用后生成的审批请求。
type ApprovalRequest struct {
	ToolCallID     string // 即被拦截调用的 ID，也是答复键
	ToolName       string
	Summary        string // 待批准的危险内容（如完整命令）
	Reason         string // 命中的风险规则说明
	RiskKey        string // 常允许规则键（"本会话常允许此类"按此记忆）
	TimeoutSeconds int
}
