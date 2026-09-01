package toolkit

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"tars/pkg/tool/kernel"
	"time"

	"tars/pkg/sandbox"
)

// shellInfoCached 缓存 ShellInfo 的结果：shell 软链与 pwsh 安装状态在
// 进程生命周期内不会变，没必要每轮迭代重新解析。
var shellInfoCached = sync.OnceValue(func() string {
	if runtime.GOOS == "windows" {
		if v := pwshVersionCached(); v != "" {
			return "pwsh (" + v + ")"
		}
		return "cmd"
	}
	return resolveShell(shellName)
})

// pwshVersionCached 缓存 pwsh 版本（探测一次，fork 子进程）。
// 未安装返回空串，ShellInfo 据此回退 "cmd"。
var pwshVersionCached = sync.OnceValue(func() string {
	if !sandbox.PwshAvailable() {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "pwsh", "--version").Output()
	if err != nil {
		return ""
	}
	// "PowerShell 7.4.6" → "7.4.6"
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(out)), "PowerShell"))
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

// stateDumpSuffix 按 shell 方言拼装状态转储段：marker 之后先打印 cwd，
// 再打印 KEY=VALUE 环境行（applyState 按此格式解析）。
//
// pwsh 的两个坑：Get-ChildItem Env: 默认输出是两列展示（Name Value），
// 不是 KEY=VALUE，必须用 ForEach-Object 拼；echo 是 Write-Output 别名，
// 可直接用。
func stateDumpSuffix(kind sandbox.ShellKind) string {
	switch kind {
	case sandbox.ShellCmd:
		return " & echo " + sessionStateMarker + " & cd & set"
	case sandbox.ShellPwsh:
		return "\necho " + sessionStateMarker +
			"\n(Get-Location).Path" +
			"\nGet-ChildItem Env: | ForEach-Object { \"$($_.Name)=$($_.Value)\" }"
	default: // ShellSh
		return "\necho " + sessionStateMarker + "\npwd\nenv"
	}
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

// Shell 是 run_command 的载体（Carrier）：持有执行环境的命令面
// （sandbox.Executor）与本会话的持久终端状态。
//
// 终端状态是单个 termSession：工作根恒定后（sandbox 根只在会话零消息
// 窗口内可变），不存在"按 root 分桶、换根另开终端"的需求——那是
// 动态切换工作目录时代的遗留。状态随会话注册表同生命周期，进程退出
// 自然回收。
type Shell struct {
	exec   sandbox.Executor
	spill  *OutputSpill
	term   *termSession // 懒初始化（首次执行时）
	termMu sync.Mutex   // 仅保护 term 的初始化
}

// NewShell 创建命令执行工具载体。exec 为执行环境的命令面
// （native/docker/vm）；nil 时 handler 报错（装配层须保证注入）。
// archive 为超长输出的落盘通道；nil 时退化为原地截断。
func NewShell(exec sandbox.Executor, archive ArchiveProvider) *Shell {
	return &Shell{exec: exec, spill: NewOutputSpill(archive)}
}

// Definitions 实现 tool.Carrier。
func (s *Shell) Definitions() []*kernel.Definition {
	return []*kernel.Definition{s.RunCommand()}
}

// Close 实现 tool.Carrier：命令均为短命进程（调用即起、超时即杀），
// 无残留资源可清；termSession 只是 cwd/env 快照。
func (s *Shell) Close() error { return nil }

// executor 返回命令面；nil 时返回错误（供 handler 开头调用）。
func (s *Shell) executor() (sandbox.Executor, error) {
	if s.exec == nil {
		return nil, fmt.Errorf("no execution environment configured")
	}
	return s.exec, nil
}

// session 返回本会话唯一的持久终端状态，首次调用时以当前工作根初始化。
// 多个 run_command 可能并行（工具并行执行），故初始化加锁；运行期状态
// 一致性由 termSession.mu 保证（调用方持锁）。
func (s *Shell) session(root string) *termSession {
	s.termMu.Lock()
	defer s.termMu.Unlock()
	if s.term == nil {
		s.term = &termSession{cwd: root, env: os.Environ()}
	}
	return s.term
}

// runCommandRiskRules 针对 shell 命令文本的危险模式（Windows cmd 与
// POSIX sh 兼顾）。规则与工具实现同文件声明——危险性与工具定义内聚，
// 由策略层（pkg/tool/guard）的通用引擎评估。
// 注意分隔符截断用 [^|;&]：命中 `cmd1 && dangerous` 链式写法中的危险段。
var runCommandRiskRules = []kernel.RiskRule{
	// rm 后紧跟的纯字母旗标串中任一含 r（-r/-rf/-fr/--recursive，含分离写法
	// rm -r -f）；旗标串之外的 -word（如文件名 my-report.txt）不误判
	{ID: "rm-recursive", Reason: "递归删除（rm -r / rm -rf）", ArgsKey: "command", Pattern: regexp.MustCompile(`(?i)\brm\b(?:\s+-{1,2}[a-z]+)*\s+-{1,2}[a-z]*r[a-z]*`)},
	{ID: "win-recursive-delete", Reason: "递归删除（del/rmdir /s、Remove-Item -Recurse、robocopy /MIR）", ArgsKey: "command", Pattern: regexp.MustCompile(`(?i)\b(?:del|erase|rmdir|rd)\b[^|;&]*\s/s\b|\bRemove-Item\b[^|;&]*-r(ecurse)?\b|\brobocopy\b[^|;&]*/mir\b`)},
	{ID: "dd-disk-write", Reason: "直接写块设备（dd）", ArgsKey: "command", Pattern: regexp.MustCompile(`(?i)\bdd\s+[^|;&]*\bif=`)},
	{ID: "mkfs-format", Reason: "格式化/创建文件系统", ArgsKey: "command", Pattern: regexp.MustCompile(`(?i)\bmkfs\b|\bformat\s+[a-z]:`)},
	{ID: "pipe-to-shell", Reason: "下载并直接执行远程脚本（curl|sh）", ArgsKey: "command", Pattern: regexp.MustCompile(`(?i)\b(curl|wget)\b[^|;]*\|\s*(sudo\s+)?(bash|sh|zsh)\b`)},
	{ID: "git-force-push", Reason: "强制推送（git push --force）", ArgsKey: "command", Pattern: regexp.MustCompile(`(?i)\bgit\s+push\b[^|;&]*(\s-f\b|--force)`)},
	{ID: "git-reset-hard", Reason: "硬重置丢弃改动（git reset --hard）", ArgsKey: "command", Pattern: regexp.MustCompile(`(?i)\bgit\s+reset\s+--hard\b`)},
	{ID: "shutdown-reboot", Reason: "关机/重启", ArgsKey: "command", Pattern: regexp.MustCompile(`(?i)\bshutdown\b|\breboot\b`)},
}

// RunCommand executes a shell command in a persistent terminal session
// (working directory and environment preserved across calls), capturing
// stdout/stderr with an explicit timeout.
func (s *Shell) RunCommand() *kernel.Definition {
	return &kernel.Definition{
		Name:      "run_command",
		RiskRules: runCommandRiskRules,
		Description: "Execute a shell command in a PERSISTENT terminal session — the working directory and " +
			"environment variables carry over between calls, so do NOT repeat `cd` or environment-activation " +
			"commands from earlier calls. Supports pipes, && and other shell syntax. Use for compiling, " +
			"testing, git, package managers, and anything the dedicated file tools cannot do. Returns stdout " +
			"and stderr together; non-zero exits and timeouts are reported explicitly. Long output keeps its " +
			"head and tail with the omitted amount stated in the middle, and the complete output is written to " +
			"an `archive://...` file that read_file can open — so nothing is lost. Default timeout 60s, max 300s.",
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

			execEnv, err := s.executor()
			if err != nil {
				return "", err
			}
			sess := s.session(execEnv.Root())
			sess.mu.Lock()
			defer sess.mu.Unlock()

			// 状态转储段按执行环境的 shell 方言拼装（详见 termSession 注释）。
			full := args.Command + stateDumpSuffix(execEnv.ShellKind())

			res, err := execEnv.Exec(ctx, sandbox.ExecRequest{
				Command: full,
				Dir:     sess.cwd,
				Env:     sess.env,
				Timeout: time.Duration(timeout) * time.Second,
			})
			if err != nil {
				return "", fmt.Errorf("run_command: %w", err)
			}

			result := res.Output
			userOut := result
			if idx := strings.LastIndex(result, sessionStateMarker); idx >= 0 {
				userOut = result[:idx]
				sess.applyState(result[idx+len(sessionStateMarker):])
			}
			userOut = s.spill.Apply("run_command", strings.TrimRight(userOut, "\r\n"))

			if res.TimedOut {
				return userOut + fmt.Sprintf("\n[command timed out (%d s) and was terminated; session state left unchanged]", timeout), nil
			}
			if res.ExitCode != 0 {
				return userOut + fmt.Sprintf("\n[exit: %d]", res.ExitCode), nil
			}
			if userOut == "" {
				return "(no output)", nil
			}
			return userOut, nil
		},
	}
}
