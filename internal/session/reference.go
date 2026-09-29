package session

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"tars/pkg/sandbox"
)

// reference.go —— 输入框文件引用（path:Lx-Ly）展开。
//
// 前端文件查看器框选代码后插入 `app.go:L115-120` 形式的引用；发送时
// 在消息入库前展开为真实代码片段——模型看到的是内容+出处，而不是一个
// 无法解析的裸引用。这是内置编辑器相对外部 IDE 的核心增值点。

// refPattern 匹配工作区相对路径 + L 行号引用：
//   app.go:L115        单行
//   app.go:L115-120    行段
// 路径字符集排除空白和反引号（避免吞掉 markdown 结构）。
var refPattern = regexp.MustCompile("([\\w.\\-/\\\\]+):L(\\d+)(?:-(\\d+))?")

// 展开上限：防一条消息塞进过多代码把窗口挤爆。
const (
	maxRefExpansions = 3   // 单条消息最多展开的引用数
	maxRefLines      = 200 // 单段最多截取的行数
)

// ExpandReferences 把消息中的 path:Lx-Ly 引用展开为代码片段块，追加在
// 消息末尾（保留原文引用字样，UI 与模型都能定位出处）。文件缺失/越界/
// 行号超界的引用原样保留（模型可自行用 read_file 兜底）。同一引用去重。
func ExpandReferences(wsDir, content string) string {
	if wsDir == "" || !strings.Contains(content, ":L") {
		return content
	}
	matches := refPattern.FindAllStringSubmatch(content, -1)
	if len(matches) == 0 {
		return content
	}

	fs := sandbox.NewNativeFs(wsDir)
	seen := map[string]bool{}
	var blocks []string

	for _, m := range matches {
		if len(blocks) >= maxRefExpansions {
			break
		}
		relPath, startStr, endStr := m[1], m[2], m[3]
		refKey := m[0]
		if seen[refKey] {
			continue
		}
		seen[refKey] = true

		var start, end int
		if _, err := fmt.Sscanf(startStr, "%d", &start); err != nil {
			continue
		}
		end = start
		if endStr != "" {
			if _, err := fmt.Sscanf(endStr, "%d", &end); err != nil {
				continue
			}
		}

		snippet := extractLines(fs, relPath, start, end)
		if snippet == "" {
			continue // 文件缺失/越界/空段：保留原文引用
		}
		lang := strings.TrimPrefix(strings.ToLower(filepath.Ext(relPath)), ".")
		blocks = append(blocks, fmt.Sprintf("引用 `%s` 的内容：\n```%s\n%s\n```", refKey, lang, snippet))
	}

	if len(blocks) == 0 {
		return content
	}
	return content + "\n\n---\n" + strings.Join(blocks, "\n\n")
}

// extractLines 读取文件并截取 [start, end] 行段（1-based，闭区间，
// clamp 到文件实际行数；超过上限截断并标注）。
func extractLines(fs *sandbox.NativeFs, relPath string, start, end int) string {
	data, err := fs.ReadFile(relPath)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	if start < 1 {
		start = 1
	}
	if end < start {
		end = start
	}
	if start > len(lines) {
		return "" // 行号超界
	}
	if end > len(lines) {
		end = len(lines)
	}
	truncated := false
	if end-start+1 > maxRefLines {
		end = start + maxRefLines - 1
		truncated = true
	}
	snippet := strings.Join(lines[start-1:end], "\n")
	if strings.TrimSpace(snippet) == "" {
		return ""
	}
	if truncated {
		snippet += fmt.Sprintf("\n…（截断：仅截取前 %d 行）", maxRefLines)
	}
	return snippet
}
