package tools

import (
	"context"
	"sync"
)

// Approver 征询用户是否放行一次危险调用。由宿主（GUI/CLI）实现；
// nil 表示非交互（危险调用一律拒绝，安全默认）。
type Approver interface {
	Approve(ctx context.Context, r *ApprovalRequest) (*Answer, error)
}

// RiskTable 记录"本会话常允许"的危险操作类别（内存态，重启清空）。
// 键形如 "run_command:rm-recursive"。并发安全。
type RiskTable struct {
	mu      sync.Mutex
	allowed map[string]bool
}

// NewRiskTable 创建空的常允许表。
func NewRiskTable() *RiskTable {
	return &RiskTable{allowed: make(map[string]bool)}
}

// Allow 把某类危险操作记入常允许表（"本会话常允许此类"）。
func (t *RiskTable) Allow(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.allowed[key] = true
}

// Allowed 报告某类危险操作是否已被用户常允许。
func (t *RiskTable) Allowed(key string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.allowed[key]
}

// Gate 是工具执行的权限决策门（会话级）：把"该不该执行"从"怎么执行"分离。
// 危险分类规则见 risk.go（classifyRisk）；Gate 持有策略状态（常允许表）
// 与用户通道（Approver）。
type Gate struct {
	approver Approver
	risks    *RiskTable
}

// NewGate 创建权限门。risks 为 nil 时内部自建（不跨轮共享）。
func NewGate(approver Approver, risks *RiskTable) *Gate {
	if risks == nil {
		risks = NewRiskTable()
	}
	return &Gate{approver: approver, risks: risks}
}

// Check 判断一次危险调用是否放行：
// 常允许命中直接放行；无用户通道（非交互）默认拒绝；
// 用户答复 "allow_always" 记入常允许表并视为放行。
// 返回的 Answer 语义与历史行为一致（拒绝理由经 Reason 反馈给模型）。
func (g *Gate) Check(ctx context.Context, req *ApprovalRequest) (*Answer, error) {
	if g == nil || req == nil {
		return &Answer{Value: "allow", Source: "rule"}, nil
	}
	if g.risks.Allowed(req.RiskKey) {
		return &Answer{Value: "allow", Source: "rule"}, nil
	}
	if g.approver == nil {
		return &Answer{Value: "deny", Reason: "no interactive session available", Source: "timeout_default"}, nil
	}
	ans, err := g.approver.Approve(ctx, req)
	if err != nil {
		return nil, err
	}
	if ans.Value == "allow_always" {
		g.risks.Allow(req.RiskKey)
		ans.Value = "allow"
	}
	return ans, nil
}
