package kernel

import (
	"context"
	"encoding/json"
	"regexp"
)

// RiskLevel 是工具调用的风险声明级别。
type RiskLevel string

const (
	// RiskLevelLow 只读查询，不触发审批（与空值行为一致，仅作显式标注）。
	RiskLevelLow RiskLevel = "low"
	// RiskLevelMedium 执行前需用户审批（MCP 工具默认）。
	RiskLevelMedium RiskLevel = "medium"
	// RiskLevelHigh 需用户审批，GUI 中醒目标注。
	RiskLevelHigh RiskLevel = "high"
)

// RiskRule 是工具声明的一条危险模式。
type RiskRule struct {
	// ID 常允许键后缀（与工具名组成 RiskKey）。
	ID string
	// Reason 展示给用户的风险说明。
	Reason string
	// Pattern 命中即需审批。
	Pattern *regexp.Regexp
	// ArgsKey 从 JSON 参数中提取匹配目标文本的字段名。
	ArgsKey string
}

// Handler 是工具的执行体。args 是模型生成的 JSON 参数原文，
// 返回的字符串会作为 tool 消息的 content 回填给模型。
type Handler func(ctx context.Context, args json.RawMessage) (string, error)

// Definition 描述一个可注册的工具：对模型的申明 + 本地执行体。
type Definition struct {
	// Name 工具名，模型按此发起调用。a-z、A-Z、0-9、下划线、连字符，最长 64。
	Name string
	// Description 给模型看的说明，是模型选择何时/如何调用工具的唯一依据。
	Description string
	// Parameters 参数的 JSON Schema，例如：
	// 	map[string]any{
	// 		"type": "object",
	// 		"properties": map[string]any{"path": map[string]any{"type": "string"}},
	// 		"required": []string{"path"},
	// 	}
	Parameters map[string]any
	// Handler 本地执行体。
	Handler Handler
	// Risk 是工具的风险声明（纯声明的一部分）：
	// 非空且非 low 时每次调用前应由 Policy 按声明拦截（声明优先于规则匹配）；
	// 空值/low 回落到 RiskRules 的细粒度规则分类。目前仅 MCP 动态注册使用
	// （第三方工具危险不可枚举，按服务器配置声明）。
	Risk RiskLevel
	// RiskRules 工具声明的危险模式集：调用参数中文本字段命中某条规则的
	// 模式即需审批。规则与工具实现同文件声明（危险性与工具定义内聚），
	// 由策略层（如 pkg/tool/guard）的通用匹配引擎评估——策略不认识
	// 任何具体工具的参数结构。
	RiskRules []RiskRule
}

// ToolResult 记录一次工具调用的执行结果。
type ToolResult struct {
	ID     string // 对应的 tool_call_id
	Name   string // 工具名
	Args   string // 模型生成的原始 JSON 参数
	Output string // 执行结果文本；失败时为失败原因，永远不为空
	// Error 非空表示执行失败（Handler 返回 error / panic / 工具不存在）。
	// 成功时为 nil——判断成败用这个字段，不要靠 Output 的字符串前缀。
	Error error
}

// OnToolComplete is invoked when a single tool finishes execution (in its own
// goroutine), carrying the full ToolResult. Use it to push per-tool progress
// to the frontend in real time without waiting for all tools to complete.
type OnToolComplete func(result ToolResult)

// Carrier 是工具的载体：持有注入的依赖与资源，产出其所承载工具的
// Definition，会话销毁时释放资源。
type Carrier interface {
	Definitions() []*Definition
	Close() error
}
