package session

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"tars/pkg/event"
	"tars/pkg/llm"
	"tars/pkg/schema"
	"tars/pkg/tool/guard"

	"github.com/google/uuid"
)

// Manager 是会话的存储与工厂：目录布局、jsonl 快照、meta.json，
// 以及 Info 的创建与恢复。会话的持久化细节全部封装在本包，
// 外部（boot）只面对 Info 与本类型的少量方法。
// Store 为普通对象，由装配层（boot）创建并注入。
type Manager struct {
	// 会话的数据
	data *Data

	// risks 是"本会话常允许"的危险操作常允许表（内存态，重启清空），
	// 由 guard.Gate 消费；会话级载体，跨轮共享。
	risks *guard.RiskTable
	// sink 事件出口：消息追加时发射 KindMessageAppended（非序列化）。
	sink event.Sink

	llmMgr      *llm.Manager
	selector    Selector
	extractor   Extractor
	failures    int  // 连续失败计数
	circuitOpen bool // 熔断：true 后 Maybe 短路（02 篇 §9）
	// lastActedTokens 已据其做过减量的实测用量：同一个信号只作用一次
	// （实测值滞后一次请求，见 MaybeCompress）。
	lastActedTokens int

	Threshold   float64 // 触发阈值（0-1），默认 0.8
	KeepTurns   int     // 尾部保留完整轮数，默认 6
	MinBatch    int     // 最小压缩集消息数，默认 8
	MaxFailures int     // 熔断阈值（连续失败次数），默认 3
	// MaxFullEntries 保留完整形态的归档条目数上限（更旧的退化为存根）。
	// MaxEntries 归档条目总数上限（更旧的存根直接丢弃）。
	// 二者是归档区自身的膨胀闸门，不进 yaml/前端配置面——调它们需要同时
	// 理解存根语义，属实现细节而非用户旋钮。
	MaxFullEntries int
	MaxEntries     int
}

// 归档区膨胀闸门的默认值（见 Manager.MaxFullEntries / MaxEntries）。
// 量级取舍：20 条完整条目约 4k token，是长会话下可接受的常驻开销；
// 120 条存根约 2.4k token，越过它的会话已有数百轮，最旧的存根实际价值趋零。
const (
	DefaultMaxFullEntries = 20
	DefaultMaxEntries     = 120
)

// NewManager 创建会话存储；workDir 为应用工作目录根，sink 注入每个
// 创建/恢复出的会话（消息追加时发射事件），nil 时静默。
//
// WorkspaceDir 在此解析完毕（旧 meta 缺失时回填默认值并持久化）：
// Controller 紧接着用 GetWorkspaceDir() 构造 sandbox，根必须在那时就绪。
func NewManager(data *Data, sink event.Sink, llmMgr *llm.Manager, threshold float64, keepTurns int, minBatch int, maxFailures int) *Manager {
	if data.WorkspaceDir == "" {
		data.WorkspaceDir = GetWorkspaceDir(instance.GetWorkDir(), data.ID)
		if err := instance.SaveMetadata(data.ID, data.Metadata); err != nil {
			slog.Warn("Failed to persist workspaceDir backfill", "id", data.ID, "error", err)
		}
	}
	return &Manager{
		data:           data,
		risks:          guard.NewRiskTable(),
		sink:           sink,
		llmMgr:         llmMgr,
		selector:       NewTailKeepSelector(keepTurns, minBatch),
		extractor:      NewLLMExtractor(),
		Threshold:      threshold,
		KeepTurns:      keepTurns,
		MinBatch:       minBatch,
		MaxFailures:    maxFailures,
		MaxFullEntries: DefaultMaxFullEntries,
		MaxEntries:     DefaultMaxEntries,
	}
}

// Startup 会话级启动钩子：仅负责工作目录的磁盘创建（路径回填已在
// NewManager 完成）。策略：存储类目录由 StoreManager 写路径惰性自闭合；
// 工作目录在初始创建与恢复加载时主动创建一次——Controller.Startup
// 在这两个生命周期点都会调到这里。
func (s *Manager) Startup() error {
	def := GetWorkspaceDir(instance.GetWorkDir(), s.data.ID)
	// 仅默认位置自动创建——自定义目录（SetWorkspaceDir 指向的用户项目）
	// 不自动创建，避免掩盖目录已被删除的事实。
	if s.data.WorkspaceDir == def {
		if err := os.MkdirAll(def, 0755); err != nil {
			return fmt.Errorf("session: create workspace dir for %s: %w", s.data.ID, err)
		}
	}
	return nil
}

func (s *Manager) Shutdown() error {
	return nil
}

func (s *Manager) GetID() string {
	if s.data != nil {
		return s.data.ID
	}
	return ""
}

func (s *Manager) GetData() *Data {
	return s.data
}

func (s *Manager) GetBaseDir() string {
	return GetBaseDir(instance.GetWorkDir())
}

func (s *Manager) GetSessionDir() string {
	return GetSessionDir(instance.GetWorkDir(), s.data.ID)
}

func (s *Manager) GetDataDir() string {
	return GetDataDir(instance.GetWorkDir(), s.data.ID)
}

func (s *Manager) GetWorkspaceDir() string {
	return s.data.WorkspaceDir
}

// SetWorkspaceDir 设置工作目录。**仅允许在有任何对话消息之前调用**——
// 一旦有消息即锁定（01 篇配套决策）：
//
// 历史消息里含相对旧根的路径与文件内容，改目录后模型照着旧路径继续操作全错，
// 而它无从察觉。故不追踪、不迁移，直接锁定。
//
// 守卫在会话层而非仅靠前端禁用按钮（否则"点开目录"与"发消息"并发时仍会漏），
// 且不用「轮运行中」而是「有消息」——前者是瞬态（点选与轮启动之间有个窗口），
// 后者是静态的：SubmitMessage 先 AppendUserMessage 再启动轮（controller），
// 零消息 ⇒ 从未启动过任何轮，无竞态。
func (s *Manager) SetWorkspaceDir(dir string) error {
	if len(s.data.Messages) > 0 {
		return fmt.Errorf("会话已有对话记录，工作目录已锁定；如需在新目录下工作，请新建会话")
	}
	// 自定义目录必须已存在（sandbox 以其为 chdir 目标；不存在时命令执行
	// 必然失败，提前在此暴露而不是延迟到工具执行时）。
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return fmt.Errorf("workspace 目录不存在: %s", dir)
	}
	s.data.WorkspaceDir = dir
	s.data.UpdatedAt = time.Now().UnixMilli()

	// 不再广播 workspace:changed：锁定后只在零消息窗口内变更，那次是前端
	// 主动调用、拿返回值即可同步，广播通道没有订阅者价值。
	return instance.SaveMetadata(s.data.ID, s.data.Metadata)
}

// RiskTable 返回会话级常允许表（惰性创建；重启清空）。
// 装配工具权限门（guard.NewGate）时注入。
func (s *Manager) RiskTable() *guard.RiskTable {
	return s.risks
}

func (s *Manager) RenameSession(title string) error {
	return s.SetTitle(title)
}

// --- 会话生命周期（Info 的创建/恢复/删除） ---

// --- 消息持久化（jsonl 快照日志；Info 内部使用） ---

// AppendUserMessage 新一轮对话的消息准备：追加 user 消息，首条消息顺便完成自动命名。
// 返回新建 user 消息的 ID（服务层透传给前端回填本地占位）。
// assistant 消息不预置——交错式存储，由轮运行中每次迭代经 AppendMessage 追加。
func (s *Manager) AppendUserMessage(content string) string {
	now := time.Now().UnixMilli()
	id := uuid.NewString()
	msg := &schema.Message{
		ID:        id,
		Role:      schema.RoleUser,
		TurnID:    id, // 新轮起点：user 消息的 TurnID 即自身 ID
		Content:   content,
		CreatedAt: now,
	}
	s.AppendMessage(now, msg)

	v := s.data.UpdateTitle(content)
	if v {
		err := instance.SaveMetadata(s.data.ID, s.data.Metadata)
		if err != nil {
			slog.Warn("Failed to save session meta", "id", s.data.ID, "error", err)
		}
		// 自动命名与手动改名（SetTitle）同一事件通道——前端列表靠它即时
		// 刷新标题；漏发时标题要重启重新 listSessions 才显示（曾经如此）。
		s.sink.Emit(event.Event{
			Kind:           event.KindSessionRenamed,
			SessionRenamed: &event.SessionRenamedEvent{SessionID: s.data.ID, Title: s.data.Title},
		})
	}
	return id
}

// PrepareRetry 重试的消息准备：截断到目标轮的 user 消息，全量覆写持久化。
// messageID 指定目标 assistant 消息（取其前最近的 user）；空 = 截断到
// 最后一条 user 消息（涵盖"最后一轮 assistant"与"上一轮未产出"两种情形）。
// 返回该轮的 user 消息内容（trace 展示用）。
func (s *Manager) PrepareRetry(messageID string) (string, error) {
	userText, err := s.data.PrepareRetry(messageID)
	if err == nil {
		s.invalidateCompactionIfCutoffLost("retry crosses cutoff")
	}

	wErr := instance.RewriteMessages(s.data.ID, s.data.Messages)
	if wErr != nil {
		return "", wErr
	}
	return userText, err
}

// DeleteFrom 删除 messageID 及其后全部消息（截断语义），返回被删消息的原下标。
// 轮运行中禁止（服务层已 guard）。
func (s *Manager) DeleteFrom(messageID string) (int, error) {
	idx, err := s.data.DeleteFrom(messageID)
	if err != nil {
		return idx, err
	}

	s.invalidateCompactionIfCutoffLost("delete crosses cutoff")
	return idx, instance.RewriteMessages(s.data.ID, s.data.Messages)
}

// EditUserMessage 就地编辑一条 user 消息的内容（不触发重新生成）。
func (s *Manager) EditUserMessage(messageID, content string) error {
	// 编辑压缩区内消息（原文已归档）→ 压缩态整体作废（03 篇 §4：保守一致）。
	if c := s.data.Compaction; c != nil && c.CutoffMessageID != "" {
		cutIdx, _ := s.data.FindMessage(c.CutoffMessageID)
		editIdx, _ := s.data.FindMessage(messageID)
		if cutIdx >= 0 && editIdx >= 0 && editIdx <= cutIdx {
			s.invalidateCompaction("edit crosses cutoff")
		}
	}

	err := s.data.EditUserMessage(messageID, content)
	if err != nil {
		return err
	}

	return instance.RewriteMessages(s.data.ID, s.data.Messages)
}

// SetTitle 重命名会话（内存 + 磁盘 meta）；事件通知由服务层负责。
func (s *Manager) SetTitle(title string) error {
	s.data.SetTitle(title)

	err := instance.SaveMetadata(s.data.ID, s.data.Metadata)
	if err != nil {
		slog.Warn("Failed to save session meta", "id", s.data.ID, "error", err)
	}

	s.sink.Emit(event.Event{
		Kind:           event.KindSessionRenamed,
		SessionRenamed: &event.SessionRenamedEvent{SessionID: s.data.ID, Title: title},
	})

	return nil
}

// AppendMessage 追加消息：内存列表 + jsonl 持久化 + 事件通知，一处完成。
// 轮分组键（TurnID）在此统一盖章——调用方（agent 循环）只管迭代序号，
// 不需要知道"轮"是怎么划分的（划分规则见 turn.go）。
func (s *Manager) AppendMessage(updateAt int64, msg ...*schema.Message) {
	// 按序推进轮键：批内出现 user 消息即开新轮（它自身 ID 就是新键），
	// 其后的 assistant/tool 归属该轮。单条追加是本逻辑的特例。
	turnID := s.data.CurrentTurnID()
	for _, m := range msg {
		if m.Role == schema.RoleUser {
			if m.TurnID == "" {
				m.TurnID = m.ID
			}
			turnID = m.TurnID
			continue
		}
		if m.TurnID == "" {
			m.TurnID = turnID
		}
	}
	s.data.AppendMessage(updateAt, msg...)

	err := instance.AppendSaveMessage(s.data.ID, msg...)
	if err != nil {
		slog.Warn("Failed to store message", "id", s.data.ID, "error", err)
	}
	for _, m := range msg {
		EmitMessageAppended(s.sink, s.data.ID, m)
	}
}

func (s *Manager) History() []*schema.Message {
	return s.data.History()
}

// --- 压缩态与压缩管线（plan/context 02/03 篇） ---

// MaybeCompress 压缩触发点（agent.Session 接口）：迭代组装消息前调用，
// 达到阈值则执行一次完整压缩；未触发/熔断/不值得压均快速返回。
func (s *Manager) MaybeCompress(ctx context.Context, provider llm.Provider) {
	if s == nil || s.circuitOpen {
		return
	}
	raw := s.data.RawHistory()
	tokens := lastPromptTokens(raw)
	if tokens <= 0 {
		return // 首轮或无实测信号
	}
	// ContextWindow 在 llm 配置构建期（Config.Validate）已回填默认值，
	// 此处无需兜底；为 0 只可能是"尚未配置激活模型"——那时轮跑不起来，
	// 防御性跳过。
	window := s.llmMgr.ContextWindow()
	if window <= 0 {
		return
	}
	budget := int(float64(window) * s.Threshold)
	if tokens < budget {
		return
	}
	// 实测用量来自上一次请求，本轮已生效的压缩要等下一次请求才反映进去。
	// 同一个信号只允许作用一次，否则阶梯会连续压好几次：Normal 档压完后
	// cutoff 前移，Normal 自身确实切不出新批次（cut <= start），但 High
	// 档保留更少轮次，"cutoff 之后"重新出现可压区间 → 又压一次，每次都是
	// 一次 LLM 调用 + 一次缓存重建。见 TestSameUsageSignalActsOnce。
	if tokens == s.lastActedTokens {
		return
	}

	// ① 定界（纯内存，无副作用）：不值得压时静默返回——不发事件，
	//    避免产生没有 Done/Failed 配对的悬挂 span。
	plan, ok := s.planCompaction(raw, budget)
	if !ok {
		return
	}

	s.sink.Emit(event.Event{Kind: event.KindCompressionStarted, CompressionStarted: &event.CompressionStartedEvent{
		SessionID: s.GetID(), TriggerTokens: tokens, Budget: budget,
	}})
	if plan.pressure != PressureNormal {
		// 常规档压不回预算：保留区被强制收窄。持续出现说明单轮体量过大
		// （多个满额工具输出），需要的是工具侧减量，不是压缩侧加压。
		slog.Warn("compression degraded", "pressure", plan.pressure.String(),
			"tokens", tokens, "budget", budget)
	}
	newEntries, totalEntries, stats, err := s.compress(ctx, provider, raw, plan, tokens)
	if err != nil {
		// 用户取消（或轮超时）不是压缩故障：不计数、不熔断。否则用户在压缩
		// 期间连按三次停止，本会话压缩就被永久禁用了——而下一轮上下文只会
		// 更大，届时压缩恰恰最该工作。仍发 Failed 事件以配对已发出的
		// Started（避免悬挂 span），只是不污染熔断计数。
		if ctx.Err() != nil {
			slog.Info("compression aborted", "session", s.GetID(), "reason", ctx.Err())
			s.sink.Emit(event.Event{Kind: event.KindCompressionFailed, CompressionFailed: &event.CompressionFailedEvent{
				SessionID: s.GetID(), Error: ctx.Err().Error(),
				Failures: s.failures, CircuitOpen: s.circuitOpen,
			}})
			return
		}
		s.failures++
		// 提取调用自身超窗口时立即熔断，不等攒够 MaxFailures：提取的输入是
		// 「完整轨迹 + 指令」，比常规请求更大，重试必然同样失败——每次都白付
		// 一次全量 prefill。这类错误不是瞬态故障，重试没有意义。
		if llm.IsContextOverflow(err) {
			s.circuitOpen = true
			slog.Warn("compression circuit opened: extractor input exceeds the model window", "error", err)
		} else if s.failures >= s.MaxFailures {
			s.circuitOpen = true
			slog.Warn("compression circuit opened", "failures", s.failures)
		}
		slog.Warn("compression failed", "error", err, "failures", s.failures)
		s.sink.Emit(event.Event{Kind: event.KindCompressionFailed, CompressionFailed: &event.CompressionFailedEvent{
			SessionID: s.GetID(), Error: err.Error(),
			Failures: s.failures, CircuitOpen: s.circuitOpen,
		}})
		return
	}
	s.failures = 0
	s.lastActedTokens = tokens

	s.sink.Emit(event.Event{Kind: event.KindCompressionDone, CompressionDone: &event.CompressionDoneEvent{
		SessionID: s.GetID(), BeforeTokens: tokens, AfterTokens: stats.AfterTokens,
		NewEntries: newEntries, TotalEntries: totalEntries, DurationMs: stats.DurationMs,
	}})

	// 压完（含极限档）仍超预算：说明**单轮自己**就超窗口。此处刻意不做
	// 静默劣化——剪当前轮的内容就是剪模型正在使用的信息，它会基于残缺
	// 上下文继续跑，产出"看起来完成了、实际是错的"结果，用户无从察觉。
	// 让请求照发、由供应商拒绝（错误经 KindError 透出），语义是诚实的失败：
	// 用户得到的信息是"任务对当前窗口太大，请拆分或开新会话"，可操作。
	// 与熔断同一哲学（§9：发事件通知宿主升级，而非降低质量硬撑）。
	if stats.AfterTokens > budget {
		slog.Warn("context still over budget after compaction; a single turn exceeds the window",
			"afterTokens", stats.AfterTokens, "budget", budget)
	}
}

// extractModelID 返回当轮提取所用模型的条目 ID（纯配置读，未配置返回空）。
func (s *Manager) extractModelID() string {
	if cfg := s.llmMgr.Config(); cfg != nil {
		if m := cfg.ActiveModel(); m != nil {
			return m.EntryID
		}
	}
	return ""
}

// compactPlan 是一次压缩的定界结果（planCompaction 的产物，纯内存）。
type compactPlan struct {
	batch      []*schema.Message // 本次压缩集
	newCutoff  string            // 新压缩边界（压缩集末条消息 ID）
	oldEntries []*ArchiveEntry   // 既有归档条目（增量衔接用）
	pressure   Pressure          // 采用的压力档位（观测用）
}

// planCompaction 计算压缩边界；ok=false 表示不值得压（压缩集过小或无
// 足够轮次可切），调用方应静默放弃——不算失败，不发事件。
//
// 降级阶梯：常规档优先；若按该档压完、保留区 + 归档区仍超预算，逐级放宽
// 保留区（Pressure）。全档位都压不回预算时用最激进的那档尽力而为——
// 此时不压的后果是请求超长被供应商拒绝、整轮失败。
func (s *Manager) planCompaction(raw []*schema.Message, budget int) (*compactPlan, bool) {
	var curCutoff string
	var oldEntries []*ArchiveEntry

	cur := s.GetCompaction()
	if cur != nil {
		curCutoff = cur.CutoffMessageID
		oldEntries = cur.Entries
	}
	var best *compactPlan
	for _, p := range Pressures {
		batch, newCutoff, ok := s.selector.Select(raw, curCutoff, p)
		if !ok {
			continue
		}
		best = &compactPlan{batch: batch, newCutoff: newCutoff, oldEntries: oldEntries, pressure: p}
		// 估算按本档压完的视图规模。本次新条目尚未提取（还没调 LLM），
		// 故只计旧条目——估值偏小约 200 token/条，可接受：阶梯只用于选档，
		// 偏小意味着更倾向温和档，不会过度激进。
		if estimateProjectedTokens(oldEntries, raw, newCutoff) <= budget {
			return best, true
		}
	}
	return best, best != nil
}

// capEntries 约束归档区自身的体量（两级，均作用于最旧端）。
// 归档区是 append-only 的，不设上限则长会话下它会线性膨胀，最终自己吃掉预算。
//
//	超出 MaxFullEntries：退化为存根（省约九成体量，保住 pointer 可发现性）
//	超出 MaxEntries    ：存根整条丢弃（末端兜底；原文仍在 messages.jsonl
//	                     与 archive/*.md，只是模型不再被告知其存在）
func (s *Manager) capEntries(entries []*ArchiveEntry) []*ArchiveEntry {
	out := entries
	if n := len(out) - s.MaxFullEntries; s.MaxFullEntries > 0 && n > 0 {
		out = append([]*ArchiveEntry(nil), entries...)
		for i := 0; i < n; i++ {
			// 拷贝后再退化：条目指针与既有内存态共享，就地改会让后续
			// 步骤失败时无法回滚（compress 承诺"任一步失败内存态不变"）。
			stub := *out[i]
			stub.Stub()
			out[i] = &stub
		}
	}
	if s.MaxEntries > 0 && len(out) > s.MaxEntries {
		out = out[len(out)-s.MaxEntries:]
	}
	return out
}

// compress 执行管线②-⑤（归档/提取/校验/写回/一致性红线；01 定界已由
// planCompaction 完成）。任一步失败整体放弃，内存态不变。
// 返回：新增条目数、条目总数、本次计量。
func (s *Manager) compress(ctx context.Context, provider llm.Provider, raw []*schema.Message, plan *compactPlan, beforeTokens int) (int, int, *CompactionStats, error) {
	started := time.Now()
	batch, newCutoff, oldEntries := plan.batch, plan.newCutoff, plan.oldEntries

	// ② 归档：原文先落盘（崩溃窗口见 03 篇 §3）
	label := RangeLabel(raw, batch)
	archivePath, err := s.WriteArchive(label, RenderArchive(batch))
	if err != nil {
		return 0, 0, nil, err
	}

	// ③ 提取（旧条目尾部 2 条供增量衔接）
	tail := oldEntries
	if len(tail) > 2 {
		tail = tail[len(tail)-2:]
	}
	relPointer := ArchiveScheme + filepath.Base(archivePath)
	entries, err := s.extractor.Extract(ctx, provider, &ExtractRequest{
		Prefix: raw, Batch: batch, OldTail: tail, Range: label, Pointer: relPointer,
	})
	if err != nil {
		return 0, 0, nil, err
	}
	if len(entries) == 0 {
		return 0, 0, nil, fmt.Errorf("extractor returned no entries")
	}

	for _, e := range entries {
		if e.Range == "" {
			e.Range = label
		}
		if e.Pointer == "" {
			// pointer 是"摘要不足时读回原文"的唯一逃生通道：必须非空。
			e.Pointer = relPointer
		}
	}

	// ③.5 经济性校验：新条目必须比它替代的那批消息更小。
	//
	// 不做内容正确性校验（标识符回查等）——字符串层面能验证的只是"词汇
	// 合法性"，而真正在意的是语义正确性，两者之间没有桥；业界十个 harness
	// 也无一例外只靠提示词约束保真。我们的保障来自可找回性：条目带
	// pointer → archive/*.md，且 messages.jsonl 永久保留全量原文。
	//
	// 但"压缩后反而更大"是可判定的硬事实：发生时应放弃，否则白付一次
	// KV Cache 重建（deepseek-harness / Reasonix / grok-build 均有此校验）。
	// 双侧同一口径（渲染后字节数）比较，避免估算偏差。
	if newBytes, oldBytes := len(renderEntries(entries)), messagesBytes(batch); newBytes >= oldBytes {
		return 0, 0, nil, fmt.Errorf("compaction not economical: entries %d bytes >= batch %d bytes", newBytes, oldBytes)
	}

	// ④ 写回（先盘后内存由 data 保证）
	merged := make([]*ArchiveEntry, 0, len(oldEntries)+len(entries))
	merged = append(merged, oldEntries...)
	merged = append(merged, entries...)
	merged = s.capEntries(merged)
	times := 1

	cur := s.GetCompaction()
	if cur != nil {
		times = cur.TimesCompressed + 1 // 累计计数随压缩态延续
	}
	next := &CompactionData{
		Entries:         merged,
		CutoffMessageID: newCutoff,
		TimesCompressed: times,
		Stats: &CompactionStats{
			BeforeTokens: beforeTokens,
			AfterTokens:  estimateProjectedTokens(merged, raw, newCutoff),
			DurationMs:   time.Since(started).Milliseconds(),
			// 提取用的是当轮 provider（同模型）。记下来供成本归因：压缩的
			// 开销单独一次全量 prefill，不记则无从归账。纯配置读，不建客户端。
			ExtractModel: s.extractModelID(),
			At:           time.Now().UnixMilli(),
		},
	}

	err = s.SetCompaction(next)
	if err != nil {
		return 0, 0, nil, err
	}

	// ⑤ 一致性红线（02 篇 §5.1）：清除被压 skill 的 loaded 记录
	for _, name := range LoadSkillCalls(batch) {
		s.UnmarkSkillLoaded(name)
	}

	return len(entries), len(merged), next.Stats, nil
}

// RawHistory 返回原始轨迹副本（不做压缩投影）——压缩器的选择/归档用。
func (s *Manager) RawHistory() []*schema.Message {
	return s.data.RawHistory()
}

// GetCompaction 返回当前压缩态（nil = 未压缩，恒等投影）。
func (s *Manager) GetCompaction() *CompactionData {
	return s.data.Compaction
}

// SetCompaction 写回压缩态：先原子落盘再改内存（03 篇红线）。
func (s *Manager) SetCompaction(c *CompactionData) error {
	if err := instance.SaveCompaction(s.data.ID, c); err != nil {
		return err
	}
	s.data.Compaction = c
	return nil
}

// WriteArchive 写入归档原文（目录创建由 StoreManager 自闭合）。
func (s *Manager) WriteArchive(rangeLabel string, content []byte) (string, error) {
	return instance.WriteArchive(s.data.ID, rangeLabel, content)
}

// ReadArchive 读取归档原文，供 read_file 的 archive:// 通道消费
// （结构式满足 toolkit.ArchiveProvider，装配层无需适配器）。
//
// 这是"摘要不够时读回原文"的落地：归档在会话数据目录、workspace 之外，
// 沙箱按设计读不到——轨迹与其派生物不该落在模型可写区域（模型误删
// messages.jsonl 不可恢复）。故不搬存储、不加工具，只给 read_file
// 多认一种路径形态。
func (s *Manager) ReadArchive(name string) ([]byte, error) {
	return instance.ReadArchive(s.data.ID, name)
}

// UnmarkSkillLoaded 从已加载技能集合移除并写穿 meta.json
// （02 篇 §5.1 一致性红线：skill 正文被压缩后调用）。
func (s *Manager) UnmarkSkillLoaded(name string) {
	s.data.UnmarkSkillLoaded(name)
	if err := instance.SaveMetadata(s.data.ID, s.data.Metadata); err != nil {
		slog.Warn("Failed to save session meta", "id", s.data.ID, "error", err)
	}
}

// invalidateCompaction 作废压缩态：先删盘再清内存（03 篇红线）。
// 归档文件保留（审计资产，不随作废删除）。
func (s *Manager) invalidateCompaction(reason string) {
	if s.data.Compaction == nil {
		return
	}
	if err := instance.DeleteCompaction(s.data.ID); err != nil {
		slog.Warn("Failed to delete compaction", "id", s.data.ID, "error", err)
	}
	s.data.Compaction = nil
	slog.Info("Compaction invalidated", "id", s.data.ID, "reason", reason)
}

// invalidateCompactionIfCutoffLost 截断类操作后调用：cutoff 消息已不在
// 轨迹中（截断进入压缩区）→ 压缩态整体作废（03 篇 §4 交互矩阵）。
func (s *Manager) invalidateCompactionIfCutoffLost(reason string) {
	c := s.data.Compaction
	if c == nil || c.CutoffMessageID == "" {
		return
	}
	if _, m := s.data.FindMessage(c.CutoffMessageID); m == nil {
		s.invalidateCompaction(reason)
	}
}

// EmitMessageAppended 发射消息追加/更新事件（sink 为空时静默）。
func EmitMessageAppended(sink event.Sink, sessionID string, m *schema.Message) {
	sink.Emit(event.Event{
		Kind:    event.KindMessageAppended,
		Message: &event.MessageAppendedEvent{SessionID: sessionID, Message: m},
	})
}

func (s *Manager) MarkSkillLoaded(name string) {
	s.data.MarkSkillLoaded(name)

	err := instance.SaveMetadata(s.data.ID, s.data.Metadata)
	if err != nil {
		slog.Warn("Failed to save session meta", "id", s.data.ID, "error", err)
	}
}

func (s *Manager) IsSkillLoaded(name string) bool {
	return s.data.IsSkillLoaded(name)
}

func (s *Manager) GetLoadedSkills() []string {
	return s.data.GetLoadedSkills()
}

func (s *Manager) MarkToolLoaded(name string) {
	s.data.MarkToolLoaded(name)

	err := instance.SaveMetadata(s.data.ID, s.data.Metadata)
	if err != nil {
		slog.Warn("Failed to save session meta", "id", s.data.ID, "error", err)
	}
}

func (s *Manager) IsToolLoaded(name string) bool {
	return s.data.IsToolLoaded(name)
}

// UnmarkToolLoaded 从已加载集合移除并写穿 meta.json
// （MCP 恢复时剔除失效条目用）。
func (s *Manager) UnmarkToolLoaded(name string) {
	s.data.UnmarkToolLoaded(name)

	err := instance.SaveMetadata(s.data.ID, s.data.Metadata)
	if err != nil {
		slog.Warn("Failed to save session meta", "id", s.data.ID, "error", err)
	}
}

func (s *Manager) GetLoadedTools() []string {
	return s.data.GetLoadedTools()
}
