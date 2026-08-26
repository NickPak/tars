package toolkit

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"tars/pkg/tool/kernel"
)

type readFileArgs struct {
	Path   string `json:"path"`
	Offset int    `json:"offset,omitempty"` // 1-based start line, 0 means from the beginning
	Limit  int    `json:"limit,omitempty"`  // max number of lines to read, 0 means up to the size cap
}

// ReadFile reads a text file with line-number prefixes and explicit range
// notices, per the design plan: large files must be read in segments via
// offset/limit, and truncation is always announced ("showing lines X-Y of N").
func (f *FileTools) ReadFile() *kernel.Definition {
	return &kernel.Definition{
		Name: "read_file",
		Description: "Read the content of a text file. Every line is returned with a line-number prefix " +
			"(`<number><TAB>content`) as a reading aid — the prefix is NOT part of the file and must never be " +
			"copied into edit_file anchors. Boundary: large files MUST be read in segments via offset/limit; " +
			"whenever the output is partial, an explicit notice states the shown range and the total line count. " +
			"Directories and binary files are rejected.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":   map[string]any{"type": "string", "description": "File path, workspace-relative or absolute, e.g. `src/main.go`"},
				"offset": map[string]any{"type": "integer", "description": "1-based line number to start reading from, default 1"},
				"limit":  map[string]any{"type": "integer", "description": "Maximum number of lines to read, default: as many as fit the output size cap"},
			},
			"required": []string{"path"},
		},
		Handler: func(_ context.Context, raw json.RawMessage) (string, error) {
			args, err := UnmarshalArgs[readFileArgs](raw)
			if err != nil {
				return "", fmt.Errorf("invalid arguments: %w", err)
			}
			fs, err := f.fileSystem()
			if err != nil {
				return "", err
			}
			info, err := fs.Stat(args.Path)
			if err != nil {
				return "", err
			}
			if info.IsDir {
				return "", fmt.Errorf("%s is a directory; use glob_files to list files", args.Path)
			}
			data, err := fs.ReadFile(args.Path)
			if err != nil {
				return "", err
			}
			if len(data) == 0 {
				return "(empty file)", nil
			}
			if LooksBinary(data) {
				return "", fmt.Errorf("%s appears to be a binary file; read_file only handles text", args.Path)
			}

			lines := strings.Split(string(data), "\n")
			total := len(lines)
			start := 0
			if args.Offset > 1 {
				start = args.Offset - 1
			}
			if start > total {
				start = total
			}
			end := total
			if args.Limit > 0 && start+args.Limit < end {
				end = start + args.Limit
			}

			var b strings.Builder
			last := start
			for i := start; i < end; i++ {
				line := strings.TrimSuffix(lines[i], "\r")
				entry := fmt.Sprintf("%6d\t%s\n", i+1, line)
				if b.Len()+len(entry) > MaxOutputBytes {
					break
				}
				b.WriteString(entry)
				last = i + 1
			}
			out := strings.TrimRight(b.String(), "\n")
			if start > 0 || last < total {
				out += fmt.Sprintf("\n\n[showing lines %d-%d of %d]", start+1, last, total)
			}
			return out, nil
		},
	}
}
