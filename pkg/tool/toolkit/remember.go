package toolkit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"tars/pkg/ask"
	"tars/pkg/memory"
	"tars/pkg/tool"
)

// RememberTool 是 remember 工具的载体（Carrier）：持有记忆写入能力
// （memory.Provider）与交互通道（ask.AskProvider，覆盖写确认用）。
// 无外部资源，Close 为空方法。
type RememberTool struct {
	mp    memory.Provider
	asker ask.AskProvider
}

// NewRememberTool 创建 remember 载体。
func NewRememberTool(mp memory.Provider, asker ask.AskProvider) *RememberTool {
	return &RememberTool{mp: mp, asker: asker}
}

// Definitions 实现 tool.Carrier。
func (t *RememberTool) Definitions() []*tool.Definition {
	return []*tool.Definition{t.definition()}
}

// Close 实现 tool.Carrier：无资源。
func (t *RememberTool) Close() error { return nil }

type rememberArgs struct {
	Type      string `json:"type"`
	Subject   string `json:"subject"`
	Body      string `json:"body"`
	Scope     string `json:"scope"`
	Overwrite bool   `json:"overwrite"`
}

// definition 返回 remember 工具定义。
func (t *RememberTool) definition() *tool.Definition {
	return &tool.Definition{
		Name: "remember",
		Description: "Persist a fact for FUTURE sessions (cross-session memory). ONLY record what the user " +
			"explicitly stated (preferences, decisions, constraints) or a lesson genuinely learned in this " +
			"session. Do NOT record: anything derivable from the repository (architecture, paths, debug " +
			"history), content already in AGENTS.md, temporary task state, model-specific workarounds, or " +
			"secrets (passwords/keys are REJECTED). `type`: user (global preference), feedback (how the user " +
			"wants you to work), project (project-specific fact), reference (external pointer), lesson " +
			"(pitfall/workflow worth repeating). `scope`: project (default, most facts) or global (only for " +
			"true cross-project user preferences). `subject`: short key like \"prefer-pnpm\" — reusing an " +
			"existing subject without overwrite=true fails; overwriting asks the user to confirm (the old " +
			"value is archived, never lost).",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"type": map[string]any{
					"type":        "string",
					"enum":        []string{"user", "feedback", "project", "reference", "lesson"},
					"description": "Fact type",
				},
				"subject": map[string]any{
					"type":        "string",
					"description": "Short unique key, letters/digits/hyphens (e.g. \"prefer-pnpm\")",
				},
				"body": map[string]any{
					"type":        "string",
					"description": "One or two sentences stating the fact itself",
				},
				"scope": map[string]any{
					"type":        "string",
					"enum":        []string{"project", "global"},
					"description": "project (default) or global (cross-project user facts only)",
				},
				"overwrite": map[string]any{
					"type":        "boolean",
					"description": "Set true to update an existing subject (asks the user to confirm)",
				},
			},
			"required": []string{"type", "subject", "body"},
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			args, err := UnmarshalArgs[rememberArgs](raw)
			if err != nil {
				return "", fmt.Errorf("invalid arguments: %w", err)
			}
			if t.mp == nil {
				return "", errors.New("remember requires a memory runtime; none available")
			}

			in := &memory.RememberInput{
				Type:      memory.FactType(strings.TrimSpace(args.Type)),
				Subject:   args.Subject,
				Body:      args.Body,
				Scope:     memory.Scope(args.Scope),
				Overwrite: args.Overwrite,
			}

			// 覆盖写需用户确认（创建走白名单自动放行；旧值归档留痕，永不真删）。
			// 先做一次"干跑"校验：subject 存在且未确认时才弹确认——创建路径零打扰。
			if in.Overwrite {
				if t.asker == nil {
					return "", errors.New("overwriting memory requires an interactive session")
				}
				ans, err := t.asker.Ask(ctx, tool.CallIDFromCtx(ctx), &ask.Question{
					Type:     "confirm",
					Question: fmt.Sprintf("覆盖已有记忆「%s」？旧值将归档保留，可随时恢复。", strings.TrimSpace(args.Subject)),
					Default:  "deny",
				})
				if err != nil {
					return "", err
				}
				if ans.Value != "confirm" {
					return fmt.Sprintf("user declined to overwrite memory %q; nothing was written", args.Subject), nil
				}
			}

			created, err := t.mp.Remember(in)
			if err != nil {
				return "", err
			}
			scope := in.Scope
			if scope == "" {
				scope = memory.ScopeProject
			}
			verb := "updated"
			if created {
				verb = "remembered"
			}
			return fmt.Sprintf("memory %s: [%s] %s (scope: %s). It appears in the memory index from the next turn.",
				verb, in.Type, strings.TrimSpace(in.Subject), scope), nil
		},
	}
}
