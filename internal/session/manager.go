package session

import (
	"log/slog"
	"time"

	"tars/pkg/compaction"
	"tars/pkg/event"
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
	// sink 事件出口：消息追加/聚合更新时发射 KindMessageAppended（非序列化）。
	sink event.Sink
}

// NewManager 创建会话存储；workDir 为应用工作目录根，sink 注入每个
// 创建/恢复出的会话（消息追加时发射事件），nil 时静默。
func NewManager(data *Data, sink event.Sink) *Manager {
	return &Manager{
		data:  data,
		risks: guard.NewRiskTable(),
		sink:  sink,
	}
}

func (s *Manager) Startup() error {
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

func (s *Manager) SetWorkspaceDir(dir string) error {
	s.data.WorkspaceDir = dir
	s.data.UpdatedAt = time.Now().UnixMilli()

	err := instance.SaveMetadata(s.data.ID, s.data.Metadata)

	s.sink.Emit(event.Event{
		Kind: event.KindWorkspaceChanged,
		Workspace: &event.WorkspaceChangedEvent{
			SessionID: s.data.ID,
			Path:      dir,
			IsCustom:  true,
		},
	})
	return err
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
// assistant 消息不再预置——由轮运行中首轮产出时经 UpsertAssistant 创建。
func (s *Manager) AppendUserMessage(content string) string {
	now := time.Now().UnixMilli()
	id := uuid.NewString()
	s.data.AppendMessage(now,
		&schema.Message{
			ID:        id,
			Role:      schema.RoleUser,
			Content:   content,
			CreatedAt: now,
		},
	)

	v := s.data.UpdateTitle(content)
	if v {
		err := instance.SaveMetadata(s.data.ID, s.data.Metadata)
		if err != nil {
			slog.Warn("Failed to save session meta", "id", s.data.ID, "error", err)
		}
	}
	return id
}

// UpsertAssistant 把一轮迭代的 assistant 产出聚合进指定 ID 的消息：
// 不存在则创建（本轮首轮产出），存在则按增量聚合（Content/Reasoning 拼接、
// ToolCalls 追加、Parts 按迭代追加一片），并写 jsonl 快照（按消息 ID 去重，
// 崩溃安全：部分进度不丢）。
func (s *Manager) UpsertAssistant(id string, delta *schema.Message) {
	m := s.data.UpsertAssistant(id, delta)
	if m == nil {
		return
	}

	err := instance.AppendSaveMessage(s.data.ID, m)
	if err != nil {
		slog.Warn("Failed to snapshot assistant message", "id", s.data.ID, "error", err)
	}
	EmitMessageAppended(s.sink, s.data.ID, m)
}

// FinalizeAssistant 把一轮的 token 用量与总耗时写回 assistant 消息并快照，
// 历史会话重新打开后每条消息的用量信息才能恢复。
// 消息不存在（一轮未产出，如首轮即失败）时静默跳过。
func (s *Manager) FinalizeAssistant(id string, usage *schema.UsageInfo, elapsedMs int64) {
	m := s.data.FinalizeAssistant(id, usage, elapsedMs)
	if m == nil {
		return
	}

	err := instance.AppendSaveMessage(s.data.ID, m)
	if err != nil {
		slog.Warn("Failed to store message", "id", s.data.ID, "error", err)
	}
	EmitMessageAppended(s.sink, s.data.ID, m)
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
func (s *Manager) AppendMessage(updateAt int64, msg ...*schema.Message) {
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

// --- 压缩态（compaction，plan/context 02/03 篇：CompactStore 接口实现） ---

// RawHistory 返回原始轨迹副本（不做压缩投影）——压缩器的选择/归档用。
func (s *Manager) RawHistory() []*schema.Message {
	return s.data.RawHistory()
}

// Compaction 返回当前压缩态（nil = 未压缩，恒等投影）。
func (s *Manager) Compaction() *compaction.Compaction {
	return s.data.Compaction
}

// ApplyCompaction 写回压缩态：先原子落盘再改内存（03 篇红线）。
func (s *Manager) ApplyCompaction(c *compaction.Compaction) error {
	if err := instance.SaveCompaction(s.data.ID, c); err != nil {
		return err
	}
	s.data.Compaction = c
	return nil
}

// ArchivePath 分配归档文件路径（目录布局由 StoreManager 持有）。
func (s *Manager) ArchivePath(rangeLabel string) string {
	return instance.ArchivePath(s.data.ID, rangeLabel)
}

// UnmarkSkillLoaded 从已加载技能集合移除并写穿 meta.json
//（02 篇 §5.1 一致性红线：skill 正文被压缩后调用）。
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
