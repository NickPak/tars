package kernel

import (
	"context"

	"tars/pkg/schema"
)

// Decision 是 Policy 对一次工具调用的裁决。
type Decision struct {
	// Allow 为 false 时调用被拦截，handler 不执行。
	Allow bool
	// Output 拦截时作为工具结果回填给模型的内容（拒绝理由回模型，
	// 据此调整方案）；Allow 为 true 时忽略。
	Output string
}

// PolicyProvider 是工具执行前的审批策略
type PolicyProvider interface {
	// Check 在 handler 执行前裁决一次调用。返回 error 表示裁决本身
	// 失败（执行器将其作为工具错误结果处理）。
	Check(ctx context.Context, def *Definition, call schema.ToolCall) (*Decision, error)
}
