package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeRefFile(t *testing.T, dir, name string, lineCount int) {
	t.Helper()
	lines := make([]string, lineCount)
	for i := range lines {
		lines[i] = "// line " + strings.Repeat("x", 1) + string(rune('1'+i%9))
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(strings.Join(lines, "\n")), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestExpandReferences_Basic(t *testing.T) {
	dir := t.TempDir()
	writeRefFile(t, dir, "app.go", 10)

	out := ExpandReferences(dir, "看看 app.go:L2-4 这段")
	if !strings.Contains(out, "引用 `app.go:L2-4` 的内容：") {
		t.Fatalf("expected expansion block, got: %s", out)
	}
	if !strings.Contains(out, "```go\n// line") {
		t.Fatalf("expected fenced go snippet, got: %s", out)
	}
	// 第 2 行在，第 1 行不在截取段里
	if !strings.Contains(out, "// line x2") || strings.Contains(out, "// line x1\n") {
		t.Fatalf("line range not honored: %s", out)
	}
}

func TestExpandReferences_SingleLine(t *testing.T) {
	dir := t.TempDir()
	writeRefFile(t, dir, "main.py", 5)

	out := ExpandReferences(dir, "main.py:L3 是什么")
	if !strings.Contains(out, "```py\n// line x3\n```") {
		t.Fatalf("single-line expansion wrong: %s", out)
	}
}

func TestExpandReferences_SkipsInvalid(t *testing.T) {
	dir := t.TempDir()
	writeRefFile(t, dir, "app.go", 10)

	// 文件不存在
	if out := ExpandReferences(dir, "nofile.go:L1-2"); strings.Contains(out, "```") {
		t.Fatal("missing file should not expand")
	}
	// 行号超界
	if out := ExpandReferences(dir, "app.go:L99"); strings.Contains(out, "```") {
		t.Fatal("out-of-range line should not expand")
	}
	// 逃逸路径
	if out := ExpandReferences(dir, "../secret.txt:L1"); strings.Contains(out, "```") {
		t.Fatal("escaping path should not expand")
	}
	// 无引用原样返回
	if out := ExpandReferences(dir, "普通消息"); out != "普通消息" {
		t.Fatal("plain message should pass through")
	}
	// 空工作区
	if out := ExpandReferences("", "app.go:L1"); out != "app.go:L1" {
		t.Fatal("empty workspace should pass through")
	}
}

func TestExpandReferences_DedupAndClamp(t *testing.T) {
	dir := t.TempDir()
	writeRefFile(t, dir, "app.go", 300)

	// 重复引用只展开一次
	out := ExpandReferences(dir, "app.go:L1-2 和 app.go:L1-2")
	if strings.Count(out, "```go") != 1 {
		t.Fatalf("duplicate ref should expand once, got: %s", out)
	}
	// 超过上限截断并标注
	out = ExpandReferences(dir, "app.go:L1-290")
	if !strings.Contains(out, "截断") {
		t.Fatal("over-limit range should be truncated with note")
	}
	// 行段 clamp 到文件末尾（不报错）
	out = ExpandReferences(dir, "app.go:L295-999")
	if !strings.Contains(out, "```go") {
		t.Fatal("range beyond EOF should clamp, not fail")
	}
}
