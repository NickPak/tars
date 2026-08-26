package toolkit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"tars/pkg/skill"
	"tars/pkg/tool/kernel"

	"tars/pkg/mcp"
)

// DiscoverTool 是 discover_tools 工具的载体（Carrier）：持有两个能力源
// Provider（技能检索 + MCP 检索/物化）。无自有资源，Close 为空方法。
type DiscoverTool struct {
	skillRt skill.SkillProvider
	mcpRt   mcp.McpProvider
}

// NewDiscoverTool 创建 discover_tools 载体。skillRt / mcpRt 为 nil 时
// 跳过对应能力源。
func NewDiscoverTool(skillRt skill.SkillProvider, mcpRt mcp.McpProvider) *DiscoverTool {
	return &DiscoverTool{skillRt: skillRt, mcpRt: mcpRt}
}

// Definitions 实现 tool.Carrier。
func (t *DiscoverTool) Definitions() []*kernel.Definition {
	return []*kernel.Definition{t.definition()}
}

// Close 实现 tool.Carrier：无资源。
func (t *DiscoverTool) Close() error { return nil }

// definition 返回 discover_tools 工具定义（plan 3.1 发现元工具）。
//
// 按自然语言能力需求检索全部能力源，返回少量候选（数量由 skills 配置的
// discoverResultLimit 决定，技能与 MCP 工具各取该上限）：
//   - Skills（本地技能库）：命中后 load_skill(name) 注入完整操作手册；
//   - MCP 工具（外部服务器）：命中即注册进本会话工具集（Materialize，
//     懒启动进程），下一轮起模型可直接按其全名调用（完整 schema 随工具
//     定义一次性下发，防"工具未被定义"的幻觉调用）。
//
// 无命中明确返回"未找到"，触发兜底链路（改需求重试 / 核心工具自行实现）。
func (t *DiscoverTool) definition() *kernel.Definition {
	return &kernel.Definition{
		Name: "discover_tools",
		Description: "Search available capabilities by natural-language need: installed skills and external " +
			"MCP tool servers. Returns the top candidates matching the query. For a skill, call load_skill(name) " +
			"to load its playbook; for an MCP tool, it is registered for this conversation immediately and can be " +
			"called directly. Use when you face a task that the core tools cannot handle, or you are unsure whether " +
			"a suitable skill or tool exists. Pass a short capability description such as '查询股价' or " +
			"'edit Word documents'. If there is no match, rephrase with different words (implementation-focused " +
			"or user-goal-focused); if there is still no match, implement with the core tools or write code on the fly.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "capability need in natural language, e.g. '查询股价', 'edit Word documents'",
				},
			},
			"required": []string{"query"},
		},
		Handler: func(_ context.Context, raw json.RawMessage) (string, error) {
			var args struct {
				Query string `json:"query"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return "", fmt.Errorf("invalid arguments: %w", err)
			}
			query := strings.TrimSpace(args.Query)
			if query == "" {
				return "", errors.New("discover_tools: query is required")
			}

			limit := 5
			if t.skillRt != nil {
				limit = t.skillRt.SearchLimit()
			}

			var skills []skill.SkillSummary
			if t.skillRt != nil {
				hits, err := t.skillRt.Search(query, limit)
				if err != nil {
					return "", fmt.Errorf("discover_tools: %w", err)
				}
				skills = hits
			}

			var mcpHits []mcp.ToolHit
			if t.mcpRt != nil {
				hits, err := t.mcpRt.Search(query, limit)
				if err != nil {
					return "", fmt.Errorf("discover_tools: %w", err)
				}
				mcpHits = hits
			}

			if len(skills) == 0 && len(mcpHits) == 0 {
				return "No matching capabilities found (skills or MCP tools). Rephrase the query and retry; " +
					"if there is still no match, implement it with the core tools or write code on the fly.", nil
			}

			var b strings.Builder
			if len(skills) > 0 {
				fmt.Fprintf(&b, "Found %d candidate skills:\n", len(skills))
				for i, h := range skills {
					fmt.Fprintf(&b, "%d. %s — %s", i+1, h.Name, h.Description)
					if h.Category != "" {
						fmt.Fprintf(&b, " [%s]", h.Category)
					}
					b.WriteString("\n")
				}
				b.WriteString("→ Call load_skill(name) on the skill you need to get its full playbook.\n")
			}
			if len(mcpHits) > 0 {
				if b.Len() > 0 {
					b.WriteString("\n")
				}
				fmt.Fprintf(&b, "Found %d MCP tools (now registered for this conversation — call them directly):\n", len(mcpHits))
				for i, h := range mcpHits {
					if err := t.mcpRt.Materialize(h); err != nil {
						fmt.Fprintf(&b, "%d. %s — %s [registration failed: %v]\n", i+1, h.FullName, h.Description, err)
						continue
					}
					fmt.Fprintf(&b, "%d. %s — %s", i+1, h.FullName, h.Description)
					if h.SourceType != "" {
						fmt.Fprintf(&b, " [%s]", h.SourceType)
					}
					b.WriteString("\n")
				}
			}
			return strings.TrimRight(b.String(), "\n"), nil
		},
	}
}
