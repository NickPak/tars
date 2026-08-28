package compaction

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"tars/pkg/llm"
	"tars/pkg/schema"
)

type fakeStore struct {
	raw        []*schema.Message
	comp       *Compaction
	archiveDir string
	unmarked   []string
}

func (f *fakeStore) RawHistory() []*schema.Message       { return f.raw }
func (f *fakeStore) Compaction() *Compaction             { return f.comp }
func (f *fakeStore) ApplyCompaction(c *Compaction) error { f.comp = c; return nil }
func (f *fakeStore) WriteArchive(label string, content []byte) (string, error) {
	path := filepath.Join(f.archiveDir, label+".md")
	if err := os.MkdirAll(f.archiveDir, 0o755); err != nil {
		return "", err
	}
	return path, os.WriteFile(path, content, 0o644)
}
func (f *fakeStore) UnmarkSkillLoaded(name string) { f.unmarked = append(f.unmarked, name) }

type failExtractor struct{}

func (failExtractor) Extract(context.Context, llm.Provider, *ExtractRequest) ([]*ArchiveEntry, error) {
	return nil, fmt.Errorf("boom")
}

// withUsage 给轨迹最后一条 assistant 消息标注实测用量（触发信号）。
func withUsage(raw []*schema.Message, promptTokens int) {
	for i := len(raw) - 1; i >= 0; i-- {
		if raw[i].Role == schema.RoleAssistant {
			raw[i].Usage = &schema.UsageInfo{PromptTokens: promptTokens}
			return
		}
	}
}

func newTriggeredStore(t *testing.T, turns, promptTokens int) *fakeStore {
	t.Helper()
	raw := mkTrajectory(turns)
	withUsage(raw, promptTokens)
	return &fakeStore{raw: raw, archiveDir: t.TempDir()}
}

func TestMaybeNotTriggered(t *testing.T) {
	st := newTriggeredStore(t, 10, 1000) // 阈值 0.8×128000，远未达
	c := New(st, nil, nil, nil, Config{})
	if out := c.Maybe(context.Background(), nil); out.Triggered {
		t.Fatal("should not trigger below threshold")
	}
	if st.comp != nil {
		t.Fatal("compaction should stay nil")
	}
}

func TestMaybeNoUsageSignal(t *testing.T) {
	st := &fakeStore{raw: mkTrajectory(10), archiveDir: t.TempDir()} // 无 Usage
	c := New(st, nil, nil, nil, Config{})
	if out := c.Maybe(context.Background(), nil); out.Triggered {
		t.Fatal("should not trigger without usage signal")
	}
}

func TestCompress(t *testing.T) {
	st := newTriggeredStore(t, 10, 200000) // 超阈值
	c := New(st, nil, nil, nil, Config{})
	out := c.Maybe(context.Background(), nil)
	if out.Err != nil {
		t.Fatalf("compress: %v", out.Err)
	}
	if !out.Triggered || !out.Compressed {
		t.Fatalf("out = %+v, want triggered+compressed", out)
	}
	comp := st.comp
	if comp == nil || comp.CutoffMessageID == "" || len(comp.Entries) != 1 {
		t.Fatalf("compaction = %+v", comp)
	}
	// Range/Pointer 回填
	e := comp.Entries[0]
	if e.Range == "" || e.Pointer == "" {
		t.Fatalf("entry not backfilled: %+v", e)
	}
	// 归档文件已落盘
	if _, err := os.Stat(filepath.Join(st.archiveDir, e.Range+".md")); err != nil {
		t.Fatalf("archive missing: %v", err)
	}
	// Stats 计量
	if comp.Stats == nil || comp.Stats.BeforeTokens != 200000 {
		t.Fatalf("stats = %+v", comp.Stats)
	}
	if out.AfterTokens <= 0 {
		t.Fatalf("afterTokens = %d", out.AfterTokens)
	}
}

func TestIncrementalCompress(t *testing.T) {
	st := newTriggeredStore(t, 10, 200000)
	c := New(st, nil, nil, nil, Config{})
	if out := c.Maybe(context.Background(), nil); !out.Compressed {
		t.Fatalf("first compress: %+v", out)
	}
	firstCutoff := st.comp.CutoffMessageID

	// 轨迹继续增长（新增 5 轮，ID 带前缀保持唯一），再次触发
	st.raw = append(st.raw, mkTrajectoryP("b", 5)...)
	withUsage(st.raw, 300000)

	if out := c.Maybe(context.Background(), nil); !out.Compressed {
		t.Fatalf("second compress: %+v", out)
	}
	if len(st.comp.Entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(st.comp.Entries))
	}
	if st.comp.CutoffMessageID == firstCutoff {
		t.Fatal("cutoff did not advance")
	}
}

func TestCircuitBreaker(t *testing.T) {
	st := newTriggeredStore(t, 10, 200000)
	c := New(st, nil, failExtractor{}, nil, Config{})
	for i := 0; i < 3; i++ {
		out := c.Maybe(context.Background(), nil)
		if out.Err == nil {
			t.Fatalf("run %d: expected error", i)
		}
	}
	if !c.CircuitOpen() {
		t.Fatal("circuit should open after 3 consecutive failures")
	}
	// 熔断后短路：不再触发
	if out := c.Maybe(context.Background(), nil); out.Triggered {
		t.Fatal("circuit open: Maybe should short-circuit")
	}
	if st.comp != nil {
		t.Fatal("failed compression must not write compaction")
	}
}

func TestLoadSkillUnmark(t *testing.T) {
	raw := mkTrajectory(10)
	// 早期轮次注入一次 load_skill 调用（其正文随压缩离开上下文）
	raw[1].ToolCalls = append(raw[1].ToolCalls, schema.ToolCall{
		ID: "tc-skill", Name: "load_skill", Args: `{"name":"pptx"}`,
	})
	withUsage(raw, 200000)
	st := &fakeStore{raw: raw, archiveDir: t.TempDir()}

	c := New(st, nil, nil, nil, Config{})
	if out := c.Maybe(context.Background(), nil); !out.Compressed {
		t.Fatalf("compress: %+v", out)
	}
	if len(st.unmarked) != 1 || st.unmarked[0] != "pptx" {
		t.Fatalf("unmarked = %v, want [pptx]", st.unmarked)
	}
}
