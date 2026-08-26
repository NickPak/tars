package guard

import (
	"context"
	"encoding/json"
	"tars/pkg/tool/kernel"

	"tars/pkg/ask"
	"tars/pkg/event"
	"tars/pkg/schema"
)

// Gate 是工具执行的权限决策门（会话级）：把"该不该执行"从"怎么执行"分离。
// 危险分类见 Classify（纯声明驱动）；Gate 持有策略状态（常允许表）、
// 用户通道（Approver）与审批事件所需的会话上下文（sink/sessionID）。
//
// Gate 实现内核的 tool.Policy 接口，由装配层注入 Registry。
type Gate struct {
	approver  ask.ApproverProvider
	risks     *RiskTable
	sink      event.Sink
	sessionID string
}

var _ kernel.PolicyProvider = (*Gate)(nil)

// NewGate 创建权限门。risks 为 nil 时内部自建（不跨轮共享）；
// sink/sessionID 用于审批请求的事件关联。
func NewGate(approver ask.ApproverProvider, risks *RiskTable, sink event.Sink, sessionID string) *Gate {
	if risks == nil {
		risks = NewRiskTable()
	}
	return &Gate{approver: approver, risks: risks, sink: sink, sessionID: sessionID}
}

func (g *Gate) Startup() error {
	return nil
}

func (g *Gate) Shutdown() error {
	return nil
}

// Check 实现 tool.Policy：判断一次危险调用是否放行。
// 常允许命中直接放行；无用户通道（非交互）默认拒绝；
// 用户答复 "allow_always" 记入常允许表并视为放行。
// 拒绝时 Output 作为正常工具结果回填（理由回模型，据此调整方案）。
func (g *Gate) Check(ctx context.Context, def *kernel.Definition, call schema.ToolCall) (*kernel.Decision, error) {
	req := Classify(def, call)
	if req == nil {
		return &kernel.Decision{Allow: true}, nil
	}
	if g.risks.Allowed(req.RiskKey) {
		return &kernel.Decision{Allow: true}, nil
	}
	if g.approver == nil {
		return deny("approval timed out; denied by default"), nil
	}
	ans, err := g.approver.Approve(ctx, g.sink, g.sessionID, req)
	if err != nil {
		return nil, err
	}
	if ans.Value == "allow_always" {
		g.risks.Allow(req.RiskKey)
		ans.Value = "allow"
	}
	if ans.Value == "allow" {
		return &kernel.Decision{Allow: true}, nil
	}
	switch {
	case ans.Reason != "":
		return deny(ans.Reason), nil
	case ans.Source == "timeout_default":
		return deny("approval timed out; denied by default"), nil
	default:
		return deny("denied by user"), nil
	}
}

// deny 构造拒绝裁决（approved:false + 理由，与历史行为一致）。
func deny(reason string) *kernel.Decision {
	b, _ := json.Marshal(map[string]any{"approved": false, "reason": reason})
	return &kernel.Decision{Allow: false, Output: string(b)}
}
