package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

const globFilesMaxResults = 200

type globFilesArgs struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path,omitempty"`
}

// GlobFiles finds files by name pattern, supporting `**` for any-depth
// matching. It matches path names only — content search belongs to grep_files.
func GlobFiles() *Definition {
	return &Definition{
		Name: "glob_files",
		Description: "Find files by NAME pattern, e.g. `**/*.py` or `src/**/*.ts`; a pattern without a path " +
			"separator (e.g. `*.go`) matches at any depth. Returns matching paths relative to the search root, " +
			"sorted; results are capped and truncation is announced explicitly. Boundary: matches path names " +
			"only, NOT file contents — use grep_files to search inside files.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{"type": "string", "description": "Glob pattern, e.g. `**/*.py`; `**` matches any number of directories"},
				"path":    map[string]any{"type": "string", "description": "Root directory to search, workspace-relative or absolute; defaults to the workspace root"},
			},
			"required": []string{"pattern"},
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			args, err := UnmarshalArgs[globFilesArgs](raw)
			if err != nil {
				return "", fmt.Errorf("invalid arguments: %w", err)
			}
			if strings.TrimSpace(args.Pattern) == "" {
				return "", fmt.Errorf("pattern is required")
			}
			root := args.Path
			if root == "" {
				root = "."
			}
			abs, err := resolveInWorkspace(root, resolveWorkspaceDir(ctx))
			if err != nil {
				return "", err
			}
			pattern := filepath.ToSlash(args.Pattern)
			if !strings.Contains(pattern, "/") {
				pattern = "**/" + pattern
			}

			var matches []string
			truncated := false
			walkErr := filepath.WalkDir(abs, func(p string, d fs.DirEntry, err error) error {
				if err != nil {
					return nil // skip unreadable entries
				}
				if d.IsDir() {
					if p != abs && isIgnoredEntry(d.Name()) {
						return filepath.SkipDir
					}
					return nil
				}
				rel, err := filepath.Rel(abs, p)
				if err != nil {
					return nil
				}
				if matchGlob(pattern, filepath.ToSlash(rel)) {
					if len(matches) >= globFilesMaxResults {
						truncated = true
						return filepath.SkipAll
					}
					matches = append(matches, rel)
				}
				return nil
			})
			if walkErr != nil {
				return "", walkErr
			}
			if len(matches) == 0 {
				return fmt.Sprintf("no files matched pattern %q", args.Pattern), nil
			}
			sort.Strings(matches)
			out := strings.Join(matches, "\n")
			if truncated {
				out += fmt.Sprintf("\n\n[too many results, truncated to first %d]", globFilesMaxResults)
			}
			return out, nil
		},
	}
}

// matchGlob matches a slash-separated path against a glob pattern where `**`
// matches zero or more path segments and `*`/`?` match within one segment.
func matchGlob(pattern, name string) bool {
	return matchGlobSegments(strings.Split(pattern, "/"), strings.Split(name, "/"))
}

func matchGlobSegments(pat, segs []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			pat = pat[1:]
			if len(pat) == 0 {
				return true
			}
			for i := 0; i <= len(segs); i++ {
				if matchGlobSegments(pat, segs[i:]) {
					return true
				}
			}
			return false
		}
		if len(segs) == 0 {
			return false
		}
		ok, err := path.Match(pat[0], segs[0])
		if err != nil || !ok {
			return false
		}
		pat = pat[1:]
		segs = segs[1:]
	}
	return len(segs) == 0
}
