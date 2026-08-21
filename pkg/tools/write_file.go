package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type writeFileArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// WriteFile creates a new file or fully overwrites an existing one, creating
// parent directories as needed. Partial edits are delegated to edit_file.
func WriteFile() *Definition {
	return &Definition{
		Name: "write_file",
		Description: "Create a new file or COMPLETELY OVERWRITE an existing one; parent directories are " +
			"created automatically. Boundary: for partial modifications of an existing file use edit_file — " +
			"do not rewrite the whole file via write_file.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":    map[string]any{"type": "string", "description": "File path, workspace-relative or absolute, e.g. `src/util/helper.go`"},
				"content": map[string]any{"type": "string", "description": "Full content to write"},
			},
			"required": []string{"path", "content"},
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			args, err := UnmarshalArgs[writeFileArgs](raw)
			if err != nil {
				return "", fmt.Errorf("invalid arguments: %w", err)
			}
			abs, err := resolveInWorkspace(args.Path, resolveWorkspaceDir(ctx))
			if err != nil {
				return "", err
			}
			if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
				return "", err
			}
			if err := os.WriteFile(abs, []byte(args.Content), 0644); err != nil {
				return "", err
			}
			lines := 0
			if args.Content != "" {
				lines = strings.Count(args.Content, "\n") + 1
			}
			return fmt.Sprintf("wrote %s (%d bytes, %d lines)", args.Path, len(args.Content), lines), nil
		},
	}
}
