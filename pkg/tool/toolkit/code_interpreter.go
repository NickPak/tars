package toolkit

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"tars/pkg/tool/kernel"
	"time"

	"tars/pkg/sandbox"
)

type codeInterpreterArgs struct {
	Code    string `json:"code"`
	Timeout int    `json:"timeout,omitempty"` // seconds
}

const (
	codeInterpreterDefaultTimeout = 60
	codeInterpreterMaxTimeout     = 300
	// codeInterpreterTempDir 是临时脚本的存放目录（workspace 相对路径）。
	// 脚本必须落在 workspace 内：docker/vm 等实现里只有挂载进环境的
	// 文件才对命令面可见。
	codeInterpreterTempDir = ".tars/tmp"
)

// codeInterpreterRiskRules 针对 Python 代码的明显破坏性调用模式。
// 与工具实现同文件声明（内聚），由策略层通用引擎评估。
var codeInterpreterRiskRules = []kernel.RiskRule{
	{ID: "py-rmtree", Reason: "递归删除目录（shutil.rmtree）", ArgsKey: "code", Pattern: regexp.MustCompile(`\bshutil\.rmtree\s*\(`)},
	{ID: "py-remove", Reason: "删除文件（os.remove/os.unlink）", ArgsKey: "code", Pattern: regexp.MustCompile(`\bos\.(remove|unlink)\s*\(`)},
	{ID: "py-shell-out", Reason: "Python 内执行 shell（os.system/subprocess）", ArgsKey: "code", Pattern: regexp.MustCompile(`\bos\.system\s*\(|\bsubprocess\.`)},
}

// CodeInterpreter 是 code_interpreter 工具的载体（Carrier）：持有组合
// 执行环境（sandbox.Sandbox），脚本经文件面写入、命令面执行。
// 临时脚本名经计数器生成（无随机依赖），Close 无资源可清。
type CodeInterpreter struct {
	sb      sandbox.SandboxProvider
	counter uint64 // 临时脚本序号（会话内唯一即可）
	python  string // 探测缓存的解释器名（环境内不变）
	mu      sync.Mutex
}

// NewCodeInterpreter 创建 code_interpreter 载体。sb 为 nil 时 handler 报错
// （装配层须保证注入）。
func NewCodeInterpreter(sb sandbox.SandboxProvider) *CodeInterpreter {
	return &CodeInterpreter{sb: sb}
}

// Definitions 实现 tool.Carrier。
func (c *CodeInterpreter) Definitions() []*kernel.Definition {
	return []*kernel.Definition{c.definition()}
}

// Close 实现 tool.Carrier：无自有资源（执行环境归装配层回收）。
func (c *CodeInterpreter) Close() error { return nil }

// CodeInterpreter executes Python 3 code with the system interpreter,
// capturing stdout/stderr with an explicit timeout. OS-level sandboxing
// (no network, workspace-only files) is planned in design plan stage 4;
// until then it runs with the application's permissions and the description
// says so (fidelity red line: the description must match reality).
func (c *CodeInterpreter) definition() *kernel.Definition {
	return &kernel.Definition{
		Name:      "code_interpreter",
		RiskRules: codeInterpreterRiskRules,
		Description: "Execute Python 3 code with the system interpreter and return stdout/stderr; non-zero " +
			"exits and timeouts are reported explicitly. Use for computation, data analysis, batch text/file " +
			"processing, or orchestrating multi-step work in ONE program — intermediate results stay in " +
			"variables, print only what matters. Scientific libraries (numpy/pandas/sympy) are available when " +
			"installed in the environment. Boundary: runs with the desktop app's permissions (no sandbox or " +
			"network isolation yet) — do not perform destructive system operations; for plain single-file " +
			"reads/writes prefer read_file/write_file. Long output is truncated with an explicit notice. " +
			"Default timeout 60s, max 300s.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"code":    map[string]any{"type": "string", "description": "Python 3 source code to execute"},
				"timeout": map[string]any{"type": "integer", "description": "Timeout in seconds, default 60, max 300"},
			},
			"required": []string{"code"},
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			args, err := UnmarshalArgs[codeInterpreterArgs](raw)
			if err != nil {
				return "", fmt.Errorf("invalid arguments: %w", err)
			}
			if strings.TrimSpace(args.Code) == "" {
				return "", fmt.Errorf("code is required")
			}
			if c.sb == nil {
				return "", fmt.Errorf("no execution environment configured")
			}
			python, err := c.lookupPython(ctx)
			if err != nil {
				return "", err
			}
			timeout := args.Timeout
			if timeout <= 0 {
				timeout = codeInterpreterDefaultTimeout
			}
			timeout = min(timeout, codeInterpreterMaxTimeout)

			// 临时脚本经文件面写入 workspace（避免 shell 引用地狱；
			// 且只有环境内可见的文件，命令面才能执行）。
			rel := c.nextTempScript()
			if err := c.sb.MkdirAll(codeInterpreterTempDir); err != nil {
				return "", err
			}
			if err := c.sb.WriteFile(rel, []byte(args.Code)); err != nil {
				return "", err
			}
			defer c.sb.Remove(rel)

			// rel 由本载体生成（.tars/tmp/tars-code-N.py），无空格无引号，
			// 无需 shell 转义。
			res, err := c.sb.Exec(ctx, sandbox.ExecRequest{
				Command: python + " -u " + rel,
				Dir:     c.sb.Root(),
				Timeout: time.Duration(timeout) * time.Second,
			})
			if err != nil {
				return "", fmt.Errorf("code_interpreter: %w", err)
			}

			result := TruncateOutput(strings.TrimRight(res.Output, "\r\n"))
			if res.TimedOut {
				return result + fmt.Sprintf("\n[execution timed out (%d s) and was terminated]", timeout), nil
			}
			if res.ExitCode != 0 {
				return result + fmt.Sprintf("\n[exit: %d]", res.ExitCode), nil
			}
			if result == "" {
				return "(no output)", nil
			}
			return result, nil
		},
	}
}

// nextTempScript 生成会话内唯一的临时脚本路径（workspace 相对）。
func (c *CodeInterpreter) nextTempScript() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.counter++
	return fmt.Sprintf("%s/tars-code-%d.py", codeInterpreterTempDir, c.counter)
}

// lookupPython 经执行环境探测 Python 3 解释器：`python` 优先
// （Windows 标准），`python3` 兜底（macOS/Linux 标准）。
// 结果缓存于载体（环境的解释器不会中途变更）。
func (c *CodeInterpreter) lookupPython(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.python != "" {
		return c.python, nil
	}
	for _, name := range []string{"python", "python3"} {
		probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		res, err := c.sb.Exec(probeCtx, sandbox.ExecRequest{
			Command: name + " --version",
			Dir:     c.sb.Root(),
			Timeout: 5 * time.Second,
		})
		cancel()
		if err == nil && res.ExitCode == 0 {
			c.python = name
			return name, nil
		}
	}
	return "", fmt.Errorf("no Python interpreter found in PATH (tried `python` and `python3`)")
}

// lookupPython finds a Python 3 interpreter on the HOST: `python` first
// (standard on Windows), then `python3` (standard on macOS/Linux).
// 仅服务于状态栏 env 区（宿主信息展示）；code_interpreter 的探测走
// 执行环境（sandbox.Executor），两者在 native 下同源。
func lookupPython() (string, error) {
	for _, name := range []string{"python", "python3"} {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("no Python interpreter found in PATH (tried `python` and `python3`)")
}

// pythonVersionCached 缓存 Python 版本（进程内不变，查询需 fork 子进程）。
var pythonVersionCached = sync.OnceValue(func() string {
	python, err := lookupPython()
	if err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, python, "--version").Output()
	if err != nil {
		return ""
	}
	// "Python 3.12.1" → "3.12.1"
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(out)), "Python"))
})

// PythonVersion returns the system Python version (e.g. "3.12.1"), or ""
// when no interpreter is available. Used by the agent status bar (env zone).
func PythonVersion() string {
	return pythonVersionCached()
}
