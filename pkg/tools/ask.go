package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"tars/pkg/ask"
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
			var q ask.Question
			if err := json.Unmarshal(raw, &q); err != nil {
				return "", fmt.Errorf("invalid arguments: %w", err)
			}
			if err := validateQuestion(&q); err != nil {
				return "", err
			}

			toolCallID := ToolCallIDFromCtx(ctx)

			env := EnvFromCtx(ctx)
			if env == nil || env.Ask == nil {
				return "", errors.New("ask_user requires an interactive session; none available")
			}
			ans, err := env.Ask.Ask(ctx, toolCallID, &q)
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

func validateQuestion(q *ask.Question) error {
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
		q.TimeoutSeconds = ask.DefaultAskTimeout
	}
	q.TimeoutSeconds = min(q.TimeoutSeconds, ask.DefaultAskMaxTimeout)
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
