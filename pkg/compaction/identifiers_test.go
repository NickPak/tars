package compaction

import (
	"context"
	"strings"
	"testing"

	"tars/pkg/schema"
)

func TestExtractIdentifiers(t *testing.T) {
	text := `read https://example.com/a.zip and /usr/local/bin,
uuid 123e4567-e89b-12d3-a456-426614174000, ip 127.0.0.1:8080,
hash abc1234, win C:\Users\x\file.txt, path pkg/compaction/compactor.go`
	ids := extractIdentifiers(text)
	for _, want := range []string{
		"https://example.com/a.zip",
		"123e4567-e89b-12d3-a456-426614174000",
		"127.0.0.1:8080",
		"abc1234",
		`C:\Users\x\file.txt`,
		"pkg/compaction/compactor.go",
		"usr/local/bin", // 起始 \b 不匹配 "/"，前导斜杠不进 token——双侧提取口径一致即可
	} {
		if _, ok := ids[want]; !ok {
			t.Errorf("identifier %q not extracted", want)
		}
	}
}

func TestHexRequiresDigit(t *testing.T) {
	ids := extractIdentifiers("deadbeef is a word, abc1234 is a hash")
	if _, ok := ids["deadbeef"]; ok {
		t.Error("pure-letter hex-like word should be excluded")
	}
	if _, ok := ids["abc1234"]; !ok {
		t.Error("hex with digit should be kept")
	}
}

func TestProseNotPath(t *testing.T) {
	ids := extractIdentifiers("this and/or that, yes/no")
	for id := range ids {
		t.Errorf("unexpected identifier from prose: %q", id)
	}
}

func TestURLTrailingPunctuationTrimmed(t *testing.T) {
	ids := extractIdentifiers("see https://example.com/a.zip, and https://b.dev/x.")
	if _, ok := ids["https://example.com/a.zip"]; !ok {
		t.Error("trailing comma should be trimmed")
	}
	if _, ok := ids["https://b.dev/x"]; !ok {
		t.Error("trailing period should be trimmed")
	}
}

func TestMissingIdentifiers(t *testing.T) {
	batch := []*schema.Message{
		{ID: "u1", Role: schema.RoleUser, Content: "fetch https://example.com/data.csv please"},
	}
	entries := []*ArchiveEntry{{
		Goal: "fetch data", Identifiers: []string{"https://example.com/data.csv"},
	}}

	// 条目覆盖 → 通过
	if missing := MissingIdentifiers(batch, entries, nil); len(missing) != 0 {
		t.Fatalf("missing = %v", missing)
	}
	// 条目缺失 → 报告（URL 与裸路径两个 token 都被检出，双侧口径一致）
	entries[0].Identifiers = nil
	missing := MissingIdentifiers(batch, entries, nil)
	if len(missing) != 2 || missing[1] != "https://example.com/data.csv" {
		t.Fatalf("missing = %v", missing)
	}
	// 保留区包含 → 通过
	retained := []*schema.Message{{Role: schema.RoleUser, Content: "see https://example.com/data.csv"}}
	if missing := MissingIdentifiers(batch, entries, retained); len(missing) != 0 {
		t.Fatalf("missing = %v", missing)
	}
}

func TestIdentifierCheckBlocksCompression(t *testing.T) {
	raw := mkTrajectory(10)
	raw[0].Content = "download https://example.com/bigfile.tar.gz first" // 标识符进压缩集
	withUsage(raw, 200000)
	st := &fakeStore{raw: raw, archiveDir: t.TempDir()}

	c := New(st, nil, nil, nil, Config{}) // StaticExtractor 条目不含该 URL
	out := c.Maybe(context.Background(), nil)
	if out.Err == nil || !strings.Contains(out.Err.Error(), "identifier") {
		t.Fatalf("out = %+v, want identifier error", out)
	}
	if st.comp != nil {
		t.Fatal("compaction must not be written on identifier failure")
	}
}
