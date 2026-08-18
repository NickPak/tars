package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testEnv 是当前用例共享的执行环境（与生产语义一致：Env 会话级长命，
// 跨多次工具调用持有 WorkDir 与 TermSessions 等状态）。
var testEnv *Env

func call(t *testing.T, def *Definition, args string) string {
	t.Helper()
	ctx := WithEnv(context.Background(), testEnv)
	out, err := def.Handler(ctx, json.RawMessage(args))
	if err != nil {
		t.Fatalf("%s failed: %v", def.Name, err)
	}
	return out
}

func TestBuiltinToolsSmoke(t *testing.T) {
	dir := t.TempDir()
	testEnv = &Env{WorkDir: dir}
	os.MkdirAll(filepath.Join(dir, "src", "pkg"), 0755)
	os.WriteFile(filepath.Join(dir, "src", "main.go"), []byte("package main\n\nfunc main() {}\n"), 0644)
	os.WriteFile(filepath.Join(dir, "src", "pkg", "util.py"), []byte("def helper():\n    return 42\n"), 0644)
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("alpha\nbeta\ngamma\n"), 0644)

	// glob_files
	out := call(t, GlobFiles(), `{"pattern":"**/*.go","path":"."}`)
	if !strings.Contains(out, filepath.Join("src", "main.go")) {
		t.Fatalf("glob_files missing main.go: %s", out)
	}
	out = call(t, GlobFiles(), `{"pattern":"*.py","path":"."}`)
	if !strings.Contains(out, "util.py") {
		t.Fatalf("glob_files bare pattern should match at any depth: %s", out)
	}

	// grep_files
	out = call(t, GrepFiles(), `{"pattern":"func main","include":"*.go","path":"."}`)
	if !strings.Contains(out, "main.go:3:") {
		t.Fatalf("grep_files bad output: %s", out)
	}

	// read_file: line numbers + range notice
	out = call(t, ReadFile(), `{"path":"notes.txt","offset":2,"limit":1}`)
	if !strings.Contains(out, "2\tbeta") || !strings.Contains(out, "[showing lines 2-2 of 4]") {
		t.Fatalf("read_file bad output: %s", out)
	}

	// write_file
	call(t, WriteFile(), `{"path":"new/deep.txt","content":"hello\nworld"}`)
	if _, err := os.Stat(filepath.Join(dir, "new", "deep.txt")); err != nil {
		t.Fatalf("write_file did not create nested file: %v", err)
	}

	// edit_file exact mode
	call(t, EditFile(), `{"path":"notes.txt","old_string":"beta","new_string":"BETA"}`)
	data, _ := os.ReadFile(filepath.Join(dir, "notes.txt"))
	if !strings.Contains(string(data), "BETA") {
		t.Fatalf("edit_file exact mode failed: %s", data)
	}

	// edit_file marker mode
	call(t, EditFile(), `{"path":"notes.txt","start_marker":"alpha","end_marker":"gamma","new_string":"only"}`)
	data, _ = os.ReadFile(filepath.Join(dir, "notes.txt"))
	if strings.TrimSpace(string(data)) != "only" {
		t.Fatalf("edit_file marker mode failed: %s", data)
	}

	// edit_file ambiguity must fail without touching the file
	os.WriteFile(filepath.Join(dir, "dup.txt"), []byte("x x"), 0644)
	_, err := EditFile().Handler(WithEnv(context.Background(), &Env{WorkDir: dir}), json.RawMessage(`{"path":"dup.txt","old_string":"x","new_string":"y"}`))
	if err == nil {
		t.Fatal("edit_file should reject ambiguous old_string")
	}
	data, _ = os.ReadFile(filepath.Join(dir, "dup.txt"))
	if string(data) != "x x" {
		t.Fatal("edit_file must leave the file untouched on failure")
	}

	// run_command persistent session: cwd must carry over
	rc := RunCommand()
	call(t, rc, `{"command":"cd src"}`)
	out = call(t, rc, `{"command":"cd"}`)
	if !strings.Contains(filepath.ToSlash(out), "src") {
		t.Fatalf("run_command session did not persist cwd: %s", out)
	}
}
