package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

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

			env := EnvFromCtx(ctx)
			if env == nil || env.Skills == nil {
				return "", errors.New("load_skill requires a skill runtime; none available")
			}
			rt := env.Skills

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
