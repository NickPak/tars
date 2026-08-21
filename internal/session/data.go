// Package session 定义会话的完整封装：内存运行态（Info：消息列表、
// 运行标记）与持久化（Store：目录布局、jsonl 快照、meta.json，
// 以及 Info 的创建/恢复/删除）。会话是消息历史的单一事实来源：
// agent 循环经 Session 接口写入，下游经事件流订阅。
// 会话的索引管理在 internal/boot（Controller 持有各自的 Info）。
package session

import (
	"cmp"
	"fmt"
	"slices"
	"time"

	"tars/pkg/schema"
)

const (
	DefaultSessionTitle       = "新对话"
	DefaultSessionTitleLength = 50
)

// Metadata 是磁盘上的会话摘要。
type Metadata struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	CreatedAt    int64  `json:"createdAt"`
	UpdatedAt    int64  `json:"updatedAt"`
	WorkspaceDir string `json:"workspaceDir"`
}

func NewMetadata(id string) *Metadata {
	now := time.Now().UnixMilli()

	return &Metadata{
		ID:           id,
		Title:        DefaultSessionTitle,
		CreatedAt:    now,
		UpdatedAt:    now,
		WorkspaceDir: GetWorkspaceDir(instance.GetWorkDir(), id),
	}
}

// Data 是会话的内存数据：会话级元数据 + 消息列表 + 会话级幂等状态。
// 轮的运行态（取消标记）在 boot.Controller——goroutine 在那里创建。
type Data struct {
	*Metadata
	Messages []*schema.Message `json:"messages"`
}

// NewData 创建新会话的内存态；store/sink 由 Store.Create 注入。
// 恢复路径不经此函数（Store.RestoreAll 直接字面量构造）。
func NewData(id string) *Data {

	return &Data{
		Metadata: NewMetadata(id),
		Messages: make([]*schema.Message, 0, 16),
	}
}

// SortByCreatedAt 按创建时间升序排序会话列表（boot.App.ListSessions 使用）。
func SortByCreatedAt(list []*Data) {
	slices.SortFunc(list, func(a, b *Data) int {
		return cmp.Compare(a.CreatedAt, b.CreatedAt)
	})
}

// UpsertAssistant 把一轮迭代的 assistant 产出聚合进指定 ID 的消息：
// 不存在则创建（本轮首轮产出），存在则按增量聚合（Content/Reasoning 拼接、
// ToolCalls 追加、Parts 按迭代追加一片），并写 jsonl 快照（按消息 ID 去重，
// 崩溃安全：部分进度不丢）。
func (i *Data) UpsertAssistant(id string, delta *schema.Message) *schema.Message {
	now := time.Now().UnixMilli()
	var m *schema.Message
	_, m = i.FindMessage(id)
	if m == nil {
		m = &schema.Message{ID: id, Role: schema.RoleAssistant, CreatedAt: now}
		i.Messages = append(i.Messages, m)
	}

	m.Content += delta.Content
	m.Reasoning += delta.Reasoning
	part := schema.MessagePart{Content: delta.Content, Reasoning: delta.Reasoning}
	if len(delta.ToolCalls) > 0 {
		m.ToolCalls = append(m.ToolCalls, delta.ToolCalls...)
		part.ToolCalls = append(part.ToolCalls, delta.ToolCalls...)
	}
	m.Parts = append(m.Parts, part)
	i.UpdatedAt = now
	return m
}

// FinalizeAssistant 把一轮的 token 用量与总耗时写回 assistant 消息并快照，
// 历史会话重新打开后每条消息的用量信息才能恢复。
// 消息不存在（一轮未产出，如首轮即失败）时静默跳过。
func (i *Data) FinalizeAssistant(id string, usage *schema.UsageInfo, elapsedMs int64) *schema.Message {
	var assistant *schema.Message
	_, assistant = i.FindMessage(id)
	if assistant == nil {
		return nil
	}

	assistant.Usage = usage
	assistant.ElapsedMs = elapsedMs
	i.UpdatedAt = time.Now().UnixMilli()

	return assistant
}

// PrepareRetry 重试的消息准备：截断到目标轮的 user 消息，全量覆写持久化。
// messageID 指定目标 assistant 消息（取其前最近的 user）；空 = 截断到
// 最后一条 user 消息（涵盖"最后一轮 assistant"与"上一轮未产出"两种情形）。
// 返回该轮的 user 消息内容（trace 展示用）。
func (i *Data) PrepareRetry(messageID string) (string, error) {
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

	return userText, nil
}

// DeleteFrom 删除 messageID 及其后全部消息（截断语义），返回被删消息的原下标。
// 轮运行中禁止（服务层已 guard）。
func (i *Data) DeleteFrom(messageID string) (int, error) {
	index := -1
	index, _ = i.FindMessage(messageID)
	if index < 0 {
		return index, fmt.Errorf("message not found: %s", messageID)
	}

	i.Messages = i.Messages[:index]
	i.UpdatedAt = time.Now().UnixMilli()
	return index, nil
}

// EditUserMessage 就地编辑一条 user 消息的内容（不触发重新生成）。
func (i *Data) EditUserMessage(messageID, content string) error {
	var user *schema.Message
	_, user = i.FindMessage(messageID)
	if user == nil {
		return fmt.Errorf("message not found or not editable: %s", messageID)
	}

	if user.Role != schema.RoleUser {
		return fmt.Errorf("message not found or not editable: %s", messageID)
	}

	user.Content = content
	i.UpdatedAt = time.Now().UnixMilli()
	return nil
}

// SetTitle 重命名会话（内存 + 磁盘 meta）；事件通知由服务层负责。
func (i *Data) SetTitle(title string) {
	i.Title = title
	i.UpdatedAt = time.Now().UnixMilli()
}

// AppendMessage 追加消息：内存列表 + jsonl 持久化 + 事件通知，一处完成。
func (i *Data) AppendMessage(updateAt int64, msg ...*schema.Message) {
	i.Messages = append(i.Messages, msg...)
	i.UpdatedAt = updateAt
}

// History 返回消息历史的副本（调用方修改不影响内部状态）。
// 轮运行期间消息列表只允许尾部追加，副本头部下标稳定。
func (i *Data) History() []*schema.Message {
	out := make([]*schema.Message, len(i.Messages))
	copy(out, i.Messages)
	return out
}

func (i *Data) UpdateTitle(content string) bool {
	if i.Title != DefaultSessionTitle {
		return false
	}

	title := content
	if len(title) > DefaultSessionTitleLength {
		title = title[:DefaultSessionTitleLength]
	}
	i.Title = title
	return true
}

func (i *Data) FindMessage(messageID string) (int, *schema.Message) {
	index := -1
	var message *schema.Message
	for idx, m := range i.Messages {
		if m.ID == messageID {
			index = idx
			message = m
			break
		}
	}
	return index, message
}
