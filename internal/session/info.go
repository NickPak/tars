// Package session 定义会话的完整封装：内存运行态（Info：消息列表、
// 运行标记）与持久化（Store：目录布局、jsonl 快照、meta.json，
// 以及 Info 的创建/恢复/删除）。会话是消息历史的单一事实来源：
// agent 循环经 Session 接口写入，下游经事件流订阅。
// 会话的索引管理在 internal/boot（Controller 持有各自的 Info）。
package session

import (
	"cmp"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"tars/internal/event"
	"tars/pkg/schema"
	"tars/pkg/tools"

	"github.com/google/uuid"
)

const (
	DefaultSessionTitle       = "新对话"
	DefaultSessionTitleLength = 50
)

// Info 是会话的内存数据：会话级元数据 + 消息列表 + 会话级幂等状态。
// 轮的运行态（取消标记）在 boot.Controller——goroutine 在那里创建。
type Info struct {
	ID        string           `json:"id"`
	Title     string           `json:"title"`
	CreatedAt int64            `json:"createdAt"`
	UpdatedAt int64            `json:"updatedAt"`
	Messages  []*schema.Message `json:"messages"`
	// risks 是"本会话常允许"的危险操作常允许表（内存态，重启清空），
	// 由 tools.Gate 消费；会话级载体，跨轮共享。
	risks *tools.RiskTable
	// LoadedSkills 记录本会话已 load_skill 的技能（内存态，跨轮幂等，
	// 重启清空）。状态栏据此展示"已加载技能"。
	LoadedSkills map[string]bool `json:"-"`
	// store 会话持久化后端，由 Store 创建/恢复会话时注入（非序列化）。
	store *Store `json:"-"`
	// sink 事件出口：消息追加/聚合更新时发射 KindMessageAppended（非序列化）。
	sink event.Sink `json:"-"`
}

// emit 发射消息追加/更新事件（sink 为空时静默）。
func (i *Info) emit(m *schema.Message) {
	if i.sink != nil {
		i.sink.Emit(event.Event{
			Kind:    event.KindMessageAppended,
			Message: &event.MessageAppendedEvent{SessionID: i.ID, Message: m},
		})
	}
}

// RiskTable 返回会话级常允许表（惰性创建；重启清空）。
// 装配工具权限门（tools.NewGate）时注入。
func (i *Info) RiskTable() *tools.RiskTable {
	if i.risks == nil {
		i.risks = tools.NewRiskTable()
	}
	return i.risks
}

// IsSkillLoaded 报告技能是否已在本会话加载。
func (i *Info) IsSkillLoaded(name string) bool {
	return i.LoadedSkills[name]
}

// MarkSkillLoaded 标记技能已在本会话加载（幂等）。
func (i *Info) MarkSkillLoaded(name string) {
	if i.LoadedSkills == nil {
		i.LoadedSkills = make(map[string]bool)
	}
	i.LoadedSkills[name] = true
}

// LoadedSkillNames 返回已加载技能名（排序后），供状态栏展示。
func (i *Info) LoadedSkillNames() []string {
	names := make([]string, 0, len(i.LoadedSkills))
	for n := range i.LoadedSkills {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

// NewInfo 创建新会话的内存态；store/sink 由 Store.Create 注入。
// 恢复路径不经此函数（Store.RestoreAll 直接字面量构造）。
func NewInfo(id string, st *Store, sink event.Sink) *Info {
	now := time.Now().UnixMilli()

	return &Info{
		ID:        id,
		Title:     DefaultSessionTitle,
		CreatedAt: now,
		UpdatedAt: now,
		Messages:  make([]*schema.Message, 0, 16),
		store:     st,
		sink:      sink,
	}
}

// SortByCreatedAt 按创建时间升序排序会话列表（boot.App.ListSessions 使用）。
func SortByCreatedAt(list []*Info) {
	slices.SortFunc(list, func(a, b *Info) int {
		return cmp.Compare(a.CreatedAt, b.CreatedAt)
	})
}

// AppendUser 新一轮对话的消息准备：追加 user 消息，首条消息顺便完成自动命名。
// assistant 消息不再预置——由轮运行中首轮产出时经 UpsertAssistant 创建。
func (i *Info) AppendUser(content string) {
	now := time.Now().UnixMilli()
	i.AppendMessage(now,
		&schema.Message{
			ID:        uuid.NewString(),
			Role:      schema.RoleUser,
			Content:   content,
			CreatedAt: now,
		},
	)
	i.updateTitle(content)
}

// UpsertAssistant 把一轮迭代的 assistant 产出聚合进指定 ID 的消息：
// 不存在则创建（本轮首轮产出），存在则按增量聚合（Content/Reasoning 拼接、
// ToolCalls 追加、Parts 按迭代追加一片），并写 jsonl 快照（按消息 ID 去重，
// 崩溃安全：部分进度不丢）。
func (i *Info) UpsertAssistant(id string, delta *schema.Message) {
	now := time.Now().UnixMilli()
	var m *schema.Message
	for _, x := range i.Messages {
		if x.ID == id {
			m = x
			break
		}
	}
	if m == nil {
		m = &schema.Message{ID: id, Role: schema.RoleAssistant, CreatedAt: now}
		i.Messages = append(i.Messages, m)
	}

	m.Content += delta.Content
	m.Reasoning += delta.Reasoning
	part := schema.MessagePart{Content: delta.Content}
	if len(delta.ToolCalls) > 0 {
		m.ToolCalls = append(m.ToolCalls, delta.ToolCalls...)
		part.ToolCalls = append(part.ToolCalls, delta.ToolCalls...)
	}
	m.Parts = append(m.Parts, part)
	i.UpdatedAt = now

	if err := i.store.AppendMessage(i.ID, m); err != nil {
		slog.Warn("Failed to snapshot assistant message", "id", i.ID, "error", err)
	}
	i.emit(m)
}

// FinalizeAssistant 把一轮的 token 用量与总耗时写回 assistant 消息并快照，
// 历史会话重新打开后每条消息的用量信息才能恢复。
// 消息不存在（一轮未产出，如首轮即失败）时静默跳过。
func (i *Info) FinalizeAssistant(id string, usage *schema.UsageInfo, elapsedMs int64) {
	for _, m := range i.Messages {
		if m.ID != id {
			continue
		}
		m.Usage = usage
		m.ElapsedMs = elapsedMs
		i.UpdatedAt = time.Now().UnixMilli()
		if err := i.store.AppendMessage(i.ID, m); err != nil {
			slog.Warn("Failed to store message", "id", i.ID, "error", err)
		}
		i.emit(m)
		return
	}
}

// PrepareRetry 重试的消息准备：截断到目标轮的 user 消息，全量覆写持久化。
// messageID 指定目标 assistant 消息（取其前最近的 user）；空 = 截断到
// 最后一条 user 消息（涵盖"最后一轮 assistant"与"上一轮未产出"两种情形）。
// 返回该轮的 user 消息内容（trace 展示用）。
func (i *Info) PrepareRetry(messageID string) (string, error) {
	if len(i.Messages) == 0 {
		return "", fmt.Errorf("no messages to retry")
	}

	userIndex := -1
	if messageID != "" {
		targetAssistant := -1
		for k, m := range i.Messages {
			if m.ID == messageID && m.Role == schema.RoleAssistant {
				targetAssistant = k
				break
			}
		}
		if targetAssistant == -1 {
			return "", fmt.Errorf("no assistant message found to retry")
		}
		for k := targetAssistant - 1; k >= 0; k-- {
			if i.Messages[k].Role == schema.RoleUser {
				userIndex = k
				break
			}
		}
	} else {
		for k := len(i.Messages) - 1; k >= 0; k-- {
			if i.Messages[k].Role == schema.RoleUser {
				userIndex = k
				break
			}
		}
	}
	if userIndex == -1 {
		return "", fmt.Errorf("no user message found to retry")
	}

	i.Messages = i.Messages[:userIndex+1]
	userText := i.Messages[userIndex].Content
	i.UpdatedAt = time.Now().UnixMilli()

	if err := i.store.RewriteMessages(i.ID, i.Messages); err != nil {
		return "", err
	}
	return userText, nil
}

// DeleteFrom 删除 messageID 及其后全部消息（截断语义），返回被删消息的原下标。
// 轮运行中禁止（服务层已 guard）。
func (i *Info) DeleteFrom(messageID string) (int, error) {
	for idx, m := range i.Messages {
		if m.ID != messageID {
			continue
		}
		i.Messages = i.Messages[:idx]
		i.UpdatedAt = time.Now().UnixMilli()
		return idx, i.store.RewriteMessages(i.ID, i.Messages)
	}
	return -1, fmt.Errorf("message not found: %s", messageID)
}

// EditUserMessage 就地编辑一条 user 消息的内容（不触发重新生成）。
func (i *Info) EditUserMessage(messageID, content string) error {
	for _, m := range i.Messages {
		if m.ID != messageID {
			continue
		}
		if m.Role != schema.RoleUser {
			break
		}
		m.Content = content
		i.UpdatedAt = time.Now().UnixMilli()
		return i.store.RewriteMessages(i.ID, i.Messages)
	}
	return fmt.Errorf("message not found or not editable: %s", messageID)
}

// SetTitle 重命名会话（内存 + 磁盘 meta）；事件通知由服务层负责。
func (i *Info) SetTitle(title string) error {
	i.Title = title
	i.UpdatedAt = time.Now().UnixMilli()
	return i.store.SaveMeta(i.ID, &Meta{
		Title:     i.Title,
		CreatedAt: i.CreatedAt,
		UpdatedAt: i.UpdatedAt,
	})
}

// AppendMessage 追加消息：内存列表 + jsonl 持久化 + 事件通知，一处完成。
func (i *Info) AppendMessage(updateAt int64, msg ...*schema.Message) {
	i.Messages = append(i.Messages, msg...)
	i.UpdatedAt = updateAt

	if err := i.store.AppendMessage(i.ID, msg...); err != nil {
		slog.Warn("Failed to store message", "id", i.ID, "error", err)
	}
	for _, m := range msg {
		i.emit(m)
	}
}

// History 返回消息历史的副本（调用方修改不影响内部状态）。
// 轮运行期间消息列表只允许尾部追加，副本头部下标稳定。
func (i *Info) History() []*schema.Message {
	out := make([]*schema.Message, len(i.Messages))
	copy(out, i.Messages)
	return out
}

func (i *Info) updateTitle(content string) {
	if i.Title != DefaultSessionTitle {
		return
	}

	title := content
	if len(title) > DefaultSessionTitleLength {
		title = title[:DefaultSessionTitleLength]
	}
	i.Title = title

	if err := i.store.SaveMeta(i.ID, &Meta{
		Title:     i.Title,
		CreatedAt: i.CreatedAt,
		UpdatedAt: i.UpdatedAt,
	}); err != nil {
		slog.Warn("Failed to save session meta", "id", i.ID, "error", err)
	}
}
