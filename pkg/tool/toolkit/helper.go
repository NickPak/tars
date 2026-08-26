package toolkit

import (
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"tars/pkg/sandbox"
)

// MaxOutputBytes caps a single tool output to avoid blowing up the context.
const MaxOutputBytes = 64 * 1024

// UnmarshalArgs 将模型生成的 JSON 参数反序列化为指定类型，供 Handler 使用。
func UnmarshalArgs[T any](args json.RawMessage) (T, error) {
	var v T
	err := json.Unmarshal(args, &v)
	return v, err
}

// WalkDir 基于 FileSystem.ReadDir 递归遍历目录（沙箱实现只需提供
// 非递归 ReadDir，遍历逻辑由工具侧承担）。rel 为相对 root 的
// 斜杠路径；目录遍历时跳过常见噪音目录；不可读的分支静默跳过。
// fn 返回 false 时停止遍历。
func WalkDir(fs sandbox.FileSystem, root string, fn func(rel string, e sandbox.DirEntry) bool) {
	var rec func(dir, relBase string) bool
	rec = func(dir, relBase string) bool {
		entries, err := fs.ReadDir(dir)
		if err != nil {
			return true // 跳过不可读分支
		}
		for _, e := range entries {
			rel := e.Name
			if relBase != "" {
				rel = relBase + "/" + e.Name
			}
			if e.IsDir {
				if IsIgnoredEntry(e.Name) {
					continue
				}
				if !rec(path.Join(dir, e.Name), rel) {
					return false
				}
				continue
			}
			if !fn(rel, e) {
				return false
			}
		}
		return true
	}
	rec(root, "")
}

// RelToOS 把斜杠相对路径转为本机分隔符（输出展示用，保持与
// filepath.Rel 的历史输出一致）。
func RelToOS(rel string) string {
	return filepath.FromSlash(rel)
}

// TruncateOutput caps s at maxOutputBytes with an explicit notice; truncation
// is never silent. ToValidUTF8 keeps the cut from landing mid-rune.
func TruncateOutput(s string) string {
	if len(s) <= MaxOutputBytes {
		return s
	}
	return strings.ToValidUTF8(s[:MaxOutputBytes], "") + fmt.Sprintf("\n\n[output too large, truncated to first %d bytes]", MaxOutputBytes)
}

// IsIgnoredEntry skips common noise and version-control directories when
// walking trees (glob_files/grep_files).
func IsIgnoredEntry(name string) bool {
	switch name {
	case ".git", "node_modules", ".venv", "dist", "build", "__pycache__", ".idea", ".vscode":
		return true
	}
	return false
}

// LooksBinary sniffs the first bytes for a NUL, the cheapest reliable
// binary indicator.
func LooksBinary(data []byte) bool {
	const sniff = 512
	n := min(len(data), sniff)
	for i := 0; i < n; i++ {
		if data[i] == 0 {
			return true
		}
	}
	return false
}
