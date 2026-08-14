package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// SkillRuntime 是 load_skill / discover_tools 工具与状态栏所需的宿主注入能力。
type SkillRuntime interface {
	// Load 返回指定 Skill 的 SKILL.md 全文。
	Load(name string) (string, error)
	// IsLoaded / MarkLoaded 管理会话级"已加载"幂等状态。
	IsLoaded(name string) bool
	MarkLoaded(name string)
	// Loaded 返回已加载技能名（排序后），供状态栏展示。
	Loaded() []string
	// Search 按自然语言需求检索技能（BM25），返回候选；无命中返回空。
	Search(query string, limit int) ([]SkillSummary, error)
}

// SkillSummary 是一次检索命中的技能摘要（discover_tools 返回用）。
type SkillSummary struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Category    string `json:"category"`
}

type skillRuntimeCtxKey struct{}

// WithSkillRuntime 把技能运行时放入 ctx（宿主调用）。
func WithSkillRuntime(ctx context.Context, r SkillRuntime) context.Context {
	return context.WithValue(ctx, skillRuntimeCtxKey{}, r)
}

// SkillRuntimeFromCtx 取出技能运行时；非交互场景返回 nil。
func SkillRuntimeFromCtx(ctx context.Context) SkillRuntime {
	r, _ := ctx.Value(skillRuntimeCtxKey{}).(SkillRuntime)
	return r
}

// LoadSkill 返回 load_skill 工具。
func LoadSkill() *Definition {
	return &Definition{
		Name: "load_skill",
		Description: "Load a Skill's SKILL.md into the conversation to learn how to perform a " +
			"specialized task. The Skill catalog is listed in your system context; call this with the " +
			"skill name before doing the work it describes (it contains the exact workflow, required " +
			"files and conventions). Loading is idempotent — calling again for an already-loaded skill " +
			"returns a short notice. Only load a skill when you actually need it.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "The skill name from the catalog (e.g. \"pptx\" or \"deploy-app\")",
				},
			},
			"required": []string{"name"},
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var args struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return "", fmt.Errorf("invalid arguments: %w", err)
			}
			name := strings.TrimSpace(args.Name)
			if name == "" {
				return "", errors.New("skill name is required")
			}

			rt := SkillRuntimeFromCtx(ctx)
			if rt == nil {
				return "", errors.New("load_skill requires a skill runtime; none available")
			}

			if rt.IsLoaded(name) {
				return fmt.Sprintf("skill %q is already loaded; its instructions are still in effect", name), nil
			}

			content, err := rt.Load(name)
			if err != nil {
				return "", err
			}
			rt.MarkLoaded(name)
			return content, nil
		},
	}
}
