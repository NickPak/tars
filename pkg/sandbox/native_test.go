package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func newTestNative(t *testing.T) (*NativeFs, string) {
	t.Helper()
	dir := t.TempDir()
	return NewNativeFs(dir), dir
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

	res, err := sb.Exec(ctx, ExecRequest{Command: "echo hello", Dir: dir, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if res.ExitCode != 0 || !strings.Contains(res.Output, "hello") {
		t.Errorf("unexpected result: %+v", res)
	}

	// 非零退出（exit 是三种方言共有的内建/关键字）
	res, err = sb.Exec(ctx, ExecRequest{Command: "exit 3", Dir: dir, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("Exec exit-code: %v", err)
	}
	if res.ExitCode == 0 {
		t.Error("non-zero exit should be reported")
	}
}

// Windows 装了 pwsh 时必须选 pwsh（cmd 表达能力太弱，只在无 pwsh 时回退）。
func TestNative_ShellKindPrefersPwsh(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("pwsh preference is a Windows behavior")
	}
	sb, _ := newTestNative(t)
	if PwshAvailable() {
		if got := sb.ShellKind(); got != ShellPwsh {
			t.Fatalf("ShellKind = %s, want pwsh (pwsh is installed)", got)
		}
	} else if got := sb.ShellKind(); got != ShellCmd {
		t.Fatalf("ShellKind = %s, want cmd fallback (no pwsh installed)", got)
	}
}

// pwsh 实机冒烟（仅当本机装了 pwsh）：echo 可用，且状态转储的
// KEY=VALUE 格式能被 applyState 消费的前提成立。
func TestNative_ExecPwshSmoke(t *testing.T) {
	if runtime.GOOS != "windows" || !PwshAvailable() {
		t.Skip("pwsh not installed")
	}
	sb, dir := newTestNative(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// (Get-Location).Path 输出 cwd；ForEach 拼 KEY=VALUE 环境行
	res, err := sb.Exec(ctx, ExecRequest{
		Command: "(Get-Location).Path\nGet-ChildItem Env:PATH | ForEach-Object { \"$($_.Name)=$($_.Value)\" }",
		Dir:     dir, Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("Exec via pwsh: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("pwsh exit code = %d, output: %s", res.ExitCode, res.Output)
	}
	if !strings.Contains(res.Output, "PATH=") {
		t.Errorf("env dump should be KEY=VALUE form, got: %.200s", res.Output)
	}
}

func TestNative_ExecTimeout(t *testing.T) {
	sb, dir := newTestNative(t)
	cmd := "sleep 5"
	if runtime.GOOS == "windows" { // pwsh/cmd 都能跑原生命令 ping
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

// SetRoot：仅限零消息窗口更换根目录。换根后旧根文件不可见、
// 新写入落到新根。
func TestNative_SetRoot(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	sb := NewNativeFs(dir1)

	if err := sb.WriteFile("a.txt", []byte("1")); err != nil {
		t.Fatal(err)
	}
	sb.SetRoot(dir2)
	if _, err := sb.Stat("a.txt"); err == nil {
		t.Error("after SetRoot, old root file must not be visible")
	}
	if err := sb.WriteFile("b.txt", []byte("2")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir2, "b.txt")); err != nil {
		t.Error("write should land in the new root")
	}
}

// confine 单次读根：拼接与校验同一值（原实现两次独立解析，
// 中间根被换掉时合法路径会被误判逃逸——TOCTOU）。
// 现在 root 是固定字段，窗口已被结构性消除；此测试钉住"拼接与
// 校验同根"的语义，防回归。
func TestNative_ConfineSingleRootRead(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	sb := NewNativeFs(dir1)
	if err := os.WriteFile(filepath.Join(dir1, "ok.txt"), []byte("1"), 0644); err != nil {
		t.Fatal(err)
	}

	// 模拟"校验瞬间根被换"：若在旧根下能读通，而新根下该文件不存在，
	// 说明 confine 用同一个根完成了拼接与校验（而不是拼接用旧根、
	// 校验用新根导致的误拒）。
	if _, err := sb.ReadFile("ok.txt"); err != nil {
		t.Fatalf("in-root read must pass: %v", err)
	}
	sb.SetRoot(dir2)
	if _, err := sb.ReadFile("ok.txt"); err == nil {
		t.Fatal("same path must not resolve against the new root after SetRoot")
	}
}
