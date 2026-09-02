package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tars/pkg/event"
	"tars/pkg/llm"
	"tars/pkg/schema"
)

type failExtractor struct{}

func (failExtractor) Extract(context.Context, llm.Provider, *ExtractRequest) ([]*ArchiveEntry, error) {
	return nil, fmt.Errorf("boom")
}

// bloatExtractor 产出比原文更大的条目（经济性校验的反例）。
type bloatExtractor struct{}

func (bloatExtractor) Extract(_ context.Context, _ llm.Provider, req *ExtractRequest) ([]*ArchiveEntry, error) {
	return []*ArchiveEntry{{
		Goal:   strings.Repeat("冗长无用的摘要文本", 500),
		Result: "ok",
	}}, nil
}

// compressionKinds 按顺序提取捕获到的压缩事件 kind。
func compressionKinds(sink *recordingSink) []event.Kind {
	var kinds []event.Kind
	for _, e := range sink.events {
		switch e.Kind {
		case event.KindCompressionStarted, event.KindCompressionDone, event.KindCompressionFailed:
			kinds = append(kinds, e.Kind)
		}
	}
	return kinds
}

func lastDone(sink *recordingSink) *event.CompressionDoneEvent {
	var out *event.CompressionDoneEvent
	for _, e := range sink.events {
		if e.Kind == event.KindCompressionDone {
			out = e.CompressionDone
		}
	}
	return out
}

func lastFailed(sink *recordingSink) *event.CompressionFailedEvent {
	var out *event.CompressionFailedEvent
	for _, e := range sink.events {
		if e.Kind == event.KindCompressionFailed {
			out = e.CompressionFailed
		}
	}
	return out
}

// countingExtractor 记录被调次数（陈旧信号闸门的断言用）。
type countingExtractor struct{ calls int }

func (c *countingExtractor) Extract(context.Context, llm.Provider, *ExtractRequest) ([]*ArchiveEntry, error) {
	c.calls++
	return []*ArchiveEntry{{Goal: "g", Result: "ok"}}, nil
}

// 同一个实测用量只作用一次。
//
// 实测值来自上一次请求，本轮已生效的压缩要等下一次请求才反映进去。没有这道
// 闸门时，降级阶梯会拿着同一个陈旧数字连续压好几次——每次一次 LLM 调用 +
// 一次缓存重建。机制：Normal 档压完后 cutoff 前移，Normal 自身确实切不出新
// 批次（cut <= start），但 High 档保留更少轮次，"cutoff 之后"重新出现可压
// 区间，于是又压一次。
//
// 这一条是回归防线：设计讨论中曾误判"压缩管线自限，不需要闸门"。
func TestSameUsageSignalActsOnce(t *testing.T) {
	ext := &countingExtractor{}
	m, _ := newCompressingManager(t, ext)
	appendTurns(t, m, 10)
	withLastUsage(m, 200000)

	for i := 0; i < 4; i++ {
		m.MaybeCompress(context.Background(), nil)
	}
	if ext.calls != 1 {
		t.Fatalf("extractor called %d times on one usage signal, want 1", ext.calls)
	}

	// 新的实测值（仍超预算）→ 闸门放行
	appendTurnsP(t, m, "b", 5)
	withLastUsage(m, 210000)
	m.MaybeCompress(context.Background(), nil)
	if ext.calls != 2 {
		t.Fatalf("a fresh usage signal must be acted on: calls = %d, want 2", ext.calls)
	}
}

// 提取调用超窗口 → 立即熔断，不等攒够 MaxFailures。
//
// 提取的输入是「完整轨迹 + 指令」，比常规请求更大：常规请求已经逼近上限时，
// 提取必然同样失败。重试不是瞬态重试，而是重复白付全量 prefill。
func TestMaybeCompressOpensCircuitOnContextOverflow(t *testing.T) {
	overflow := errors.New("This model's maximum context length is 128000 tokens")
	m, sink := newCompressingManager(t, errExtractor{err: overflow})
	appendTurns(t, m, 10)
	withLastUsage(m, 200000)

	m.MaybeCompress(context.Background(), nil)

	if !m.circuitOpen {
		t.Fatal("circuit must open immediately on a context-overflow extraction error")
	}
	f := lastFailed(sink)
	if f == nil || !f.CircuitOpen {
		t.Fatalf("failed event should report the open circuit: %+v", f)
	}
	if f.Failures != 1 {
		t.Fatalf("failures = %d, want 1 (opened on the first attempt)", f.Failures)
	}
}

// errExtractor 是可指定错误的提取器（failExtractor 的通用版）。
type errExtractor struct{ err error }

func (e errExtractor) Extract(context.Context, llm.Provider, *ExtractRequest) ([]*ArchiveEntry, error) {
	return nil, e.err
}

// 用户取消不得计入熔断：否则压缩期间连按三次停止，本会话压缩被永久禁用，
// 而下一轮上下文只会更大——届时压缩恰恰最该工作。
func TestMaybeCompressCancelDoesNotCountAsFailure(t *testing.T) {
	m, sink := newCompressingManager(t, errExtractor{err: context.Canceled})
	appendTurns(t, m, 10)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 模拟用户在压缩进行中按停止

	for i := 0; i < testMaxFailures+2; i++ {
		withLastUsage(m, 200000+i) // 每次换个新信号，绕过陈旧信号闸门
		m.MaybeCompress(ctx, nil)
	}

	if m.circuitOpen {
		t.Fatal("cancellation must not open the circuit")
	}
	if m.failures != 0 {
		t.Fatalf("failures = %d, want 0 (cancellation is not a compression fault)", m.failures)
	}
	// 仍与已发出的 Started 配对（不留悬挂 span）
	kinds := compressionKinds(sink)
	var started, failed int
	for _, k := range kinds {
		switch k {
		case event.KindCompressionStarted:
			started++
		case event.KindCompressionFailed:
			failed++
		}
	}
	if started == 0 || started != failed {
		t.Fatalf("started=%d failed=%d; every Started needs a pairing Failed", started, failed)
	}
}

// 真实故障仍然照常计数并熔断（上一条不能把熔断整个废掉）。
func TestMaybeCompressRealFailureStillTrips(t *testing.T) {
	m, _ := newCompressingManager(t, failExtractor{})
	appendTurns(t, m, 10)

	for i := 0; i < testMaxFailures; i++ {
		withLastUsage(m, 200000+i)
		m.MaybeCompress(context.Background(), nil)
	}
	if !m.circuitOpen {
		t.Fatal("real failures must still open the circuit")
	}
}

// 提取模型记入计量（成本归因：压缩自身要付一次全量 prefill）。
func TestCompressionStatsRecordsExtractModel(t *testing.T) {
	m, _ := newCompressingManager(t, nil)
	appendTurns(t, m, 10)
	withLastUsage(m, 200000)

	m.MaybeCompress(context.Background(), nil)

	st := m.GetCompaction().Stats
	if st == nil || st.ExtractModel == "" {
		t.Fatalf("stats must record the extraction model: %+v", st)
	}
}

func TestMaybeCompressBelowThreshold(t *testing.T) {
	m, sink := newCompressingManager(t, nil)
	appendTurns(t, m, 10)
	withLastUsage(m, 1000) // 阈值 0.8×128000，远未达

	m.MaybeCompress(context.Background(), nil)
	if m.GetCompaction() != nil {
		t.Fatal("compaction should stay nil below threshold")
	}
	if k := compressionKinds(sink); len(k) != 0 {
		t.Fatalf("no compression events expected, got %v", k)
	}
}

func TestMaybeCompressNoUsageSignal(t *testing.T) {
	m, sink := newCompressingManager(t, nil)
	appendTurns(t, m, 10) // 无 Usage

	m.MaybeCompress(context.Background(), nil)
	if m.GetCompaction() != nil || len(compressionKinds(sink)) != 0 {
		t.Fatal("should not compress without usage signal")
	}
}

// 真正无可压时静默放弃：不写压缩态，也不发任何事件（避免产生没有
// Done/Failed 配对的悬挂 span）。轨迹只有 1 轮——连极限档（留 1 轮）
// 也切不出压缩集。
func TestMaybeCompressNothingToCompress(t *testing.T) {
	m, sink := newCompressingManager(t, nil)
	appendTurns(t, m, 1)
	withLastUsage(m, 200000)

	m.MaybeCompress(context.Background(), nil)
	if m.GetCompaction() != nil {
		t.Fatal("compaction should not be written when there is nothing to compress")
	}
	if k := compressionKinds(sink); len(k) != 0 {
		t.Fatalf("no events expected when declined, got %v", k)
	}
}

// 降级阶梯：常规档被 MinBatch 挡住，但用量已远超预算——此时不压的后果是
// 请求超长被供应商拒绝、整轮失败，故必须放宽保留区照压。
// （回归：此前这里静默放弃，是"卡住"的真实成因。）
func TestMaybeCompressDegradesInsteadOfGivingUp(t *testing.T) {
	m, sink := newCompressingManager(t, nil)
	appendTurns(t, m, 7) // keepTurns=6 → 常规档压缩集仅 3 条 < minBatch=8
	withLastUsage(m, 200000)

	m.MaybeCompress(context.Background(), nil)

	comp := m.GetCompaction()
	if comp == nil || len(comp.Entries) != 1 {
		t.Fatalf("compaction should be written under pressure: %+v", comp)
	}
	kinds := compressionKinds(sink)
	if len(kinds) != 2 || kinds[1] != event.KindCompressionDone {
		t.Fatalf("kinds = %v, want [started done]", kinds)
	}
}

// 档位选择：预算够时取能压的最温和档；全档位都压不回预算时取最激进档
// 尽力而为。
func TestPlanCompactionPressureSelection(t *testing.T) {
	m := newTestManager(t)
	appendTurns(t, m, 7)
	raw := m.RawHistory()

	// 常规档被 minBatch 挡住 → 降一级即可满足预算
	plan, ok := m.planCompaction(raw, 100000)
	if !ok || plan.pressure != PressureHigh {
		t.Fatalf("plan pressure = %v (ok=%v), want high", plan.pressure, ok)
	}

	// 预算小到任何档都放不下 → 取最激进档，而不是放弃
	plan, ok = m.planCompaction(raw, 1)
	if !ok || plan.pressure != PressureCritical {
		t.Fatalf("plan pressure = %v (ok=%v), want critical", plan.pressure, ok)
	}
}

func TestMaybeCompressSuccess(t *testing.T) {
	m, sink := newCompressingManager(t, nil)
	appendTurns(t, m, 10)
	withLastUsage(m, 200000)

	m.MaybeCompress(context.Background(), nil)

	comp := m.GetCompaction()
	if comp == nil || comp.CutoffMessageID == "" || len(comp.Entries) != 1 {
		t.Fatalf("compaction = %+v", comp)
	}
	if comp.TimesCompressed != 1 {
		t.Fatalf("timesCompressed = %d, want 1", comp.TimesCompressed)
	}
	// Range/Pointer 回填 + 归档原文落盘
	e := comp.Entries[0]
	if e.Range == "" || e.Pointer == "" {
		t.Fatalf("entry not backfilled: %+v", e)
	}
	if _, err := os.Stat(filepath.Join(m.GetDataDir(), ArchiveDir, e.Range+".md")); err != nil {
		t.Fatalf("archive missing: %v", err)
	}
	if comp.Stats == nil || comp.Stats.BeforeTokens != 200000 {
		t.Fatalf("stats = %+v", comp.Stats)
	}
	// 事件：Started → Done
	kinds := compressionKinds(sink)
	if len(kinds) != 2 || kinds[0] != event.KindCompressionStarted || kinds[1] != event.KindCompressionDone {
		t.Fatalf("kinds = %v, want [started done]", kinds)
	}
	done := lastDone(sink)
	if done == nil || done.BeforeTokens != 200000 || done.NewEntries != 1 || done.TotalEntries != 1 {
		t.Fatalf("done payload = %+v", done)
	}
	if done.AfterTokens <= 0 {
		t.Fatalf("afterTokens = %d", done.AfterTokens)
	}
}

func TestMaybeCompressIncremental(t *testing.T) {
	m, _ := newCompressingManager(t, nil)
	appendTurnsP(t, m, "a", 10)
	withLastUsage(m, 200000)
	m.MaybeCompress(context.Background(), nil)
	first := m.GetCompaction()
	if first == nil {
		t.Fatal("first compression did not happen")
	}
	firstCutoff := first.CutoffMessageID

	// 轨迹继续增长后再次触发：条目追加、cutoff 前移、计数累加
	appendTurnsP(t, m, "b", 5)
	withLastUsage(m, 300000)
	m.MaybeCompress(context.Background(), nil)

	comp := m.GetCompaction()
	if len(comp.Entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(comp.Entries))
	}
	if comp.CutoffMessageID == firstCutoff {
		t.Fatal("cutoff did not advance")
	}
	if comp.TimesCompressed != 2 {
		t.Fatalf("timesCompressed = %d, want 2", comp.TimesCompressed)
	}
}

func TestMaybeCompressCircuitBreaker(t *testing.T) {
	m, sink := newCompressingManager(t, failExtractor{})
	appendTurns(t, m, 10)
	withLastUsage(m, 200000)

	for i := 0; i < testMaxFailures; i++ {
		m.MaybeCompress(context.Background(), nil)
	}
	if !m.circuitOpen {
		t.Fatal("circuit should open after consecutive failures")
	}
	f := lastFailed(sink)
	if f == nil || !f.CircuitOpen || f.Failures != testMaxFailures {
		t.Fatalf("last failed payload = %+v", f)
	}
	// 熔断后短路：不再产生新事件
	before := len(compressionKinds(sink))
	m.MaybeCompress(context.Background(), nil)
	if after := len(compressionKinds(sink)); after != before {
		t.Fatal("circuit open: MaybeCompress should short-circuit")
	}
	if m.GetCompaction() != nil {
		t.Fatal("failed compression must not write compaction")
	}
}

// 经济性校验：条目比它替代的消息还大时放弃压缩（否则白付一次缓存重建）。
// 注：不做内容正确性校验——保真靠提示词约束 + pointer 可找回性。
func TestMaybeCompressRejectsUneconomicalEntries(t *testing.T) {
	m, sink := newCompressingManager(t, bloatExtractor{})
	appendTurns(t, m, 10)
	withLastUsage(m, 200000)

	m.MaybeCompress(context.Background(), nil)

	f := lastFailed(sink)
	if f == nil || !strings.Contains(f.Error, "not economical") {
		t.Fatalf("failed event = %+v, want economy rejection", f)
	}
	if m.GetCompaction() != nil {
		t.Fatal("compaction must not be written when entries are bigger than the batch")
	}
}

// 一致性红线（02 篇 §5.1）：skill 正文随压缩离开上下文时，
// 会话 loaded 记录必须同步清除并写穿 meta.json。
func TestMaybeCompressUnmarksArchivedSkill(t *testing.T) {
	m, _ := newCompressingManager(t, nil)
	appendTurns(t, m, 10)
	m.data.Messages[1].ToolCalls = append(m.data.Messages[1].ToolCalls, schema.ToolCall{
		ID: "tc-skill", Name: "load_skill", Args: `{"name":"pptx"}`,
	})
	m.MarkSkillLoaded("pptx")
	withLastUsage(m, 200000)

	m.MaybeCompress(context.Background(), nil)

	if m.IsSkillLoaded("pptx") {
		t.Fatal("skill should be unmarked after its body was archived")
	}
	meta, err := GetStoreManager().LoadMetadata(m.GetID())
	if err != nil || meta == nil {
		t.Fatalf("load meta: %v", err)
	}
	if _, ok := meta.LoadedSkills["pptx"]; ok {
		t.Fatal("meta.json should not contain pptx")
	}
}
