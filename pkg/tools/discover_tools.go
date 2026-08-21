package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"tars/pkg/mcp"
	skills2 "tars/pkg/skills"
)

// DiscoverTools 返回 discover_tools 工具（plan 3.1 发现元工具）。
//
// 按自然语言能力需求检索全部能力源，返回少量候选（数量由 skills 配置的
// discoverResultLimit 决定，技能与 MCP 工具各取该上限）：
//   - Skills（本地技能库）：命中后 load_skill(name) 注入完整操作手册；
//   - MCP 工具（外部服务器）：命中即注册进本会话工具集（Materialize，
//     懒启动进程），下一轮起模型可直接按其全名调用（完整 schema 随工具
//     定义一次性下发，防"工具未被定义"的幻觉调用）。
//
// 无命中明确返回"未找到"，触发兜底链路（改需求重试 / 核心工具自行实现）。
func DiscoverTools() *Definition {
	return &Definition{
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
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
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

			env := EnvFromCtx(ctx)
			if env == nil {
				return "", errors.New("discover_tools: execution environment not initialized")
			}

			limit := 5
			if env.Skills != nil {
				limit = env.Skills.SearchLimit()
			}

			var skills []skills2.SkillSummary
			if env.Skills != nil {
				hits, err := env.Skills.Search(query, limit)
				if err != nil {
					return "", fmt.Errorf("discover_tools: %w", err)
				}
				skills = hits
			}

			var mcpHits []mcp.ToolHit
			if env.MCP != nil {
				hits, err := env.MCP.Search(query, limit)
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
					if err := env.MCP.Materialize(h); err != nil {
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
