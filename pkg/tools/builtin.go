// Package tools 的工具注册采用两层模型：
//
//   - 包级 builtins 目录：进程级、全局、只读（init 完成后不再修改），
//     登记"有哪些内置工具可用"；条目是工厂（workDir 在 init 时不可得，
//     实例化推迟到 Registry 创建时）。
//   - 每会话 Registry（见 registry.go）：创建时把内置工具实例化并拷贝
//     进自己的 map——会话级视图，可独立增删工具而不影响其他会话。
//
// 本文件同时承载内置工具的共享工具函数（路径解析/输出截断/编码转换）。
package tools

import (
	"cmp"
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// Built-in tools. Each constructor returns a *Definition ready for Register.
//
// File-based tools (read/write/edit/glob/grep) perform workspace boundary
// checks, rejecting paths that escape workDir via ".." or absolute paths
// outside the root, preventing the agent from reading or writing outside the
// workspace. run_command and code_interpreter are universal escape hatches
// that cannot be isolated at the path level; they rely on timeouts (OS-level
// sandboxing is planned, see the design plan stage 4).

// maxOutputBytes caps a single tool output to avoid blowing up the context.
const maxOutputBytes = 64 * 1024

// resolveWorkDir 从 ctx 中的会话级执行环境（Env）取工作目录；
// 无 Env（如测试直调 Handler）返回空串——调用方须容忍（拼装路径前校验）。
func resolveWorkDir(ctx context.Context) string {
	if env := EnvFromCtx(ctx); env != nil {
		return env.WorkDir
	}
	return ""
}

// resolveInWorkspace resolves path to an absolute path within the workspace,
// rejecting escapes.
func resolveInWorkspace(path, workDir string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("path is required")
	}
	abs := path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(workDir, abs)
	}
	abs = filepath.Clean(abs)
	root, err := filepath.Abs(workDir)
	if err != nil {
		return "", err
	}
	root = filepath.Clean(root)
	if !strings.HasPrefix(abs+string(filepath.Separator), root+string(filepath.Separator)) && abs != root {
		return "", fmt.Errorf("path %q escapes workspace root %q", path, root)
	}
	return abs, nil
}

// truncateOutput caps s at maxOutputBytes with an explicit notice; truncation
// is never silent. ToValidUTF8 keeps the cut from landing mid-rune.
func truncateOutput(s string) string {
	if len(s) <= maxOutputBytes {
		return s
	}
	return strings.ToValidUTF8(s[:maxOutputBytes], "") + fmt.Sprintf("\n\n[output too large, truncated to first %d bytes]", maxOutputBytes)
}

// toUTF8 converts command output to valid UTF-8. Windows console builtin
// commands (dir, type, etc.) emit GBK on Chinese systems (code page 936),
// which would otherwise render as mojibake in the frontend and LLM context.
// Pure ASCII/UTF-8 output (e.g. from `go run`) passes through unchanged.
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

// isIgnoredEntry skips common noise and version-control directories when
// walking trees (glob_files/grep_files).
func isIgnoredEntry(name string) bool {
	switch name {
	case ".git", "node_modules", ".venv", "dist", "build", "__pycache__", ".idea", ".vscode":
		return true
	}
	return false
}

// looksBinary sniffs the first bytes for a NUL, the cheapest reliable
// binary indicator.
func looksBinary(data []byte) bool {
	const sniff = 512
	n := min(len(data), sniff)
	for i := 0; i < n; i++ {
		if data[i] == 0 {
			return true
		}
	}
	return false
}

// builtins 是包级工具目录：进程级、全局、只读（init 完成后不再修改）。
// 内置工具通过 init() 调用 RegisterBuiltin 自动注册到目录。
// 所有 Definition 都是纯声明（schema + handler），运行时状态一律经
// Env 读取——实例可安全共享给全部会话的 Registry。
var builtins = map[string]*Definition{}

// RegisterBuiltin 注册一个内置工具；空名、nil 定义或重复名都是
// 组装期编程错误，直接 panic。
func RegisterBuiltin(name string, def *Definition) {
	if name == "" || def == nil {
		panic("tools: RegisterBuiltin with empty name or nil definition")
	}
	if _, exists := builtins[name]; exists {
		panic("tools: RegisterBuiltin duplicate " + name)
	}
	builtins[name] = def
}

// BuiltinNames 返回内置工具的名称列表（字典序），供系统提示词展示。
func BuiltinNames() []string {
	names := make([]string, 0, len(builtins))
	for name := range builtins {
		names = append(names, name)
	}
	slices.SortFunc(names, cmp.Compare)
	return names
}

// 内置工具集合在此集中登记。
func init() {
	RegisterBuiltin("code_interpreter", CodeInterpreter())
	RegisterBuiltin("run_command", RunCommand())
	RegisterBuiltin("read_file", ReadFile())
	RegisterBuiltin("write_file", WriteFile())
	RegisterBuiltin("edit_file", EditFile())
	RegisterBuiltin("glob_files", GlobFiles())
	RegisterBuiltin("grep_files", GrepFiles())
	RegisterBuiltin("todo_write", TodoWrite())
	RegisterBuiltin("ask_user", AskUser())
	RegisterBuiltin("load_skill", LoadSkill())
	RegisterBuiltin("discover_tools", DiscoverTools())
}
