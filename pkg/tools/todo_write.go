package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"tars/pkg/store"
)

type todoItem struct {
	ID      string `json:"id"`
	Content string `json:"content"`
	Status  string `json:"status"`
}

type todoWriteArgs struct {
	Todos []todoItem `json:"todos"`
}

// TodoWrite 返回 todo_write 工具定义。
//
// 设计文档 2.10：全量覆写 TODO 列表（Claude Code TodoWrite 同款语义）。
// 每次传入完整列表，原子更新，无部分更新 bug。
// "复述计划"本身刷新注意力（Manus 的"通过复述操纵注意力"）。
//
// 数据流：模型调 todo_write([...]) → 框架校验并更新 TODO 状态机
//   → 持久化到 workspace 文件（跨会话存活）
//   → 返回确认 → 下一次注入状态栏时渲染 todo 区
//
// 状态由本工具显式推进，框架不替模型改 TODO。状态栏 todo 区只是其投影。
func TodoWrite() *Definition {
	return &Definition{
		Name: "todo_write",
		Description: "Create or update the task TODO list. Pass the FULL list every time — this is an " +
			"atomic overwrite, not a partial update. Use it when a task breaks down into multiple " +
			"steps: create the list upfront, then update item statuses as you complete each step. " +
			"The framework renders the current list in the <agent_status> bar each turn, so you " +
			"always see up-to-date progress without re-reading it from history. " +
			"Status must be one of: pending, in_progress, completed, cancelled. " +
			"Keep at most one item in_progress at a time.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"todos": map[string]any{
					"type": "array",
					"description": "Complete TODO list. Every call replaces the entire list.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"id":      map[string]any{"type": "string", "description": "Stable identifier for this item (used across updates)"},
							"content": map[string]any{"type": "string", "description": "Short description of the task step"},
							"status":  map[string]any{"type": "string", "enum": []string{"pending", "in_progress", "completed", "cancelled"}},
						},
						"required": []string{"id", "content", "status"},
					},
				},
			},
			"required": []string{"todos"},
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			args, err := UnmarshalArgs[todoWriteArgs](raw)
			if err != nil {
				return "", fmt.Errorf("invalid arguments: %w", err)
			}

			todoStore := store.TodoStoreFromCtx(ctx)
			if todoStore == nil {
				return "", fmt.Errorf("todo_write: no TodoStore in context")
			}

			// 校验并转换
			todos := make([]store.Todo, len(args.Todos))
			for i, item := range args.Todos {
				if item.ID == "" {
					return "", fmt.Errorf("todos[%d]: id is required", i)
				}
				if item.Content == "" {
					return "", fmt.Errorf("todos[%d]: content is required", i)
				}
				if !store.ValidTodoStatus(item.Status) {
					return "", fmt.Errorf("todos[%d]: invalid status %q (must be pending/in_progress/completed/cancelled)", i, item.Status)
				}
				todos[i] = store.Todo{
					ID:      item.ID,
					Content: strings.TrimSpace(item.Content),
					Status:  item.Status,
				}
			}

			if err := todoStore.Replace(todos); err != nil {
				return "", fmt.Errorf("todo_write: persist failed: %w", err)
			}

			// 返回简洁确认——完整列表由状态栏渲染，不在此重复
			return summarize(todos), nil
		},
	}
}

// summarize 生成一行摘要确认，避免与状态栏的 todo 区重复。
func summarize(todos []store.Todo) string {
	counts := map[string]int{}
	for _, t := range todos {
		counts[t.Status]++
	}
	var parts []string
	if n := counts[store.TodoInProgress]; n > 0 {
		parts = append(parts, fmt.Sprintf("%d in_progress", n))
	}
	if n := counts[store.TodoPending]; n > 0 {
		parts = append(parts, fmt.Sprintf("%d pending", n))
	}
	if n := counts[store.TodoCompleted]; n > 0 {
		parts = append(parts, fmt.Sprintf("%d completed", n))
	}
	if n := counts[store.TodoCancelled]; n > 0 {
		parts = append(parts, fmt.Sprintf("%d cancelled", n))
	}
	return fmt.Sprintf("TODO list updated: %d items (%s)", len(todos), strings.Join(parts, ", "))
}
