package compaction

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"tars/pkg/llm"
	"tars/pkg/schema"
)

// Config 是压缩管线的配置（02 篇 §11；agent.Config 的对应字段由装配层映射）。
type Config struct {
	Threshold     float64 // 触发阈值（0-1），默认 0.8
	KeepTurns     int     // 尾部保留完整轮数，默认 6
	MinBatch      int     // 最小压缩集消息数，默认 8
	MaxFailures   int     // 熔断阈值（连续失败次数），默认 3
	DefaultWindow int     // ContextWindow 未配置时的回退值，默认 128000
}

func (c Config) withDefaults() Config {
	if c.Threshold <= 0 || c.Threshold > 1 {
		c.Threshold = 0.8
	}
	if c.KeepTurns <= 0 {
		c.KeepTurns = 6
	}
	if c.MinBatch <= 0 {
		c.MinBatch = 8
	}
	if c.MaxFailures <= 0 {
		c.MaxFailures = 3
	}
	if c.DefaultWindow <= 0 {
		c.DefaultWindow = 128000
	}
	return c
}

// CompactStore 是压缩器对会话存储的窄接口（消费侧定义，02 篇 §8）；
// 由 internal/session.Manager 实现，测试可用内存 fake。
type CompactStore interface {
	// RawHistory 返回原始轨迹副本（不做压缩投影）。
	RawHistory() []*schema.Message
	// Compaction 返回当前压缩态（nil = 未压缩）。
	Compaction() *Compaction
	// ApplyCompaction 写回压缩态（实现须先原子落盘再改内存，03 篇红线）。
	ApplyCompaction(c *Compaction) error
	// WriteArchive 写入归档原文并返回路径（目录创建由实现方自闭合）。
	WriteArchive(rangeLabel string, content []byte) (string, error)
	// UnmarkSkillLoaded 一致性红线（02 篇 §5.1）：压缩集含 load_skill
	// 正文时从会话 loaded 集合移除（含持久化写穿）。
	UnmarkSkillLoaded(name string)
}

// Compactor 压缩管线编排：触发判定 → 定界 → 归档 → 提取 → 写回 → 一致性红线。
// 会话级长命对象（与 ReActAgent 同寿命）；熔断器是内存态，会话重启复位。
type Compactor struct {
	store  CompactStore
	sel    Selector
	ext    Extractor
	window func() int // 当前激活模型的 ContextWindow（装配层注入，热切换生效）
	cfg    Config

	failures    int  // 连续失败计数
	circuitOpen bool // 熔断：true 后 Maybe 短路（02 篇 §9）
}

// New 创建压缩器。sel/ext/window 为 nil 时使用默认实现
// （tailKeep 选择器 / StaticExtractor / DefaultWindow）。
func New(store CompactStore, sel Selector, ext Extractor, window func() int, cfg Config) *Compactor {
	cfg = cfg.withDefaults()
	if sel == nil {
		sel = NewTailKeepSelector(cfg.KeepTurns, cfg.MinBatch)
	}
	if ext == nil {
		ext = StaticExtractor{}
	}
	if window == nil {
		window = func() int { return cfg.DefaultWindow }
	}
	return &Compactor{store: store, sel: sel, ext: ext, window: window, cfg: cfg}
}

// Outcome 是一次 Maybe 的结果（观测与测试用；期 C 映射为事件）。
type Outcome struct {
	Triggered    bool // 达到触发阈值
	Compressed   bool // 实际完成了压缩（选择器放弃时为 false）
	BeforeTokens int
	AfterTokens  int // 压缩后视图的估算 token（bytes/4）
	Entries      int // 压缩后的条目总数
	Err          error
}

// Maybe 在每轮迭代组装消息前调用：达到阈值则执行一次完整压缩。
// 未触发/熔断/不值得压均快速返回；压缩失败不阻塞主循环（熔断计数）。
func (c *Compactor) Maybe(ctx context.Context, provider llm.Provider) *Outcome {
	out := &Outcome{}
	if c == nil || c.circuitOpen {
		return out
	}
	raw := c.store.RawHistory()
	tokens := lastPromptTokens(raw)
	if tokens <= 0 {
		return out // 首轮或无实测信号
	}
	window := c.window()
	if window <= 0 {
		window = c.cfg.DefaultWindow
	}
	if float64(tokens) < float64(window)*c.cfg.Threshold {
		return out
	}
	out.Triggered = true
	out.BeforeTokens = tokens
	if err := c.compress(ctx, provider, raw, tokens, out); err != nil {
		out.Err = err
		c.failures++
		if c.failures >= c.cfg.MaxFailures {
			c.circuitOpen = true
			slog.Warn("compression circuit opened", "failures", c.failures)
		}
		slog.Warn("compression failed", "error", err, "failures", c.failures)
		return out
	}
	c.failures = 0
	return out
}

// CircuitOpen 报告熔断状态（测试与期 C 事件用）。
func (c *Compactor) CircuitOpen() bool { return c.circuitOpen }

// compress 执行管线五步（02 篇 §5；任一步失败整体放弃，内存态不变）。
func (c *Compactor) compress(ctx context.Context, provider llm.Provider, raw []*schema.Message, beforeTokens int, out *Outcome) error {
	started := time.Now()

	// ① 定界
	var curCutoff string
	var oldEntries []*ArchiveEntry
	if cur := c.store.Compaction(); cur != nil {
		curCutoff = cur.CutoffMessageID
		oldEntries = cur.Entries
	}
	batch, newCutoff, ok := c.sel.Select(raw, curCutoff)
	if !ok {
		return nil // 不值得压：不算失败
	}

	// ② 归档：原文先落盘（崩溃窗口见 03 篇 §3）
	label := RangeLabel(raw, batch)
	archivePath, err := c.store.WriteArchive(label, RenderArchive(batch))
	if err != nil {
		return err
	}

	// ③ 提取（旧条目尾部 2 条供增量衔接）
	tail := oldEntries
	if len(tail) > 2 {
		tail = tail[len(tail)-2:]
	}
	relPointer := filepath.ToSlash(filepath.Join("archive", filepath.Base(archivePath)))
	entries, err := c.ext.Extract(ctx, provider, &ExtractRequest{
		Prefix: raw, Batch: batch, OldTail: tail, Range: label, Pointer: relPointer,
	})
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return fmt.Errorf("extractor returned no entries")
	}

	// ③.5 标识符校验（02 篇 §6 红线 2）：压缩集标识符 ⊆ 新条目 ∪ 保留区文本
	retained := raw[indexOfID(raw, newCutoff)+1:]
	if missing := MissingIdentifiers(batch, entries, retained); len(missing) > 0 {
		return fmt.Errorf("identifier check failed, missing: %s", strings.Join(missing, ", "))
	}

	for _, e := range entries {
		if e.Range == "" {
			e.Range = label
		}
		if e.Pointer == "" {
			e.Pointer = relPointer
		}
	}

	// ④ 写回（先盘后内存由 store 保证）
	merged := make([]*ArchiveEntry, 0, len(oldEntries)+len(entries))
	merged = append(merged, oldEntries...)
	merged = append(merged, entries...)
	next := &Compaction{
		Entries:         merged,
		CutoffMessageID: newCutoff,
		Stats: &CompactionStats{
			BeforeTokens: beforeTokens,
			AfterTokens:  estimateProjectedTokens(merged, raw, newCutoff),
			DurationMs:   time.Since(started).Milliseconds(),
			At:           time.Now().UnixMilli(),
		},
	}
	if err := c.store.ApplyCompaction(next); err != nil {
		return err
	}

	// ⑤ 一致性红线（02 篇 §5.1）：清除被压 skill 的 loaded 记录
	for _, name := range LoadSkillCalls(batch) {
		c.store.UnmarkSkillLoaded(name)
	}

	out.Compressed = true
	out.Entries = len(merged)
	out.AfterTokens = next.Stats.AfterTokens
	return nil
}
