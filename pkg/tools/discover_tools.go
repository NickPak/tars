package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ============================================================================
// discover_tools 工具（plan 3.1 发现元工具）
//
// 按自然语言能力需求检索可用能力，返回少量候选（数量由 skills 配置的
// discoverResultLimit 决定）。当前只对 Skills 检索（MCP 未接入；接入后
// 同一通道扩展 source 级→item 级两级路由）。
// 命中返回候选的 name + description，提示用 load_skill 加载；
// 无命中明确返回"未找到"，触发兜底链路（改需求重试 / 核心工具自行实现）。
// ============================================================================

// DiscoverTools 返回 discover_tools 工具。
func DiscoverTools() *Definition {
	return &Definition{
		Name: "discover_tools",
		Description: "Search installed skills by natural-language need. Returns the top candidate skills " +
			"(name + description + category) matching the query. Use when you face a task that the core " +
			"tools and already-loaded skills don't cover — describe the capability you need and this will " +
			"find relevant skills. After finding one, call load_skill(name) to get its full instructions. " +
			"Boundary: if it returns \"no match\", rewrite the query once; if still nothing, implement the " +
			"capability yourself with the core tools or by writing code.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Natural-language description of the capability you need, e.g. \"create a PowerPoint\"",
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
				return "", errors.New("query is required")
			}

			env := EnvFromCtx(ctx)
			if env == nil || env.Skills == nil {
				return "", errors.New("discover_tools requires a skill runtime; none available")
			}
			rt := env.Skills

			hits, err := rt.Search(query, rt.SearchLimit())
			if err != nil {
				return "", err
			}
			if len(hits) == 0 {
				return "No matching skills found. Rephrase the query and retry; if there is still no match, implement it with the core tools or write code on the fly.", nil
			}

			var b strings.Builder
			b.WriteString(fmt.Sprintf("Found %d candidate skills:\n", len(hits)))
			for i, h := range hits {
				fmt.Fprintf(&b, "%d. %s — %s", i+1, h.Name, h.Description)
				if h.Category != "" {
					fmt.Fprintf(&b, " [%s]", h.Category)
				}
				b.WriteString("\n")
			}
			b.WriteString("\nCall load_skill(name) on the skill you need to get its full playbook.")
			return b.String(), nil
		},
	}
}
