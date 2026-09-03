package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"tars/pkg/schema"
)

type fakeWorkspace struct{ dir string }

func (f *fakeWorkspace) GetWorkspaceDir() string { return f.dir }

func writeAgents(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, AgentsFile), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestRenderMemoryBlock_MissingOrEmpty(t *testing.T) {
	rt := NewRuntime(&fakeWorkspace{dir: t.TempDir()})
	if msg := rt.RenderMemoryBlock(); msg != nil {
		t.Errorf("missing AGENTS.md should yield nil, got %v", msg)
	}

	dir := t.TempDir()
	writeAgents(t, dir, "  \n\n")
	if msg := NewRuntime(&fakeWorkspace{dir}).RenderMemoryBlock(); msg != nil {
		t.Error("blank AGENTS.md should yield nil")
	}
}

func TestRenderMemoryBlock_Content(t *testing.T) {
	dir := t.TempDir()
	writeAgents(t, dir, "# Rules\n\nuse pnpm\n")
	msg := NewRuntime(&fakeWorkspace{dir}).RenderMemoryBlock()
	if msg == nil {
		t.Fatal("expected memory block")
	}
	if msg.Role != schema.RoleUser {
		t.Errorf("memory block must be a user-role message (low-authority position), got %s", msg.Role)
	}
	if !strings.Contains(msg.Content, `<project_memory source="AGENTS.md">`) ||
		!strings.Contains(msg.Content, "use pnpm") {
		t.Errorf("unexpected content: %q", msg.Content)
	}
}

// 工作区切换（SetWorkspaceDir 零消息窗口）：每轮现读，下一轮即生效，
// 无需任何失效通知。
func TestRenderMemoryBlock_WorkspaceSwitch(t *testing.T) {
	ws := &fakeWorkspace{dir: t.TempDir()}
	rt := NewRuntime(ws)
	if rt.RenderMemoryBlock() != nil {
		t.Fatal("no AGENTS.md yet")
	}
	dir2 := t.TempDir()
	writeAgents(t, dir2, "rules v2")
	ws.dir = dir2
	msg := rt.RenderMemoryBlock()
	if msg == nil || !strings.Contains(msg.Content, "rules v2") {
		t.Error("workspace switch should be picked up on next render")
	}
}

func TestRenderMemoryBlock_Truncation(t *testing.T) {
	dir := t.TempDir()
	// 多字节字符填充超上限：验证 UTF-8 安全截断（不切断 rune）
	writeAgents(t, dir, strings.Repeat("规", DefaultMaxBytes))
	msg := NewRuntime(&fakeWorkspace{dir}).RenderMemoryBlock()
	if msg == nil || !strings.Contains(msg.Content, "已截断") {
		t.Fatal("oversized AGENTS.md should be truncated with read_file pointer")
	}
	if !utf8.ValidString(msg.Content) {
		t.Error("truncated content must stay valid UTF-8")
	}
}
