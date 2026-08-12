package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

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

// ApprovalRequest 是执行层拦截危险调用后生成的审批请求。
type ApprovalRequest struct {
	ToolCallID     string // 即被拦截调用的 ID，也是答复键
	ToolName       string
	Summary        string // 待批准的危险内容（如完整命令）
	Reason         string // 命中的风险规则说明
	RiskKey        string // 常允许规则键（"本会话常允许此类"按此记忆）
	TimeoutSeconds int
}

// Asker 由宿主实现：把询问/审批桥接到前端并等待答复。
type Asker interface {
	Ask(ctx context.Context, q *Question) (*Answer, error)
	Approve(ctx context.Context, r *ApprovalRequest) (*Answer, error)
}

type askerCtxKey struct{}
type toolCallIDCtxKey struct{}

// WithAsker 把交互询问通道放入 ctx（宿主调用）。
func WithAsker(ctx context.Context, a Asker) context.Context {
	return context.WithValue(ctx, askerCtxKey{}, a)
}

// AskerFromCtx 取出交互询问通道；非交互场景（测试等）返回 nil。
func AskerFromCtx(ctx context.Context) Asker {
	a, _ := ctx.Value(askerCtxKey{}).(Asker)
	return a
}

// WithToolCallID 把当前工具调用 ID 放入 ctx（执行器调用），
// 交互工具/审批门用它作为答复的关联键。
func WithToolCallID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, toolCallIDCtxKey{}, id)
}

// ToolCallIDFromCtx 取出当前工具调用 ID；不存在返回 ""。
func ToolCallIDFromCtx(ctx context.Context) string {
	id, _ := ctx.Value(toolCallIDCtxKey{}).(string)
	return id
}

const (
	askDefaultTimeout = 120 // 秒
	askMaxTimeout     = 600
)

// AskUser 返回 ask_user 工具：模型主动向用户发起结构化询问。
// handler 阻塞等待答复（ReAct 循环随之暂停），直到用户提交、超时
// （返回保守默认）或轮被取消。
func AskUser() *Definition {
	return &Definition{
		Name: "ask_user",
		Description: "Ask the user a STRUCTURED question to align before acting — the agent loop pauses until " +
			"the user answers. Use when: requirements are ambiguous and a wrong guess wastes effort; multiple " +
			"reasonable approaches exist (present 2-4 options with your recommendation); you need information " +
			"only the user has (credentials, environment specifics, business context). Do NOT use for questions " +
			"answerable by reading code or running commands, and never ask about safety-critical operations " +
			"(dangerous actions are gated by the framework automatically). One question per call; each call " +
			"should be self-contained. Types: confirm (yes/no decision), select (choose from options), " +
			"input (free text). Always set timeout_seconds and a conservative default — if the user does not " +
			"respond in time, the default is applied automatically.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"question": map[string]any{"type": "string", "description": "The complete, self-contained question to show the user"},
				"type":     map[string]any{"type": "string", "enum": []string{"confirm", "select", "input"}, "description": "Interaction form"},
				"options": map[string]any{
					"type":        "array",
					"description": "Required for select: 2-4 options",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"id":          map[string]any{"type": "string", "description": "Short option id, e.g. \"a\""},
							"label":       map[string]any{"type": "string", "description": "Option title"},
							"description": map[string]any{"type": "string", "description": "What this option means / its trade-offs"},
						},
						"required": []string{"id", "label"},
					},
				},
				"recommended":     map[string]any{"type": "string", "description": "Recommended option id and why, e.g. \"b — smallest blast radius\""},
				"timeout_seconds": map[string]any{"type": "integer", "description": "Seconds to wait for the user, default 120, max 600"},
				"default":         map[string]any{"type": "string", "description": "Answer applied on timeout; must be the conservative choice (confirm: \"deny\")"},
			},
			"required": []string{"question", "type"},
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var q Question
			if err := json.Unmarshal(raw, &q); err != nil {
				return "", fmt.Errorf("invalid arguments: %w", err)
			}
			if err := validateQuestion(&q); err != nil {
				return "", err
			}

			asker := AskerFromCtx(ctx)
			if asker == nil {
				return "", errors.New("ask_user requires an interactive session; none available")
			}
			ans, err := asker.Ask(ctx, &q)
			if err != nil {
				return "", err // 轮被取消
			}

			out := map[string]any{
				"type":   q.Type,
				"answer": ans.Value,
				"source": ans.Source,
			}
			if q.Type == "select" {
				for _, o := range q.Options {
					if o.ID == ans.Value {
						out["label"] = o.Label
						break
					}
				}
			}
			if ans.Reason != "" {
				out["reason"] = ans.Reason
			}
			b, _ := json.Marshal(out)
			return string(b), nil
		},
	}
}

func validateQuestion(q *Question) error {
	switch q.Type {
	case "confirm", "input":
	case "select":
		if len(q.Options) < 2 {
			return errors.New("select requires at least 2 options")
		}
	default:
		return fmt.Errorf("invalid type %q: must be confirm/select/input", q.Type)
	}
	if q.Question == "" {
		return errors.New("question is required")
	}
	if q.TimeoutSeconds <= 0 {
		q.TimeoutSeconds = askDefaultTimeout
	}
	q.TimeoutSeconds = min(q.TimeoutSeconds, askMaxTimeout)
	if q.Default == "" {
		// 保守兜底：confirm 拒绝，select 取推荐项或第一项，input 空
		switch q.Type {
		case "confirm":
			q.Default = "deny"
		case "select":
			q.Default = q.Options[0].ID
		}
	}
	return nil
}
