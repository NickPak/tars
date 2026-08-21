package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// editFileMaxBytes caps file size before write-back to avoid accidental blowups.
const editFileMaxBytes = 1 * 1024 * 1024

type editFileArgs struct {
	Path        string `json:"path"`
	OldString   string `json:"old_string,omitempty"`
	NewString   string `json:"new_string"`
	ReplaceAll  bool   `json:"replace_all,omitempty"`
	StartMarker string `json:"start_marker,omitempty"`
	EndMarker   string `json:"end_marker,omitempty"`
}

// EditFile edits a file using content coordinates (never line numbers — they
// drift after edits and models miscount them). Two modes:
//   - exact replacement: old_string + new_string (+ optional replace_all)
//   - marker replacement: start_marker + end_marker + new_string, replacing
//     the whole span between the anchors for large block changes
//
// Matching is fail-safe: anchors must match character-for-character and be
// unique, otherwise the file is left untouched and an error is returned.
func EditFile() *Definition {
	return &Definition{
		Name: "edit_file",
		Description: "Edit a file using content coordinates (line numbers are NOT accepted — they drift after " +
			"every edit). Two modes: " +
			"(1) Exact replacement: `old_string` + `new_string` (+ optional `replace_all`) — for small changes. " +
			"(2) Marker replacement: `start_marker` + `end_marker` + `new_string` — for large block changes; give " +
			"only the first/last few anchor lines, the whole span in between (anchors included) is replaced, so " +
			"repeat the anchors in `new_string` if they should be kept. " +
			"Boundary: anchors must match the file CHARACTER-FOR-CHARACTER (including indentation and blank " +
			"lines) and be UNIQUE — on multiple matches the edit fails; then provide more surrounding context or " +
			"set replace_all=true. When matching fails the file is left untouched (fail-safe). Always read_file " +
			"the file first to copy anchors exactly; read_file's line-number prefixes are a reading aid, never " +
			"include them in anchors.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":         map[string]any{"type": "string", "description": "File path, workspace-relative or absolute"},
				"old_string":   map[string]any{"type": "string", "description": "Mode 1: exact text to replace; must occur exactly once unless replace_all=true"},
				"new_string":   map[string]any{"type": "string", "description": "Replacement text (may be empty to delete the matched text)"},
				"replace_all":  map[string]any{"type": "boolean", "description": "Mode 1 only: replace every occurrence of old_string, default false", "default": false},
				"start_marker": map[string]any{"type": "string", "description": "Mode 2: exact text marking the start of the replaced span; must be unique in the file"},
				"end_marker":   map[string]any{"type": "string", "description": "Mode 2: exact text marking the end of the replaced span; must occur exactly once after start_marker"},
			},
			"required": []string{"path", "new_string"},
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			args, err := UnmarshalArgs[editFileArgs](raw)
			if err != nil {
				return "", fmt.Errorf("invalid arguments: %w", err)
			}
			abs, err := resolveInWorkspace(args.Path, resolveWorkspaceDir(ctx))
			if err != nil {
				return "", err
			}
			data, err := os.ReadFile(abs)
			if err != nil {
				return "", err
			}
			if len(data) > editFileMaxBytes {
				return "", fmt.Errorf("file too large (%d bytes); edit in smaller pieces", len(data))
			}
			content := string(data)

			var updated, report string
			switch {
			case args.OldString != "":
				updated, report, err = replaceExact(content, args)
			case args.StartMarker != "" && args.EndMarker != "":
				updated, report, err = replaceSpan(content, args)
			case args.StartMarker != "" || args.EndMarker != "":
				err = fmt.Errorf("marker mode requires both start_marker and end_marker")
			default:
				err = fmt.Errorf("provide either old_string (exact mode) or start_marker + end_marker (marker mode)")
			}
			if err != nil {
				return "", err
			}

			if err := os.WriteFile(abs, []byte(updated), 0644); err != nil {
				return "", err
			}
			return fmt.Sprintf("edited %s: %s", args.Path, report), nil
		},
	}
}

// replaceExact implements mode 1: literal old_string -> new_string.
func replaceExact(content string, args editFileArgs) (string, string, error) {
	old, newStr, normalized := adaptLineEndings(content, args.OldString, args.NewString)

	count := strings.Count(content, old)
	if count == 0 {
		return "", "", fmt.Errorf("old_string not found; anchors must match the file character-for-character " +
			"(including indentation) — read_file the file first and copy the exact text")
	}
	if count > 1 && !args.ReplaceAll {
		return "", "", fmt.Errorf("old_string occurs %d times; make it unique with more surrounding context, "+
			"or set replace_all=true", count)
	}

	var updated string
	replaced := count
	if args.ReplaceAll {
		updated = strings.ReplaceAll(content, old, newStr)
	} else {
		updated = strings.Replace(content, old, newStr, 1)
		replaced = 1
	}
	report := fmt.Sprintf("replaced %d occurrence(s)", replaced)
	if normalized {
		report += " (LF anchors were matched against CRLF line endings)"
	}
	return updated, report, nil
}

// replaceSpan implements mode 2: replace the span from start_marker through
// end_marker (inclusive) with new_string.
func replaceSpan(content string, args editFileArgs) (string, string, error) {
	start, _, n1 := adaptLineEndings(content, args.StartMarker, "")
	end, newStr, n2 := adaptLineEndings(content, args.EndMarker, args.NewString)

	if c := strings.Count(content, start); c != 1 {
		if c == 0 {
			return "", "", fmt.Errorf("start_marker not found; anchors must match the file character-for-character " +
				"— read_file the file first and copy the exact text")
		}
		return "", "", fmt.Errorf("start_marker occurs %d times; extend it with more surrounding context", c)
	}
	startIdx := strings.Index(content, start)
	rest := content[startIdx+len(start):]
	if c := strings.Count(rest, end); c != 1 {
		if c == 0 {
			return "", "", fmt.Errorf("end_marker not found after start_marker")
		}
		return "", "", fmt.Errorf("end_marker occurs %d times after start_marker; extend it with more surrounding context", c)
	}
	endIdx := startIdx + len(start) + strings.Index(rest, end) + len(end)

	updated := content[:startIdx] + newStr + content[endIdx:]
	report := fmt.Sprintf("replaced span (%d bytes -> %d bytes)", endIdx-startIdx, len(newStr))
	if n1 || n2 {
		report += " (LF anchors were matched against CRLF line endings)"
	}
	return updated, report, nil
}

// adaptLineEndings retries LF-only anchors against CRLF content: models see
// lines without carriage returns, so a multi-line anchor built with \n cannot
// byte-match a CRLF file. This is a declared normalization (reported in the
// tool result), not a silent transform.
func adaptLineEndings(content, old, newStr string) (string, string, bool) {
	if strings.Contains(content, "\r\n") && !strings.Contains(old, "\r\n") &&
		strings.Contains(old, "\n") && !strings.Contains(content, old) {
		crlf := strings.ReplaceAll(old, "\n", "\r\n")
		if strings.Contains(content, crlf) {
			return crlf, strings.ReplaceAll(newStr, "\n", "\r\n"), true
		}
	}
	return old, newStr, false
}
