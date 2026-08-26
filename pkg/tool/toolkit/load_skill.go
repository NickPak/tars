package toolkit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"tars/pkg/skill"
	"tars/pkg/tool/kernel"
)

// SkillTool 是 load_skill 工具的载体（Carrier）：持有技能运行时
// （skills.SkillProvider）。"已加载"幂等状态由 Provider 会话级持有，
// 载体本身无资源，Close 为空方法。
type SkillTool struct {
	rt skill.SkillProvider
}

// NewSkillTool 创建 load_skill 载体。rt 为 nil 时 handler 报错
// （装配层须保证注入技能运行时）。
func NewSkillTool(rt skill.SkillProvider) *SkillTool {
	return &SkillTool{rt: rt}
}

// Definitions 实现 tool.Carrier。
func (t *SkillTool) Definitions() []*kernel.Definition {
	return []*kernel.Definition{t.definition()}
}

// Close 实现 tool.Carrier：无资源。
func (t *SkillTool) Close() error { return nil }

// definition 返回 load_skill 工具定义。
func (t *SkillTool) definition() *kernel.Definition {
	return &kernel.Definition{
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

			if t.rt == nil {
				return "", errors.New("load_skill requires a skill runtime; none available")
			}

			if t.rt.IsLoaded(name) {
				return fmt.Sprintf("skill %q is already loaded; its instructions are still in effect", name), nil
			}

			content, err := t.rt.Load(name)
			if err != nil {
				return "", err
			}
			t.rt.MarkLoaded(name)
			return content, nil
		},
	}
}
