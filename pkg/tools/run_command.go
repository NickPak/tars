package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// shellInfoCached 缓存 ShellInfo 的结果：shell 软链在进程生命周期内不会变，
// 没必要每轮迭代重新解析。
var shellInfoCached = sync.OnceValue(func() string {
	if runtime.GOOS == "windows" {
		return "cmd"
	}
	return resolveShell(shellName)
})

// osInfoCached 缓存 OS 描述（GOOS/GOARCH 进程内不变）。
var osInfoCached = sync.OnceValue(func() string {
	return runtime.GOOS + "/" + runtime.GOARCH
})

// ShellInfo 返回 run_command 实际使用的 shell 描述（状态栏 env 区用）。
// Windows 返回 "cmd"；其他平台解析 `sh` 软链指向的真实二进制
// （bash/dash/zsh 等），让模型知道能安全使用哪种 shell 语法：
//   - "sh (bash)"  → 可用 bash 扩展语法
//   - "sh (dash)"  → 仅 POSIX sh 语法
//   - "sh"         → 无法解析，保守按 POSIX 处理
func ShellInfo() string {
	return shellInfoCached()
}

// OSInfo 返回 "windows/amd64" 形式的系统+架构描述（状态栏 env 区用）。
func OSInfo() string {
	return osInfoCached()
}

// resolveShell 解析 shellPath 软链指向的真实二进制名。
func resolveShell(shellPath string) string {
	p, err := exec.LookPath(shellPath)
	if err != nil {
		return shellName // PATH 里找不到 sh（极端情况），返回字面值
	}
	// 跟踪符号链接到真实二进制（macOS: /bin/sh → /bin/bash；Debian: /bin/sh → dash）
	realPath, err := filepath.EvalSymlinks(p)
	if err != nil {
		return shellName
	}
	base := filepath.Base(realPath)
	// 去掉版本后缀（bash5 → bash）
	base = strings.TrimSuffix(base, filepath.Ext(base))
	if base == shellName || base == "" {
		return shellName // sh 指向 sh 自身（非软链），无法进一步解析
	}
	return shellName + " (" + base + ")"
}

type runCommandArgs struct {
	Command string `json:"command"`
	Timeout int    `json:"timeout,omitempty"` // seconds
}

const (
	runCommandDefaultTimeout = 60
	runCommandMaxTimeout     = 300
	// sessionStateMarker separates user output from the session-state dump
	// (cwd + environment) appended after every command. Uniqueness keeps
	// accidental collisions with user output vanishingly unlikely.
	sessionStateMarker = "__TARS_SESSION_STATE_9f3a1c__"

	// shellName 是 run_command 实际使用的 shell：Windows 用 cmd，其他平台用 sh。
	// 状态栏通过 ShellInfo() 读取同一来源，保证"告诉模型的"与"实际执行的"一致。
	shellName = "sh"
)

// termSession is the persistent terminal state of one workspace: the working
// directory and full environment snapshot, carried over between calls so the
// model does not repeat `cd` or activation commands. The state is re-captured
// after every command (cd/pwd + env dump), so `cd`, `set`/`export`, venv
// activation etc. all persist.
type termSession struct {
	mu  sync.Mutex
	cwd string
	env []string
}

// applyState parses the dump printed after sessionStateMarker: first
// non-empty line is the cwd, the rest are KEY=VALUE environment lines.
// A failed parse leaves the previous state untouched (conservative).
func (s *termSession) applyState(dump string) {
	lines := strings.Split(dump, "\n")
	idx := -1
	for i, ln := range lines {
		if strings.TrimSpace(ln) != "" {
			idx = i
			break
		}
	}
	if idx < 0 {
		return
	}
	cwd := strings.TrimRight(lines[idx], "\r")
	env := make([]string, 0, len(lines))
	for _, ln := range lines[idx+1:] {
		ln = strings.TrimRight(ln, "\r")
		if strings.Contains(ln, "=") {
			env = append(env, ln)
		}
	}
	if len(env) == 0 {
		return
	}
	s.cwd = cwd
	s.env = env
}

// RunCommand executes a shell command in a persistent terminal session
// (working directory and environment preserved across calls), capturing
// stdout/stderr with an explicit timeout.
func RunCommand() *Definition {
	return &Definition{
		Name: "run_command",
		Description: "Execute a shell command in a PERSISTENT terminal session — the working directory and " +
			"environment variables carry over between calls, so do NOT repeat `cd` or environment-activation " +
			"commands from earlier calls. Supports pipes, && and other shell syntax. Use for compiling, " +
			"testing, git, package managers, and anything the dedicated file tools cannot do. Returns stdout " +
			"and stderr together; non-zero exits and timeouts are reported explicitly, and long output is " +
			"truncated with a notice. Default timeout 60s, max 300s.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{"type": "string", "description": "Full command line, supports pipes and && syntax, e.g. `go build ./... && go test ./pkg/...`"},
				"timeout": map[string]any{"type": "integer", "description": "Timeout in seconds, default 60, max 300"},
			},
			"required": []string{"command"},
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			args, err := UnmarshalArgs[runCommandArgs](raw)
			if err != nil {
				return "", fmt.Errorf("invalid arguments: %w", err)
			}
			if strings.TrimSpace(args.Command) == "" {
				return "", fmt.Errorf("command is required")
			}
			timeout := args.Timeout
			if timeout <= 0 {
				timeout = runCommandDefaultTimeout
			}
			timeout = min(timeout, runCommandMaxTimeout)

			env := EnvFromCtx(ctx)
			if env == nil {
				return "", fmt.Errorf("run_command: no execution env")
			}
			wd := resolveWorkDir(ctx)
			// 持久终端状态按工作目录键控，存于会话级 Env（跨轮共享）。
			v, _ := env.TermSessions.LoadOrStore(wd, &termSession{cwd: wd, env: os.Environ()})
			sess := v.(*termSession)
			sess.mu.Lock()
			defer sess.mu.Unlock()

			cctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
			defer cancel()

			var cmd *exec.Cmd
			if runtime.GOOS == "windows" {
				full := args.Command + " & echo " + sessionStateMarker + " & cd & set"
				cmd = exec.CommandContext(cctx, "cmd", "/S", "/C", full)
			} else {
				full := args.Command + "\necho " + sessionStateMarker + "\npwd\nenv"
				cmd = exec.CommandContext(cctx, shellName, "-c", full)
			}
			cmd.Dir = sess.cwd
			cmd.Env = sess.env
			var out bytes.Buffer
			cmd.Stdout = &out
			cmd.Stderr = &out
			runErr := cmd.Run()

			result := toUTF8(out.Bytes())
			userOut := result
			if idx := strings.LastIndex(result, sessionStateMarker); idx >= 0 {
				userOut = result[:idx]
				sess.applyState(result[idx+len(sessionStateMarker):])
			}
			userOut = truncateOutput(strings.TrimRight(userOut, "\r\n"))

			if runErr != nil {
				if cctx.Err() == context.DeadlineExceeded {
					return userOut + fmt.Sprintf("\n[command timed out (%d s) and was terminated; session state left unchanged]", timeout), nil
				}
				return userOut + fmt.Sprintf("\n[exit: %v]", runErr), nil
			}
			if userOut == "" {
				return "(no output)", nil
			}
			return userOut, nil
		},
	}
}
