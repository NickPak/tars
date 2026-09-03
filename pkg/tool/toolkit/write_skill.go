package toolkit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"tars/pkg/skill"
	"tars/pkg/tool"
)

// writeSkillRiskRules 钉住 write_skill 的危险声明：任何非空 content 的
// 调用都需审批——技能写入影响后续所有会话的 Agent 行为（供应链写入）。
// 审批摘要恰好展示技能正文前 300 字符，供用户预判内容。
var writeSkillRiskRules = []tool.RiskRule{
	{ID: "write", Reason: "创建/覆盖技能：写入技能库，影响后续所有会话",
		ArgsKey: "content", Pattern: regexp.MustCompile(`(?s).+`)},
}

// SkillWriterTool 是 write_skill 工具的载体（Carrier）：持有技能运行时
// （skill.SkillProvider）的写入面。载体本身无资源，Close 为空方法。
type SkillWriterTool struct {
	w skill.Provider
}

// NewSkillWriterTool 创建 write_skill 载体。w 为 nil 时 handler 报错
// （装配层须保证注入技能运行时）。
func NewSkillWriterTool(w skill.Provider) *SkillWriterTool {
	return &SkillWriterTool{w: w}
}

// Definitions 实现 tool.Carrier。
func (t *SkillWriterTool) Definitions() []*tool.Definition {
	return []*tool.Definition{t.definition()}
}

// Close 实现 tool.Carrier：无资源。
func (t *SkillWriterTool) Close() error { return nil }

type writeSkillArgs struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Content     string `json:"content"`
	Category    string `json:"category"`
	Overwrite   bool   `json:"overwrite"`
}

// definition 返回 write_skill 工具定义。
func (t *SkillWriterTool) definition() *tool.Definition {
	return &tool.Definition{
		Name: "write_skill",
		Description: "Create a new skill or overwrite an existing one in the skill library. Use this when " +
			"the current session taught you a REUSABLE procedure worth persisting for future tasks (a " +
			"non-obvious workflow, a project-specific convention, a pitfall and its workaround). Do NOT " +
			"use it for one-off facts, session state, or anything derivable from the repository. `content` " +
			"is the SKILL.md markdown body (when to use, step-by-step workflow, constraints, pitfalls); " +
			"frontmatter is generated from `name`/`description`. Writing requires user approval; a newly " +
			"created skill is DISABLED by default and only enters the catalog after the user enables it " +
			"in Settings.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "Skill name, lowercase letters/digits/hyphens (e.g. \"deploy-app\")",
				},
				"description": map[string]any{
					"type":        "string",
					"description": "One line shown in the skill catalog: what it does and when to use it",
				},
				"content": map[string]any{
					"type":        "string",
					"description": "The SKILL.md markdown body: when to use, workflow, constraints, pitfalls",
				},
				"category": map[string]any{
					"type":        "string",
					"description": "Optional category (e.g. \"development\", \"devops\"); defaults to \"misc\"",
				},
				"overwrite": map[string]any{
					"type":        "boolean",
					"description": "Set true to replace an existing skill with the same name",
				},
			},
			"required": []string{"name", "description", "content"},
		},
		RiskRules: writeSkillRiskRules,
		Handler: func(_ context.Context, raw json.RawMessage) (string, error) {
			args, err := UnmarshalArgs[writeSkillArgs](raw)
			if err != nil {
				return "", fmt.Errorf("invalid arguments: %w", err)
			}
			if t.w == nil {
				return "", errors.New("write_skill requires a skill runtime; none available")
			}
			name := strings.TrimSpace(args.Name)
			created, err := t.w.WriteSkill(name, args.Description, args.Category, args.Content, args.Overwrite)
			if err != nil {
				return "", err
			}
			if created {
				return fmt.Sprintf("skill %q created and registered — currently DISABLED, so it is not in "+
					"the catalog yet. Tell the user it can be enabled in Settings → Skills; once enabled it "+
					"takes effect from the next turn.", name), nil
			}
			return fmt.Sprintf("skill %q overwritten; the new content takes effect from the next turn "+
				"(its enabled state was preserved).", name), nil
		},
	}
}
