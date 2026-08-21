package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type codeInterpreterArgs struct {
	Code    string `json:"code"`
	Timeout int    `json:"timeout,omitempty"` // seconds
}

const (
	codeInterpreterDefaultTimeout = 60
	codeInterpreterMaxTimeout     = 300
)

// CodeInterpreter executes Python 3 code with the system interpreter,
// capturing stdout/stderr with an explicit timeout. OS-level sandboxing
// (no network, workspace-only files) is planned in design plan stage 4;
// until then it runs with the application's permissions and the description
// says so (fidelity red line: the description must match reality).
func CodeInterpreter() *Definition {
	return &Definition{
		Name: "code_interpreter",
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
			python, err := lookupPython()
			if err != nil {
				return "", err
			}
			timeout := args.Timeout
			if timeout <= 0 {
				timeout = codeInterpreterDefaultTimeout
			}
			timeout = min(timeout, codeInterpreterMaxTimeout)

			// A temp file avoids all shell-quoting pitfalls of `python -c`.
			tmp, err := os.CreateTemp("", "tars-code-*.py")
			if err != nil {
				return "", err
			}
			defer os.Remove(tmp.Name())
			if _, err := tmp.WriteString(args.Code); err != nil {
				tmp.Close()
				return "", err
			}
			if err := tmp.Close(); err != nil {
				return "", err
			}

			cctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
			defer cancel()
			cmd := exec.CommandContext(cctx, python, "-u", tmp.Name())
			cmd.Dir = resolveWorkspaceDir(ctx)
			var out bytes.Buffer
			cmd.Stdout = &out
			cmd.Stderr = &out
			runErr := cmd.Run()

			result := truncateOutput(strings.TrimRight(toUTF8(out.Bytes()), "\r\n"))
			if runErr != nil {
				if cctx.Err() == context.DeadlineExceeded {
					return result + fmt.Sprintf("\n[execution timed out (%d s) and was terminated]", timeout), nil
				}
				return result + fmt.Sprintf("\n[exit: %v]", runErr), nil
			}
			if result == "" {
				return "(no output)", nil
			}
			return result, nil
		},
	}
}

// lookupPython finds a Python 3 interpreter: `python` first (standard on
// Windows), then `python3` (standard on macOS/Linux).
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
