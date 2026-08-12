package tools

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// workDirKey is the context key for the per-session workspace directory.
// Tools read it via WorkDirFromCtx; if absent they fall back to the workDir
// passed to their constructor (the global work dir).
type workDirKey struct{}

// WithWorkDir returns a context carrying the given workspace directory.
// Pass it through the agent loop so every tool invocation resolves paths
// relative to the active session's workspace.
func WithWorkDir(ctx context.Context, dir string) context.Context {
	return context.WithValue(ctx, workDirKey{}, dir)
}

// WorkDirFromCtx extracts the workspace directory from ctx.
// Returns "" if not set (caller should fall back to a default).
func WorkDirFromCtx(ctx context.Context) string {
	if dir, ok := ctx.Value(workDirKey{}).(string); ok {
		return dir
	}
	return ""
}

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

// resolveWorkDir picks the per-session workspace from ctx, falling back
// to the global workDir passed to the tool constructor.
func resolveWorkDir(ctx context.Context, fallback string) string {
	if wd := WorkDirFromCtx(ctx); wd != "" {
		return wd
	}
	return fallback
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
