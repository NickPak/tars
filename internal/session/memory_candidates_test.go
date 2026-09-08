package session

import (
	"context"
	"strings"
	"testing"

	"tars/pkg/llm"
	"tars/pkg/memory"
)

// asksExtractor 产出带 UserAsks 的条目（挂钩测试用，不调 LLM）。
type asksExtractor struct{}

func (asksExtractor) Extract(_ context.Context, _ llm.Provider, req *ExtractRequest) ([]*ArchiveEntry, error) {
	return []*ArchiveEntry{{
		Range: "turn_x", Goal: "g",
		UserAsks: []string{"以后都用 pnpm 安装依赖", "帮我修一下超时"},
		Result:   "ok", Pointer: req.Pointer,
	}}, nil
}

// mockCandidateSink 记录 Suggest 收到的候选原料。
type mockCandidateSink struct{ items []memory.CandidateItem }

func (m *mockCandidateSink) Suggest(items []memory.CandidateItem) {
	m.items = append(m.items, items...)
}

// 压缩成功 → UserAsks 派生候选原料交给记忆侧（P3 采纳制挂点）：
// 只传 UserAsks（feedback），带 archive:// 溯源指针。
func TestCompressSuggestsMemoryCandidates(t *testing.T) {
	m, _ := newCompressingManager(t, asksExtractor{})
	sink := &mockCandidateSink{}
	m.SetCandidateSink(sink)
	appendTurns(t, m, 10)
	withLastUsage(m, 200000)

	m.MaybeCompress(context.Background(), nil)
	if m.GetCompaction() == nil {
		t.Fatal("compression did not happen")
	}

	if len(sink.items) != 2 {
		t.Fatalf("candidate items = %d, want 2 (UserAsks only)", len(sink.items))
	}
	for _, it := range sink.items {
		if it.Type != memory.FactFeedback {
			t.Errorf("UserAsks should map to feedback type, got %s", it.Type)
		}
		if !strings.HasPrefix(it.Pointer, ArchiveScheme) {
			t.Errorf("candidate should carry archive:// pointer, got %q", it.Pointer)
		}
	}
}

// 未注入 sink 时压缩主路径不受任何影响（旁路语义）。
// 现有全部压缩测试都未注入 sink——压缩不 panic 即覆盖本语义。
func TestCompressWithoutCandidateSink(t *testing.T) {
	m, _ := newCompressingManager(t, asksExtractor{})
	appendTurns(t, m, 10)
	withLastUsage(m, 200000)
	m.MaybeCompress(context.Background(), nil)
	if m.GetCompaction() == nil {
		t.Fatal("compression must work without a candidate sink")
	}
}
