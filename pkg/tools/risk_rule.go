package tools

import (
	"encoding/json"
	"regexp"
	"strings"
	"tars/pkg/ask"
	"tars/pkg/schema"
)

// ============================================================================
// 危险调用审批门（plan/agent-tool-design-plan.md 2.13 安全审批）
//
// 执行层拦截：模型发出危险调用后、handler 执行前，由框架发起用户审批。
// 模型不参与决策（防提示注入让模型自批），只能看到结果。
// 风险分类按工具声明，规则表可扩展；v1 覆盖 run_command 的危险命令模式
// 与 code_interpreter 的明显破坏性调用。
// ============================================================================

// riskRule 一条危险模式规则。
type riskRule struct {
	id      string         // 常允许键后缀（与工具名组成 RiskKey）
	reason  string         // 展示给用户的风险说明
	pattern *regexp.Regexp // 命中即需审批
}

// runCommandRiskRules 针对 shell 命令文本匹配（Windows cmd 与 POSIX sh 兼顾）。
// 注意分隔符截断用 [^|;&]：命中 `cmd1 && dangerous` 链式写法中的危险段。
var runCommandRiskRules = []riskRule{
	// rm 后紧跟的纯字母旗标串中任一含 r（-r/-rf/-fr/--recursive，含分离写法
	// rm -r -f）；旗标串之外的 -word（如文件名 my-report.txt）不误判
	{"rm-recursive", "递归删除（rm -r / rm -rf）", regexp.MustCompile(`(?i)\brm\b(?:\s+-{1,2}[a-z]+)*\s+-{1,2}[a-z]*r[a-z]*`)},
	{"win-recursive-delete", "递归删除（del/rmdir /s、Remove-Item -Recurse、robocopy /MIR）", regexp.MustCompile(`(?i)\b(?:del|erase|rmdir|rd)\b[^|;&]*\s/s\b|\bRemove-Item\b[^|;&]*-r(ecurse)?\b|\brobocopy\b[^|;&]*/mir\b`)},
	{"dd-disk-write", "直接写块设备（dd）", regexp.MustCompile(`(?i)\bdd\s+[^|;&]*\bif=`)},
	{"mkfs-format", "格式化/创建文件系统", regexp.MustCompile(`(?i)\bmkfs\b|\bformat\s+[a-z]:`)},
	{"pipe-to-shell", "下载并直接执行远程脚本（curl|sh）", regexp.MustCompile(`(?i)\b(curl|wget)\b[^|;]*\|\s*(sudo\s+)?(bash|sh|zsh)\b`)},
	{"git-force-push", "强制推送（git push --force）", regexp.MustCompile(`(?i)\bgit\s+push\b[^|;&]*(\s-f\b|--force)`)},
	{"git-reset-hard", "硬重置丢弃改动（git reset --hard）", regexp.MustCompile(`(?i)\bgit\s+reset\s+--hard\b`)},
	{"shutdown-reboot", "关机/重启", regexp.MustCompile(`(?i)\bshutdown\b|\breboot\b`)},
}

// codeInterpreterRiskRules 针对 Python 代码的明显破坏性调用。
var codeInterpreterRiskRules = []riskRule{
	{"py-rmtree", "递归删除目录（shutil.rmtree）", regexp.MustCompile(`\bshutil\.rmtree\s*\(`)},
	{"py-remove", "删除文件（os.remove/os.unlink）", regexp.MustCompile(`\bos\.(remove|unlink)\s*\(`)},
	{"py-shell-out", "Python 内执行 shell（os.system/subprocess）", regexp.MustCompile(`\bos\.system\s*\(|\bsubprocess\.`)},
}

// classifyRisk 判断一次工具调用是否危险；危险则返回审批请求。
// 安全调用返回 nil。常允许键形如 "run_command:rm-recursive-force"。
func classifyRisk(call schema.ToolCall) *ask.ApprovalRequest {
	var rules []riskRule
	var target, summary string

	switch call.Name {
	case "run_command":
		var args runCommandArgs
		if json.Unmarshal([]byte(call.Args), &args) != nil || args.Command == "" {
			return nil // 参数非法：交给工具自身报错，不按危险拦
		}
		rules, target, summary = runCommandRiskRules, args.Command, args.Command
	case "code_interpreter":
		var args struct {
			Code string `json:"code"`
		}
		if json.Unmarshal([]byte(call.Args), &args) != nil || args.Code == "" {
			return nil
		}
		rules, target = codeInterpreterRiskRules, args.Code
		if len(args.Code) > 300 {
			summary = args.Code[:300] + "\n…"
		} else {
			summary = args.Code
		}
	default:
		// MCP 动态注册工具（mcp__ 前缀）：第三方进程，危险不可枚举，
		// 不走模式匹配——按服务器配置的 RiskLevel 声明拦截。
		if strings.HasPrefix(call.Name, "mcp__") {
			return classifyRiskWithLevel(call, RiskLevelMedium)
		}
		return nil
	}

	for _, r := range rules {
		if r.pattern.MatchString(target) {
			return &ask.ApprovalRequest{
				ToolCallID:     call.ID,
				ToolName:       call.Name,
				Summary:        summary,
				Reason:         r.reason,
				RiskKey:        call.Name + ":" + r.id,
				TimeoutSeconds: ask.DefaultAskTimeout,
			}
		}
	}
	return nil
}

// classifyRiskWithLevel 按声明的风险级别生成审批请求（MCP 工具用）：
// low 返回 nil（不拦截）；medium/high 拦截，审批摘要含工具参数截断展示。
// RiskKey 不含参数——"常允许"按工具名粒度记忆（MCP 工具的参数各异，
// 按参数记忆会导致每次调用都重新审批）。
func classifyRiskWithLevel(call schema.ToolCall, level RiskLevel) *ask.ApprovalRequest {
	if level == "" || level == RiskLevelLow {
		return nil
	}
	summary := call.Args
	if len(summary) > 300 {
		summary = summary[:300] + "\n…"
	}
	reason := "external MCP tool (declared risk: " + string(level) + ")"
	return &ask.ApprovalRequest{
		ToolCallID:     call.ID,
		ToolName:       call.Name,
		Summary:        summary,
		Reason:         reason,
		RiskKey:        call.Name,
		TimeoutSeconds: ask.DefaultAskTimeout,
	}
}
