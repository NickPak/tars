package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testWorkspace 是测试用的 WorkspaceProvider。
type testWorkspace struct {
	dir string
}

func (w *testWorkspace) GetWorkspaceDir() string { return w.dir }

func newTestNative(t *testing.T) (*NativeFs, string) {
	t.Helper()
	dir := t.TempDir()
	return NewNativeFs(&testWorkspace{dir: dir}), dir
}

// 边界约束是 native 实现的安全底线：逃逸路径一律拒绝。
func TestNative_ConfineRejectsEscape(t *testing.T) {
	sb, dir := newTestNative(t)

	for _, p := range []string{"../outside.txt", "../../etc/passwd", filepath.Join(dir, "..", "out.txt")} {
		if _, err := sb.ReadFile(p); err == nil {
			t.Errorf("ReadFile(%q) should be rejected", p)
		}
		if err := sb.WriteFile(p, []byte("x")); err == nil {
			t.Errorf("WriteFile(%q) should be rejected", p)
		}
		if _, err := sb.ReadDir(p); err == nil {
			t.Errorf("ReadDir(%q) should be rejected", p)
		}
		if _, err := sb.Stat(p); err == nil {
			t.Errorf("Stat(%q) should be rejected", p)
		}
		if err := sb.Remove(p); err == nil {
			t.Errorf("Remove(%q) should be rejected", p)
		}
	}

	// 根内路径（相对/绝对）放行
	inside := filepath.Join(dir, "ok.txt")
	if err := sb.WriteFile("ok.txt", []byte("hi")); err != nil {
		t.Fatalf("WriteFile relative: %v", err)
	}
	if _, err := sb.Stat(inside); err != nil {
		t.Fatalf("Stat absolute-inside-root: %v", err)
	}
	data, err := sb.ReadFile("ok.txt")
	if err != nil || string(data) != "hi" {
		t.Fatalf("ReadFile: %v %q", err, data)
	}
}

func TestNative_ExecAndShellKind(t *testing.T) {
	sb, dir := newTestNative(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var res *ExecResult
	var err error
	if sb.ShellKind() == ShellCmd {
		res, err = sb.Exec(ctx, ExecRequest{Command: "echo hello", Timeout: 5 * time.Second})
	} else {
		res, err = sb.Exec(ctx, ExecRequest{Command: "echo hello", Dir: dir, Timeout: 5 * time.Second})
	}
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if res.ExitCode != 0 || !strings.Contains(res.Output, "hello") {
		t.Errorf("unexpected result: %+v", res)
	}

	// 非零退出
	res, err = sb.Exec(ctx, ExecRequest{Command: "exit 3", Dir: dir, Timeout: 5 * time.Second})
	if sb.ShellKind() == ShellCmd {
		res, err = sb.Exec(ctx, ExecRequest{Command: "cmd /C exit 3", Dir: dir, Timeout: 5 * time.Second})
	}
	if err != nil {
		t.Fatalf("Exec exit-code: %v", err)
	}
	if res.ExitCode == 0 {
		t.Error("non-zero exit should be reported")
	}
}

func TestNative_ExecTimeout(t *testing.T) {
	sb, dir := newTestNative(t)
	cmd := "sleep 5"
	if sb.ShellKind() == ShellCmd {
		cmd = "ping -n 6 127.0.0.1 >nul"
	}
	res, err := sb.Exec(context.Background(), ExecRequest{Command: cmd, Dir: dir, Timeout: time.Second})
	if err != nil {
		t.Fatalf("Exec timeout should not error: %v", err)
	}
	if !res.TimedOut {
		t.Error("expected TimedOut=true")
	}
}

// 工作目录经 Provider 每次调用解析：模拟会话中改 workDir。
func TestNative_DynamicRoot(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	ws := &testWorkspace{dir: dir1}
	sb := NewNativeFs(ws)

	if err := sb.WriteFile("a.txt", []byte("1")); err != nil {
		t.Fatal(err)
	}
	ws.dir = dir2 // 用户改了 workDir
	if _, err := sb.Stat("a.txt"); err == nil {
		t.Error("after workDir change, old root file must not be visible")
	}
	if err := sb.WriteFile("b.txt", []byte("2")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir2, "b.txt")); err != nil {
		t.Error("write should land in the new root")
	}
}
