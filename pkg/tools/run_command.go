package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

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
func RunCommand(workDir string) *Definition {
	// Sessions are keyed by workspace directory: each conversation gets its
	// own persistent terminal state.
	var sessions sync.Map

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

			wd := resolveWorkDir(ctx, workDir)
			v, _ := sessions.LoadOrStore(wd, &termSession{cwd: wd, env: os.Environ()})
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
				cmd = exec.CommandContext(cctx, "sh", "-c", full)
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
