package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// NativeFs 是本机执行环境：文件面直用 os + 前缀边界检查，
// 命令面直用 os/exec。根目录在构造时固定（已 Abs+Clean），
// 仅在会话零消息窗口内经 SetRoot 更换（该窗口保证没有任何轮
// 在运行，无并发读写，故不加锁——见 workspaceservice 两道守卫）。
type NativeFs struct {
	root string
}

// NewNativeFs 创建本机执行环境。root 为空时按空根处理（此时相对
// 路径即进程相对路径）——装配层须保证传已解析的绝对路径。
func NewNativeFs(root string) *NativeFs {
	n := &NativeFs{}
	n.SetRoot(root)
	return n
}

// SetRoot 更换工作根（Abs+Clean 在此做一次）。仅限会话零消息窗口
// 调用；有消息后工作目录已锁定，调用方（session.Manager.SetWorkspaceDir）
// 负责保证。
func (n *NativeFs) SetRoot(dir string) {
	if dir == "" {
		n.root = ""
		return
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		n.root = dir
		return
	}
	n.root = filepath.Clean(abs)
}

func (n *NativeFs) Startup() error {
	return nil
}

func (n *NativeFs) Shutdown() error {
	return nil
}

func (n *NativeFs) Close() error { return nil } // 无资源

// Root 返回 workspace 根的本机绝对路径。
func (n *NativeFs) Root() string {
	return n.root
}

// confine 把工具视角路径解析为根内绝对路径，拒绝逃逸。
// 根读一次：拼接与校验同一值，不存在两次解析之间根被换掉的窗口。
func (n *NativeFs) confine(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("path is required")
	}
	root := n.root
	abs := path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(root, abs)
	}
	abs = filepath.Clean(abs)
	if !strings.HasPrefix(abs+string(filepath.Separator), root+string(filepath.Separator)) && abs != root {
		return "", fmt.Errorf("path %q escapes workspace root %q", path, root)
	}
	return abs, nil
}

func (n *NativeFs) ReadFile(path string) ([]byte, error) {
	abs, err := n.confine(path)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(abs)
}

func (n *NativeFs) WriteFile(path string, data []byte) error {
	abs, err := n.confine(path)
	if err != nil {
		return err
	}
	return os.WriteFile(abs, data, 0644)
}

func (n *NativeFs) MkdirAll(path string) error {
	abs, err := n.confine(path)
	if err != nil {
		return err
	}
	return os.MkdirAll(abs, 0755)
}

func (n *NativeFs) ReadDir(path string) ([]DirEntry, error) {
	abs, err := n.confine(path)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return nil, err
	}
	out := make([]DirEntry, 0, len(entries))
	for _, e := range entries {
		var size int64
		if info, ierr := e.Info(); ierr == nil {
			size = info.Size()
		}
		out = append(out, DirEntry{Name: e.Name(), IsDir: e.IsDir(), Size: size})
	}
	return out, nil
}

func (n *NativeFs) Stat(path string) (FileInfo, error) {
	abs, err := n.confine(path)
	if err != nil {
		return FileInfo{}, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return FileInfo{}, err
	}
	return FileInfo{IsDir: info.IsDir(), Size: info.Size()}, nil
}

func (n *NativeFs) Remove(path string) error {
	abs, err := n.confine(path)
	if err != nil {
		return err
	}
	return os.Remove(abs)
}

// pwshAvailable 探测 PATH 里是否有 PowerShell 7+（pwsh）。结果进程内
// 缓存——用户装了/卸了 pwsh 需要重启才生效，与 python/shell 探测同策略。
var pwshAvailable = sync.OnceValue(func() bool {
	_, err := exec.LookPath("pwsh")
	return err == nil
})

// PwshAvailable 报告本机是否安装了 PowerShell 7+（状态栏展示用）。
func PwshAvailable() bool { return pwshAvailable() }

// ShellKind 返回本机 shell 方言：Windows 装了 pwsh 用 pwsh，否则 cmd；
// 其他平台为 sh。
func (n *NativeFs) ShellKind() ShellKind {
	if runtime.GOOS == "windows" {
		if pwshAvailable() {
			return ShellPwsh
		}
		return ShellCmd
	}
	return ShellSh
}

// Exec 以本机 shell 执行命令。Command 由调用方（Shell 载体）按
// ShellKind 拼装好（含持久终端的状态转储段）；本方法只负责起进程、
// 收输出、限时。
func (n *NativeFs) Exec(ctx context.Context, req ExecRequest) (*ExecResult, error) {
	cctx := ctx
	cancel := func() {}
	if req.Timeout > 0 {
		cctx, cancel = context.WithTimeout(ctx, req.Timeout)
	}
	defer cancel()

	var cmd *exec.Cmd
	switch n.ShellKind() {
	case ShellCmd:
		cmd = exec.CommandContext(cctx, "cmd", "/S", "/C", req.Command)
	case ShellPwsh:
		// -NoProfile：不加载用户 profile（启动快、行为确定，避免 profile
		// 输出污染状态转储解析）；-NonInteractive：防交互式提示挂住进程。
		cmd = exec.CommandContext(cctx, "pwsh", "-NoProfile", "-NonInteractive", "-Command", req.Command)
	default:
		cmd = exec.CommandContext(cctx, string(ShellSh), "-c", req.Command)
	}
	if req.Dir != "" {
		cmd.Dir = req.Dir
	} else {
		cmd.Dir = n.root
	}
	if req.Env != nil {
		cmd.Env = req.Env
	}
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	runErr := cmd.Run()
	res := &ExecResult{Output: toUTF8(out.Bytes())}
	if runErr == nil {
		return res, nil
	}
	if cctx.Err() == context.DeadlineExceeded {
		res.TimedOut = true
		return res, nil
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		res.ExitCode = exitErr.ExitCode()
		return res, nil
	}
	// 进程未能启动（如 shell 缺失）
	return nil, runErr
}

// toUTF8 把命令输出转换为合法 UTF-8。Windows 控制台内建命令在中文系统
// （代码页 936）输出 GBK，直接回填会在前端与模型上下文里变成乱码；
// 纯 ASCII/UTF-8 输出（如 go run）原样通过。
func toUTF8(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	decoded, err := simplifiedchinese.GBK.NewDecoder().Bytes(b)
	if err != nil {
		return string(b)
	}
	return string(decoded)
}
