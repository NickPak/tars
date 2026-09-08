package toolkit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"tars/pkg/memory"
	"tars/pkg/tool"
)

// RecallTool 是 recall 工具的载体（Carrier）：持有记忆检索能力
// （memory.Provider）。无外部资源，Close 为空方法。
type RecallTool struct {
	mp memory.Provider
}

// NewRecallTool 创建 recall 载体。
func NewRecallTool(mp memory.Provider) *RecallTool {
	return &RecallTool{mp: mp}
}

// Definitions 实现 tool.Carrier。
func (t *RecallTool) Definitions() []*tool.Definition {
	return []*tool.Definition{t.definition()}
}

// Close 实现 tool.Carrier：无资源。
func (t *RecallTool) Close() error { return nil }

type recallArgs struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

// definition 返回 recall 工具定义。
func (t *RecallTool) definition() *tool.Definition {
	return &tool.Definition{
		Name: "recall",
		Description: "Search cross-session memory by keyword and return full facts. The injected memory index " +
			"only shows one line per fact (and may be truncated) — use recall when you need the full text, or " +
			"when answering \"what did the user say about X before\". Searches both project and global memory " +
			"(expired facts excluded).",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Keyword(s) to match against fact subjects and bodies (case-insensitive substring)",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Max results (default 5)",
				},
			},
			"required": []string{"query"},
		},
		Handler: func(_ context.Context, raw json.RawMessage) (string, error) {
			args, err := UnmarshalArgs[recallArgs](raw)
			if err != nil {
				return "", fmt.Errorf("invalid arguments: %w", err)
			}
			if t.mp == nil {
				return "", errors.New("recall requires a memory runtime; none available")
			}
			facts, err := t.mp.Recall(args.Query, args.Limit)
			if err != nil {
				return "", err
			}
			if len(facts) == 0 {
				return fmt.Sprintf("no memories matched %q", args.Query), nil
			}
			var b strings.Builder
			fmt.Fprintf(&b, "%d memor", len(facts))
			if len(facts) > 1 {
				b.WriteString("ies")
			} else {
				b.WriteString("y")
			}
			fmt.Fprintf(&b, " matched %q:\n\n", args.Query)
			for _, f := range facts {
				fmt.Fprintf(&b, "## [%s] %s（%s，来源 %s）\n%s\n\n",
					f.Type, f.Subject, f.LastConfirmed, f.WrittenBy, f.Body)
			}
			return b.String(), nil
		},
	}
}
