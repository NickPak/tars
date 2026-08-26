// Package guard 是工具执行前的审批策略：危险分类引擎（Classify）、
// 权限门（Gate，实现 tool.Policy）与会话级常允许表（RiskTable）。
//
// 分类知识全部来自工具的纯声明：Definition.Risk（整体级别，MCP 工具用）
// 与 Definition.RiskRules（按参数内容的细粒度模式，内置工具用）——
// 本包不认识任何具体工具的参数结构（不 import 实现包）。
package guard

import (
	"encoding/json"
	"tars/pkg/tool/kernel"

	"tars/pkg/ask"
	"tars/pkg/schema"
)

// summaryMaxLen 是审批摘要的展示截断长度。
const summaryMaxLen = 300

// Classify 判断一次工具调用是否危险；危险则返回审批请求，安全返回 nil。
//
// 声明优先：Definition.Risk 为 medium/high 时按整体级别拦截；否则回落到
// Definition.RiskRules 的逐条模式匹配。参数非法（JSON 解不开/目标字段
// 缺失或为空）时不按危险拦——交给工具自身报参数错误。
func Classify(def *kernel.Definition, call schema.ToolCall) *ask.ApprovalRequest {
	if def.Risk == kernel.RiskLevelMedium || def.Risk == kernel.RiskLevelHigh {
		return byLevel(call, def.Risk)
	}
	if len(def.RiskRules) == 0 {
		return nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(call.Args), &fields); err != nil {
		return nil
	}
	for _, r := range def.RiskRules {
		var text string
		if raw, ok := fields[r.ArgsKey]; ok {
			// 字段非字符串类型视为空（同"缺失"处理）
			_ = json.Unmarshal(raw, &text)
		}
		if text == "" || !r.Pattern.MatchString(text) {
			continue
		}
		return &ask.ApprovalRequest{
			ToolCallID:     call.ID,
			ToolName:       call.Name,
			Summary:        truncateSummary(text),
			Reason:         r.Reason,
			RiskKey:        call.Name + ":" + r.ID,
			TimeoutSeconds: ask.DefaultAskTimeout,
		}
	}
	return nil
}

// byLevel 按声明的风险级别生成审批请求（MCP 工具用）：审批摘要为参数
// 截断展示。RiskKey 不含参数——"常允许"按工具名粒度记忆（MCP 工具的
// 参数各异，按参数记忆会导致每次调用都重新审批）。
func byLevel(call schema.ToolCall, level kernel.RiskLevel) *ask.ApprovalRequest {
	return &ask.ApprovalRequest{
		ToolCallID:     call.ID,
		ToolName:       call.Name,
		Summary:        truncateSummary(call.Args),
		Reason:         "external MCP tool (declared risk: " + string(level) + ")",
		RiskKey:        call.Name,
		TimeoutSeconds: ask.DefaultAskTimeout,
	}
}

// truncateSummary 截断审批摘要（防超长命令/代码撑爆审批 UI）。
func truncateSummary(s string) string {
	if len(s) > summaryMaxLen {
		return s[:summaryMaxLen] + "\n…"
	}
	return s
}
