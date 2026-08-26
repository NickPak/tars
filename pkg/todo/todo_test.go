package todo

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTodoStore_LoadMissingFile(t *testing.T) {
	s := NewManager("/nonexistent")
	if err := s.Load(); err != nil {
		t.Fatalf("Load on missing file should not error: %v", err)
	}
	todos, _ := s.Snapshot()
	if len(todos) != 0 {
		t.Errorf("expected empty, got %d", len(todos))
	}
}

func TestTodoStore_PersistAndReload(t *testing.T) {
	dir := t.TempDir()

	s1 := NewManager(dir)
	todos := []Todo{
		{ID: "1", Content: "Step one", Status: TodoCompleted},
		{ID: "2", Content: "Step two", Status: TodoInProgress},
		{ID: "3", Content: "Step three", Status: TodoPending},
	}
	if err := s1.Replace(todos); err != nil {
		t.Fatalf("Replace: %v", err)
	}

	// 文件已写入磁盘
	if _, err := os.Stat(filepath.Join(dir, "todo.json")); err != nil {
		t.Fatalf("todo file not created: %v", err)
	}

	// 新实例加载
	s2 := NewManager(dir)
	if err := s2.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	loaded, _ := s2.Snapshot()
	if len(loaded) != 3 {
		t.Fatalf("expected 3 todos, got %d", len(loaded))
	}
	if loaded[1].Status != TodoInProgress {
		t.Errorf("expected in_progress, got %s", loaded[1].Status)
	}
}

func TestTodoStore_VersionIncrementsOnReplace(t *testing.T) {
	s := NewManager("")
	_, v0 := s.Snapshot()

	s.Replace([]Todo{{ID: "1", Content: "A", Status: TodoPending}})
	_, v1 := s.Snapshot()
	if v1 <= v0 {
		t.Errorf("version should increment: %d -> %d", v0, v1)
	}

	s.Replace([]Todo{{ID: "1", Content: "A", Status: TodoCompleted}})
	_, v2 := s.Snapshot()
	if v2 <= v1 {
		t.Errorf("version should increment: %d -> %d", v1, v2)
	}
}

func TestTodoStore_SnapshotIsCopy(t *testing.T) {
	s := NewManager("")
	s.Replace([]Todo{{ID: "1", Content: "A", Status: TodoPending}})

	snap, _ := s.Snapshot()
	snap[0].Status = TodoCompleted

	// 修改副本不应影响原数据
	again, _ := s.Snapshot()
	if again[0].Status != TodoPending {
		t.Errorf("snapshot should be a copy, original mutated")
	}
}

func TestTodoStore_EmptyFilePathSkipsPersist(t *testing.T) {
	s := NewManager("")
	if err := s.Replace([]Todo{{ID: "1", Content: "A", Status: TodoPending}}); err != nil {
		t.Fatalf("Replace with empty path should not error: %v", err)
	}
	todos, _ := s.Snapshot()
	if len(todos) != 1 {
		t.Errorf("expected 1 todo in memory, got %d", len(todos))
	}
}

func TestValidTodoStatus(t *testing.T) {
	valid := []string{TodoPending, TodoInProgress, TodoCompleted, TodoCancelled}
	for _, s := range valid {
		if !ValidTodoStatus(s) {
			t.Errorf("%q should be valid", s)
		}
	}
	invalid := []string{"done", "", "PENDING", "todo"}
	for _, s := range invalid {
		if ValidTodoStatus(s) {
			t.Errorf("%q should be invalid", s)
		}
	}
}
