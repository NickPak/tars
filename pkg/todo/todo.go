// Package todo defines the per-session TODO state machine persisted
// in the session directory (todo.json).
package todo

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

const (
	todoFile = "todo.json"
)

// TodoStatus TODO 状态枚举
const (
	TodoPending    = "pending"
	TodoInProgress = "in_progress"
	TodoCompleted  = "completed"
	TodoCancelled  = "cancelled"
)

// ValidTodoStatus 检查 status 是否是合法的 TODO 状态值。
func ValidTodoStatus(status string) bool {
	switch status {
	case TodoPending, TodoInProgress, TodoCompleted, TodoCancelled:
		return true
	default:
		return false
	}
}

// Todo 是 TODO 列表中的一项。
type Todo struct {
	ID      string `json:"id"`
	Content string `json:"content"`
	Status  string `json:"status"`
}

// TodoStore 是 per-session 的 TODO 状态机。
// 设计文档 2.10：模型调 todo_write → 框架校验并更新状态机（持久化到
// workspace 文件，跨会话存活）→ 返回确认 → StatusBar 渲染 todo 区。
//
// 线程安全：工具可能并行执行（虽然 todo_write 通常独占一轮），用 RWMutex 保护。
type TodoStore struct {
	mu       sync.RWMutex
	todos    []Todo
	filePath string
	version  int64 // 每次 Replace 自增，供 StatusBar 检测变更与"未更新轮数"
}

// NewTodoStore 创建一个以 baseDir 为会话目录的 TodoStore。
// todo.json 持久化文件在 baseDir 下。传空串则纯内存模式（不持久化）。
// 调用方应在会话恢复时调用 Load() 读回磁盘状态。
func NewTodoStore(baseDir string) *TodoStore {
	filePath := ""
	if baseDir != "" {
		filePath = filepath.Join(baseDir, todoFile)
	}
	return &TodoStore{filePath: filePath}
}

// Load 从磁盘读取 TODO 状态。文件不存在时静默返回 nil（新会话）。
func (s *TodoStore) Load() error {
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("todo: load: %w", err)
	}
	var todos []Todo
	if err := json.Unmarshal(data, &todos); err != nil {
		return fmt.Errorf("todo: parse: %w", err)
	}
	s.mu.Lock()
	s.todos = todos
	s.version = 0 // 新加载，StatusBar 首次 Render 会记录基准
	s.mu.Unlock()
	return nil
}

// Replace 全量覆写 TODO 列表（todo_write 的语义：原子、无部分更新 bug），
// 并立即持久化到磁盘。
func (s *TodoStore) Replace(todos []Todo) error {
	s.mu.Lock()
	s.todos = todos
	s.version++
	s.mu.Unlock()

	return s.save()
}

func (s *TodoStore) save() error {
	if s.filePath == "" {
		return nil // 纯内存模式（测试用）
	}
	s.mu.RLock()
	todos := s.todos
	s.mu.RUnlock()

	data, err := json.MarshalIndent(todos, "", "  ")
	if err != nil {
		return fmt.Errorf("todo: marshal: %w", err)
	}
	return os.WriteFile(s.filePath, data, 0644)
}

// Snapshot 返回当前 TODO 列表的副本和版本号。
// StatusBar 在 Render 时调用此方法渲染 todo 区并检测"未更新轮数"。
func (s *TodoStore) Snapshot() ([]Todo, int64) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Todo, len(s.todos))
	copy(out, s.todos)
	return out, s.version
}
