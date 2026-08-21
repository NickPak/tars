package tools

import (
	"context"
	"tars/pkg/ask"
	"tars/pkg/schema"
)

// Gate 是工具执行的权限决策门（会话级）：把"该不该执行"从"怎么执行"分离。
// 危险分类规则见 risk.go（classifyRisk）；Gate 持有策略状态（常允许表）
// 与用户通道（Approver）。
type Gate struct {
	approver ask.ApproverProvider
	risks    *RiskTable
}

// NewGate 创建权限门。risks 为 nil 时内部自建（不跨轮共享）。
func NewGate(approver ask.ApproverProvider, risks *RiskTable) *Gate {
	if risks == nil {
		risks = NewRiskTable()
	}
	return &Gate{approver: approver, risks: risks}
}

// Check 判断一次危险调用是否放行：
// 常允许命中直接放行；无用户通道（非交互）默认拒绝；
// 用户答复 "allow_always" 记入常允许表并视为放行。
// 返回的 Answer 语义与历史行为一致（拒绝理由经 Reason 反馈给模型）。
func (g *Gate) Check(ctx context.Context, env *Env, def *Definition, call schema.ToolCall) (*ask.Answer, error) {
	// 声明优先：Definition.Risk 为 medium/high 时按声明拦截（MCP 工具）；
	// 否则回落到 classifyRisk 的规则匹配（内置工具的 mode 分类不变）。
	var req *ask.ApprovalRequest
	if def.Risk != "" && def.Risk != RiskLevelLow {
		req = classifyRiskWithLevel(call, def.Risk)
	} else {
		req = classifyRisk(call)
	}

	if req == nil {
		return &ask.Answer{Value: "allow", Source: "rule"}, nil
	}
	if g.risks.Allowed(req.RiskKey) {
		return &ask.Answer{Value: "allow", Source: "rule"}, nil
	}
	if g.approver == nil {
		return &ask.Answer{Value: "deny", Reason: "no interactive session available", Source: "timeout_default"}, nil
	}
	ans, err := g.approver.Approve(ctx, env.Sink, env.SessionID, req)
	if err != nil {
		return nil, err
	}
	if ans.Value == "allow_always" {
		g.risks.Allow(req.RiskKey)
		ans.Value = "allow"
	}
	return ans, nil
}
