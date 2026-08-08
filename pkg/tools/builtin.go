package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// workDirKey is the context key for the per-conversation workspace directory.
// Tools read it via WorkDirFromCtx; if absent they fall back to the workDir
// passed to their constructor (the global work dir).
type workDirKey struct{}

// WithWorkDir returns a context carrying the given workspace directory.
// Pass it through the agent loop so every tool invocation resolves paths
// relative to the active conversation's workspace.
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
// File-based tools (read/edit/list/search) perform workspace boundary checks,
// rejecting paths that escape workDir via ".." or absolute paths outside the
// root, preventing the agent from reading or writing outside the workspace.
// run_command is the universal escape hatch and cannot be isolated at the path
// level; it relies on timeouts and (optionally) user confirmation.

// readFileMaxBytes caps the returned content size to avoid blowing up context.
const readFileMaxBytes = 64 * 1024

type readFileArgs struct {
	Path   string `json:"path"`
	Offset int    `json:"offset,omitempty"` // 1-based start line, 0 means from the beginning
	Limit  int    `json:"limit,omitempty"`   // max number of lines to read, 0 means unlimited
}

// ReadFile reads file content at the given path, supports line-range reads,
// and truncates output exceeding the size limit.
func ReadFile(workDir string) *Definition {
	return &Definition{
		Name:        "read_file",
		Description: "Read the content of a file at the given path. The path can be workspace-relative or absolute. For large files, use offset/limit to read a line range.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":   map[string]any{"type": "string", "description": "File path, workspace-relative or absolute"},
				"offset": map[string]any{"type": "integer", "description": "Start line number (1-based), defaults to 1"},
				"limit":  map[string]any{"type": "integer", "description": "Maximum number of lines to read, defaults to unlimited"},
			},
			"required": []string{"path"},
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			args, err := UnmarshalArgs[readFileArgs](raw)
			if err != nil {
				return "", fmt.Errorf("invalid arguments: %w", err)
			}
			wd := WorkDirFromCtx(ctx)
			if wd == "" {
				wd = workDir
			}
			abs, err := resolveInWorkspace(args.Path, wd)
			if err != nil {
				return "", err
			}
			data, err := os.ReadFile(abs)
			if err != nil {
				return "", err
			}
			content := string(data)
			if args.Offset > 0 || args.Limit > 0 {
				content = sliceLines(content, args.Offset, args.Limit)
			}
			if len(content) > readFileMaxBytes {
				return content[:readFileMaxBytes] + fmt.Sprintf("\n\n[file too large, truncated to first %d bytes]", readFileMaxBytes), nil
			}
			return content, nil
		},
	}
}

// --- search_replace ---

type searchReplaceArgs struct {
	Path        string `json:"path"`
	Search      string `json:"search"`
	Replace     string `json:"replace"`
	Regex       bool   `json:"regex,omitempty"`
	ReplaceAll  bool   `json:"replace_all,omitempty"`
}

// searchReplaceMaxBytes caps file size before write-back to avoid accidental blowups.
const searchReplaceMaxBytes = 1 * 1024 * 1024

// SearchReplace performs text or regex replacement in a file.
// By default replaces the first match only; set replace_all=true to replace all.
// For non-regex mode, search must match at least once or an error is returned.
func SearchReplace(workDir string) *Definition {
	return &Definition{
		Name:        "search_replace",
		Description: "Search and replace text in a file. Supports literal or regex matching. By default replaces only the first match; set replace_all=true to replace all occurrences. Returns an error if no match is found. Use read_file first to inspect the file content if unsure.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":        map[string]any{"type": "string", "description": "File path, workspace-relative or absolute"},
				"search":      map[string]any{"type": "string", "description": "The text to search for. If regex=true, this is a Go regex pattern."},
				"replace":     map[string]any{"type": "string", "description": "The replacement text. Use $1, $2 for regex capture groups."},
				"regex":       map[string]any{"type": "boolean", "description": "If true, treat search as a Go regex pattern. Default: false", "default": false},
				"replace_all": map[string]any{"type": "boolean", "description": "If true, replace all occurrences. Default: false (first match only)", "default": false},
			},
			"required": []string{"path", "search", "replace"},
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			args, err := UnmarshalArgs[searchReplaceArgs](raw)
			if err != nil {
				return "", fmt.Errorf("invalid arguments: %w", err)
			}
			wd := WorkDirFromCtx(ctx)
			if wd == "" {
				wd = workDir
			}
			abs, err := resolveInWorkspace(args.Path, wd)
			if err != nil {
				return "", err
			}
			data, err := os.ReadFile(abs)
			if err != nil {
				return "", err
			}
			if len(data) > searchReplaceMaxBytes {
				return "", fmt.Errorf("file too large (%d bytes); edit in chunks or use write_file", len(data))
			}
			content := string(data)

			var updated string
			var count int

			if args.Regex {
				re, err := regexp.Compile(args.Search)
				if err != nil {
					return "", fmt.Errorf("invalid regex: %w", err)
				}
				matches := re.FindAllStringIndex(content, -1)
				if len(matches) == 0 {
					return "", fmt.Errorf("regex pattern not found in %s", args.Path)
				}
				count = len(matches)
				if args.ReplaceAll {
					updated = re.ReplaceAllString(content, args.Replace)
				} else {
					// Replace only the first match: keep prefix, replace match, keep suffix
					loc := matches[0]
					updated = content[:loc[0]] + re.ReplaceAllString(content[loc[0]:loc[1]], args.Replace) + content[loc[1]:]
					count = 1
				}
			} else {
				// Literal string replacement
				count = strings.Count(content, args.Search)
				if count == 0 {
					return "", fmt.Errorf("search text not found in %s. Make sure the text matches exactly (including indentation), or read the file first with read_file", args.Path)
				}
				if args.ReplaceAll {
					updated = strings.ReplaceAll(content, args.Search, args.Replace)
				} else {
					updated = strings.Replace(content, args.Search, args.Replace, 1)
					count = 1
				}
			}

			if err := os.WriteFile(abs, []byte(updated), 0644); err != nil {
				return "", err
			}

			if args.ReplaceAll {
				return fmt.Sprintf("replaced %d occurrence(s) in %s", count, args.Path), nil
			}
			return fmt.Sprintf("replaced 1 occurrence in %s", args.Path), nil
		},
	}
}

// --- list_dir ---

type listDirArgs struct {
	Path     string `json:"path"`
	MaxDepth int    `json:"max_depth,omitempty"`
}

const listDirMaxEntries = 200

// ListDir lists the directory tree with limited depth and entry count,
// outputting an indented tree structure.
func ListDir(workDir string) *Definition {
	return &Definition{
		Name:        "list_dir",
		Description: "List the tree structure of a directory (files and subdirectories). Use this to understand project layout; do not run ls via run_command. Defaults to 2 levels deep.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":      map[string]any{"type": "string", "description": "Directory path, workspace-relative or absolute; defaults to workspace root"},
				"max_depth": map[string]any{"type": "integer", "description": "Maximum recursion depth, default 2, max 5"},
			},
			"required": []string{"path"},
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			args, err := UnmarshalArgs[listDirArgs](raw)
			if err != nil {
				return "", fmt.Errorf("invalid arguments: %w", err)
			}
			wd := WorkDirFromCtx(ctx)
			if wd == "" {
				wd = workDir
			}
			abs, err := resolveInWorkspace(args.Path, wd)
			if err != nil {
				return "", err
			}
			depth := args.MaxDepth
			if depth <= 0 {
				depth = 2
			}
			if depth > 5 {
				depth = 5
			}
			var b strings.Builder
			count := 0
			if err := walkTree(abs, "", depth, &b, &count); err != nil {
				return "", err
			}
			if count >= listDirMaxEntries {
				fmt.Fprintf(&b, "\n[too many entries, truncated to first %d]", listDirMaxEntries)
			}
			return b.String(), nil
		},
	}
}

// --- search_text ---

type searchTextArgs struct {
	Path         string `json:"path"`
	Pattern      string `json:"pattern"`
	Glob         string `json:"glob,omitempty"`
	ContextLines int    `json:"context_lines,omitempty"`
	MaxResults   int    `json:"max_results,omitempty"`
}

const searchTextMaxResults = 50

// SearchText recursively searches file contents in a directory using regex.
// Equivalent to rg/grep but does not depend on ripgrep being installed;
// output is controlled and bounded.
func SearchText(workDir string) *Definition {
	return &Definition{
		Name:        "search_text",
		Description: "Recursively search file contents in a directory using regex. Use this to find symbols, strings, or usages; do not run grep/rg via run_command. Returns matching lines with optional context.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":          map[string]any{"type": "string", "description": "Root directory to search, workspace-relative or absolute"},
				"pattern":       map[string]any{"type": "string", "description": "Regular expression pattern"},
				"glob":          map[string]any{"type": "string", "description": "File name filter, e.g. *.go; defaults to all files"},
				"context_lines": map[string]any{"type": "integer", "description": "Number of context lines to show around each match, default 0"},
				"max_results":   map[string]any{"type": "integer", "description": "Maximum number of matches, default 50"},
			},
			"required": []string{"path", "pattern"},
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			args, err := UnmarshalArgs[searchTextArgs](raw)
			if err != nil {
				return "", fmt.Errorf("invalid arguments: %w", err)
			}
			wd := WorkDirFromCtx(ctx)
			if wd == "" {
				wd = workDir
			}
			abs, err := resolveInWorkspace(args.Path, wd)
			if err != nil {
				return "", err
			}
			re, err := regexp.Compile(args.Pattern)
			if err != nil {
				return "", fmt.Errorf("invalid regex: %w", err)
			}
			max := args.MaxResults
			if max <= 0 {
				max = searchTextMaxResults
			}
			var b strings.Builder
			count := 0
			walkErr := filepath.WalkDir(abs, func(p string, d os.DirEntry, err error) error {
				if err != nil {
					return nil // skip directories without permission
				}
				if d.IsDir() {
					return nil
				}
				if args.Glob != "" {
					if ok, _ := filepath.Match(args.Glob, d.Name()); !ok {
						return nil
					}
				}
				if count >= max {
					return filepath.SkipDir
				}
				data, rerr := os.ReadFile(p)
				if rerr != nil {
					return nil
				}
				lines := strings.Split(string(data), "\n")
				rel, _ := filepath.Rel(abs, p)
				for i, line := range lines {
					if re.MatchString(line) {
						if count >= max {
							break
						}
						count++
						fmt.Fprintf(&b, "%s:%d: %s\n", rel, i+1, line)
						c := args.ContextLines
						if c > 0 {
							for j := i - c; j <= i+c; j++ {
								if j < 0 || j >= len(lines) || j == i {
									continue
								}
								fmt.Fprintf(&b, "%s:%d:   %s\n", rel, j+1, lines[j])
							}
						}
					}
				}
				return nil
			})
			if walkErr != nil {
				return "", walkErr
			}
			if count == 0 {
				return "no matches found", nil
			}
			if count >= max {
				fmt.Fprintf(&b, "\n[too many matches, truncated to first %d]", max)
			}
			return b.String(), nil
		},
	}
}

// --- run_command ---

type runCommandArgs struct {
	Command string `json:"command"`
	Timeout int    `json:"timeout,omitempty"` // seconds
}

const runCommandDefaultTimeout = 60

// RunCommand executes a shell command (supporting pipes, &&, etc.) and
// captures stdout/stderr. It is the universal escape hatch: use it for
// compiling, testing, git, and other operations that dedicated tools
// cannot perform.
func RunCommand(workDir string) *Definition {
	return &Definition{
		Name:        "run_command",
		Description: "Execute a shell command (supports pipes, &&, and other shell syntax). Use for compiling, testing, git, and other operations that dedicated tools cannot handle. Output includes both stdout and stderr. Default timeout is 60 seconds.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{"type": "string", "description": "Full command line, supports pipes and && syntax"},
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
			if timeout > 300 {
				timeout = 300
			}
			var cmd *exec.Cmd
			cctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
			defer cancel()
			if runtime.GOOS == "windows" {
				cmd = exec.CommandContext(cctx, "cmd", "/S", "/C", args.Command)
			} else {
				cmd = exec.CommandContext(cctx, "sh", "-c", args.Command)
			}
			wd := WorkDirFromCtx(ctx)
			if wd == "" {
				wd = workDir
			}
			cmd.Dir = wd
			var out bytes.Buffer
			cmd.Stdout = &out
			cmd.Stderr = &out
			runErr := cmd.Run()
			result := toUTF8(out.Bytes())
			if len(result) > readFileMaxBytes {
				// ToValidUTF8 防止截断落在多字节字符中间产生乱码
				result = strings.ToValidUTF8(result[:readFileMaxBytes], "") + fmt.Sprintf("\n\n[output too large, truncated to first %d bytes]", readFileMaxBytes)
			}
			if runErr != nil {
				if cctx.Err() == context.DeadlineExceeded {
					return result + fmt.Sprintf("\n[command timed out (%d s) and was terminated]", timeout), nil
				}
				return result + fmt.Sprintf("\n[exit: %v]", runErr), nil
			}
			return result, nil
		},
	}
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

// --- workspace path safety ---

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

// sliceLines slices content by lines: offset is 1-based, limit is the line count.
func sliceLines(content string, offset, limit int) string {
	lines := strings.Split(content, "\n")
	start := 0
	if offset > 1 {
		start = offset - 1
	}
	if start > len(lines) {
		start = len(lines)
	}
	end := len(lines)
	if limit > 0 && start+limit < end {
		end = start + limit
	}
	return strings.Join(lines[start:end], "\n")
}

// walkTree recursively outputs a directory tree.
func walkTree(dir, prefix string, depth int, b *strings.Builder, count *int) error {
	if depth < 0 || *count >= listDirMaxEntries {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for i, e := range entries {
		if *count >= listDirMaxEntries {
			return nil
		}
		*count++
		marker := "├── "
		if i == len(entries)-1 {
			marker = "└── "
		}
		name := e.Name()
		if isIgnoredEntry(name) {
			continue
		}
		fmt.Fprintf(b, "%s%s%s\n", prefix, marker, name)
		if e.IsDir() && depth > 0 {
			childPrefix := prefix + "│   "
			if i == len(entries)-1 {
				childPrefix = prefix + "    "
			}
			_ = walkTree(filepath.Join(dir, name), childPrefix, depth-1, b, count)
		}
	}
	return nil
}

// isIgnoredEntry skips common noise and version-control directories.
func isIgnoredEntry(name string) bool {
	switch name {
	case ".git", "node_modules", ".venv", "dist", "build", "__pycache__", ".idea", ".vscode":
		return true
	}
	return false
}
