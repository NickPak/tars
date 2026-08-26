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
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

type WorkspaceProvider interface {
	GetWorkspaceDir() string
}

// NativeFs 是本机执行环境：文件面直用 os + 前缀边界检查，
// 命令面直用 os/exec。rootFn 在每次操作时解析当前工作目录
// （用户可在会话存活期间改 workDir，改后下一次调用即生效）；
// nil 时按空根处理（此时相对路径即进程相对路径）。
type NativeFs struct {
	workspacePv WorkspaceProvider
}

// NewNativeFs 创建本机执行环境。rootFn 语义：每次调用解析 workspace 根。
func NewNativeFs(workspacePv WorkspaceProvider) *NativeFs {
	return &NativeFs{workspacePv: workspacePv}
}

func (n *NativeFs) Startup() error {
	return nil
}

func (n *NativeFs) Shutdown() error {
	return nil
}

func (n *NativeFs) Close() error { return nil } // 无资源

// root 解析当前 workspace 根。
func (n *NativeFs) root() string {
	return n.workspacePv.GetWorkspaceDir()
}

// Root 返回 workspace 根的本机绝对路径。
func (n *NativeFs) Root() string {
	root, err := filepath.Abs(n.root())
	if err != nil {
		return n.root()
	}
	return filepath.Clean(root)
}

// confine 把工具视角路径解析为根内绝对路径，拒绝逃逸。
func (n *NativeFs) confine(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("path is required")
	}
	abs := path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(n.root(), abs)
	}
	abs = filepath.Clean(abs)
	root := n.Root()
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

// ShellKind 返回本机 shell 方言：Windows 为 cmd，其他为 sh。
func (n *NativeFs) ShellKind() ShellKind {
	if runtime.GOOS == "windows" {
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
	if n.ShellKind() == ShellCmd {
		cmd = exec.CommandContext(cctx, "cmd", "/S", "/C", req.Command)
	} else {
		cmd = exec.CommandContext(cctx, string(ShellSh), "-c", req.Command)
	}
	if req.Dir != "" {
		cmd.Dir = req.Dir
	} else {
		cmd.Dir = n.root()
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
