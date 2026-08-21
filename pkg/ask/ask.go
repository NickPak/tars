package ask

import "context"

// ============================================================================
// 交互询问（ask_user 工具 + 危险调用审批门）的共享类型与 ctx 通道
//
// 设计（plan/agent-tool-design-plan.md 2.13）：
//   - ask_user：模型主动发起的人机对齐询问（澄清需求/方案选择/信息请求）。
//   - 审批门：框架在执行层拦截危险调用后自动发起的安全审批——模型不参与
//     决策（否则提示注入可让模型自己批自己）。
// 两者共用同一套"阻塞等待用户答复"机制：handler 阻塞在 channel 上，
// 前端提交答案后经 ResolveAsk 回写。Asker 由宿主（turn 脚手架）实现并
// 经 ctx 注入，tools 包保持对 Wails 事件的无知。
// ============================================================================

const (
	DefaultAskTimeout    = 120 // 秒
	DefaultAskMaxTimeout = 600
)

// Question 是 ask_user 工具的一次结构化询问。
type Question struct {
	Type           string           `json:"type"`                      // confirm / select / input
	Question       string           `json:"question"`                  // 完整、独立的问题
	Options        []QuestionOption `json:"options,omitempty"`         // select 必填
	Recommended    string           `json:"recommended,omitempty"`     // 推荐项 id + 理由（展示用）
	TimeoutSeconds int              `json:"timeout_seconds,omitempty"` // 超时秒数
	Default        string           `json:"default,omitempty"`         // 超时默认答案（必须保守）
}

type QuestionOption struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// Answer 是一次询问/审批的答复。
type Answer struct {
	// Value：confirm 为 "confirm"/"deny"；select 为选项 id；input 为文本；
	// 审批为 "allow"/"allow_always"/"deny"。
	Value  string `json:"value"`
	Reason string `json:"reason,omitempty"` // 拒绝理由（可选，回模型调整方案）
	// Source："user" 用户答复 / "timeout_default" 超时默认 / "rule" 常允许命中。
	Source string `json:"source"`
}

// AskProvider 由宿主实现：把 ask_user 的询问桥接到前端并等待答复。
// 宿主经 Env.Asker 注入（见 env.go）；危险调用的审批通道是独立的
// Approver 接口（见 gate.go），同一宿主实现可同时承担两者。
type AskProvider interface {
	Ask(ctx context.Context, requestID string, q *Question) (*Answer, error)
}
