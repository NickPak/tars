package toolkit

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"tars/pkg/tool/kernel"

	"tars/pkg/sandbox"
)

const grepFilesMaxResults = 50

type grepFilesArgs struct {
	Pattern string `json:"pattern"`
	Include string `json:"include,omitempty"`
	Path    string `json:"path,omitempty"`
}

// GrepFiles searches file CONTENTS with a regex and returns matching lines
// with line numbers. File-name search belongs to glob_files; the two tool
// descriptions reference each other as boundary counter-examples, per the
// design plan.
func (f *FileTools) GrepFiles() *kernel.Definition {
	return &kernel.Definition{
		Name: "grep_files",
		Description: "Search file CONTENTS with a regular expression, returning matching lines as " +
			"`path:line: content`. Boundary: searches content, not file names — use glob_files to find files " +
			"by name. Results are capped; on truncation an explicit notice is shown. Binary files and " +
			"dependency/VCS directories (.git, node_modules, ...) are skipped.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{"type": "string", "description": "Regular expression (Go syntax), e.g. `func\\s+Handle`"},
				"include": map[string]any{"type": "string", "description": "Optional file-name filter, e.g. `*.go`; defaults to all files"},
				"path":    map[string]any{"type": "string", "description": "Root directory to search, workspace-relative or absolute; defaults to the workspace root"},
			},
			"required": []string{"pattern"},
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			args, err := UnmarshalArgs[grepFilesArgs](raw)
			if err != nil {
				return "", fmt.Errorf("invalid arguments: %w", err)
			}
			re, err := regexp.Compile(args.Pattern)
			if err != nil {
				return "", fmt.Errorf("invalid regex: %w", err)
			}
			fs, err := f.fileSystem()
			if err != nil {
				return "", err
			}
			root := args.Path
			if root == "" {
				root = "."
			}

			var b strings.Builder
			count := 0
			truncated := false
			WalkDir(fs, root, func(rel string, e sandbox.DirEntry) bool {
				if args.Include != "" {
					if ok, _ := filepath.Match(args.Include, e.Name); !ok {
						return true
					}
				}
				data, rerr := fs.ReadFile(root + "/" + rel)
				if rerr != nil || LooksBinary(data) {
					return true
				}
				lines := strings.Split(string(data), "\n")
				for i, line := range lines {
					if !re.MatchString(line) {
						continue
					}
					if count >= grepFilesMaxResults {
						truncated = true
						return false
					}
					count++
					fmt.Fprintf(&b, "%s:%d: %s\n", RelToOS(rel), i+1, strings.TrimSuffix(line, "\r"))
				}
				return true
			})
			if count == 0 {
				return "no matches found", nil
			}
			out := strings.TrimRight(b.String(), "\n")
			if truncated {
				out += fmt.Sprintf("\n\n[too many matches, truncated to first %d]", grepFilesMaxResults)
			}
			return out, nil
		},
	}
}
