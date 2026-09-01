package toolkit

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tars/pkg/sandbox"
)

// fakeArchive 是测试用的归档源：内存 name → content。
type fakeArchive struct {
	files    map[string][]byte
	writeErr error // 非 nil 时模拟落盘失败
}

func (a *fakeArchive) ReadArchive(name string) ([]byte, error) {
	data, ok := a.files[name]
	if !ok {
		return nil, os.ErrNotExist
	}
	return data, nil
}

func (a *fakeArchive) WriteArchive(name string, content []byte) (string, error) {
	if a.writeErr != nil {
		return "", a.writeErr
	}
	if a.files == nil {
		a.files = map[string][]byte{}
	}
	full := name + ".md"
	a.files[full] = content
	return "/tmp/archive/" + full, nil
}

// archiveFT 构造带归档通道的 FileTools（沙箱根为空临时目录）。
func archiveFT(t *testing.T, files map[string][]byte) (*FileTools, string) {
	t.Helper()
	dir := t.TempDir()
	sb := sandbox.NewNativeFs(dir)
	return NewFileTools(sb, &fakeArchive{files: files}), dir
}

func callErr(t *testing.T, ft *FileTools, path string) error {
	t.Helper()
	args, _ := json.Marshal(readFileArgs{Path: path})
	_, err := ft.ReadFile().Handler(context.Background(), args)
	if err == nil {
		t.Fatalf("read_file(%q) should fail", path)
	}
	return err
}

// archive:// 走归档通道，且复用普通读文件的行号前缀与分页能力
// （归档文件本身可能很大——被压掉的工具输出上限 64KB）。
func TestReadFileArchiveChannel(t *testing.T) {
	body := "# Archived turns\n\nline A\nline B\nline C\n"
	ft, _ := archiveFT(t, map[string][]byte{"turn_1-2.md": []byte(body)})

	out := call(t, ft.ReadFile(), `{"path":"archive://turn_1-2.md"}`)
	if !strings.Contains(out, "Archived turns") || !strings.Contains(out, "line C") {
		t.Fatalf("archive content missing: %s", out)
	}
	if !strings.Contains(out, "\t# Archived turns") {
		t.Fatalf("archive read should keep line-number prefixes: %s", out)
	}
	// 分页可用
	out = call(t, ft.ReadFile(), `{"path":"archive://turn_1-2.md","offset":3,"limit":1}`)
	if !strings.Contains(out, "line A") || strings.Contains(out, "line B") {
		t.Fatalf("archive pagination broken: %s", out)
	}
}

// 归档名白名单：路径穿越的唯一防线（scheme 之后不得有任何拼接自由度）。
func TestReadFileArchiveRejectsTraversal(t *testing.T) {
	ft, dir := archiveFT(t, map[string][]byte{"turn_1-2.md": []byte("x")})
	// 沙箱根外放一个诱饵文件
	secret := filepath.Join(filepath.Dir(dir), "secret.md")
	if err := os.WriteFile(secret, []byte("SECRET"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(secret) })

	for _, p := range []string{
		"archive://../secret.md",
		"archive://../../etc/passwd",
		"archive://sub/turn_1-2.md",
		"archive://" + secret,
		"archive://turn_1-2.txt", // 非 .md
		"archive://",
	} {
		err := callErr(t, ft, p)
		if strings.Contains(err.Error(), "SECRET") {
			t.Fatalf("%q leaked content", p)
		}
	}
}

// 未装配归档通道时明确报错，不静默返回空。
func TestReadFileArchiveWithoutProvider(t *testing.T) {
	dir := t.TempDir()
	sb := sandbox.NewNativeFs(dir)
	ft := NewFileTools(sb, nil)

	err := callErr(t, ft, "archive://turn_1-2.md")
	if !strings.Contains(err.Error(), "no compaction archive") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// 模型把归档指针误写成普通路径时，错误里带上正确读法（一次失败即自我纠正）。
func TestReadFileArchiveHint(t *testing.T) {
	ft, _ := archiveFT(t, map[string][]byte{"turn_1-2.md": []byte("x")})

	for _, p := range []string{"archive/turn_1-2.md", "turn_1-2.md", "msg_r7.md"} {
		err := callErr(t, ft, p)
		if !strings.Contains(err.Error(), ArchiveScheme) {
			t.Fatalf("read_file(%q) error should suggest the archive:// form, got: %v", p, err)
		}
	}
	// 普通缺失文件不该被加上归档提示（避免噪声误导）
	err := callErr(t, ft, "README.md")
	if strings.Contains(err.Error(), ArchiveScheme) {
		t.Fatalf("plain missing file should not get an archive hint: %v", err)
	}
}

// 写侧无归档分支：archive:// 落到 confine 被拒，模型无法改写自己的归档。
func TestArchiveIsReadOnly(t *testing.T) {
	ft, _ := archiveFT(t, map[string][]byte{"turn_1-2.md": []byte("x")})

	args, _ := json.Marshal(map[string]string{"path": "archive://turn_1-2.md", "content": "tampered"})
	if _, err := ft.WriteFile().Handler(context.Background(), args); err == nil {
		t.Fatal("write_file must not accept an archive:// path")
	}
}
