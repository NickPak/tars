// Package session 管理会话的内存运行态：会话索引、消息列表、运行标记，
// 以及与磁盘持久化（pkg/store）的衔接。通过 GetManager 全局单例访问。
// 会话即状态聚合；轮的执行在 internal/turn（session 不认识 turn）。
package session

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"tars/pkg/store"

	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
)

const (
	DefaultSessionTitle       = "新对话"
	DefaultSessionTitleLength = 50
)

// Info 是会话的内存运行态：会话级元数据 + 消息列表 + 运行标记。
type Info struct {
	ID        string           `json:"id"`
	Title     string           `json:"title"`
	CreatedAt int64            `json:"createdAt"`
	UpdatedAt int64            `json:"updatedAt"`
	Messages  []*store.Message `json:"messages"`
	// Cancel 非 nil 表示有正在运行的轮（IsRunning），由 turn 包设置与清除。
	// 轮运行期间消息列表只允许尾部追加，下标稳定。
	Cancel context.CancelFunc `json:"-"`
	// AllowedRisks 记录用户选择"本会话常允许"的危险操作类别（内存态，
	// 重启清空）。键形如 "run_command:rm-recursive-force"。
	AllowedRisks map[string]bool `json:"-"`
}

// RiskAllowed 报告某类危险操作是否已被用户常允许。
func (i *Info) RiskAllowed(key string) bool {
	return i.AllowedRisks[key]
}

// AllowRisk 把某类危险操作记入常允许表（"本会话常允许此类"）。
func (i *Info) AllowRisk(key string) {
	if i.AllowedRisks == nil {
		i.AllowedRisks = make(map[string]bool)
	}
	i.AllowedRisks[key] = true
}

func NewInfo(id string) *Info {
	now := time.Now().UnixMilli()

	return &Info{
		ID:        id,
		Title:     DefaultSessionTitle,
		CreatedAt: now,
		UpdatedAt: now,
		Messages:  make([]*store.Message, 0, 16),
	}
}

// AppendUserTurn 新一轮对话的消息准备：追加 user 消息与空 assistant 锚点
// （流式输出的落点），首条消息顺便完成自动命名。
// 返回后尾部即本轮锚点，交给 turn.Start 执行。
func (i *Info) AppendUserTurn(content string) {
	now := time.Now().UnixMilli()
	i.AppendMessage(now,
		&store.Message{
			ID:        uuid.NewString(),
			Role:      schema.User,
			Content:   content,
			CreatedAt: now,
		},
		&store.Message{
			ID:        uuid.NewString(),
			Role:      schema.Assistant,
			CreatedAt: now,
		},
	)
	i.updateTitle(content)
}

// PrepareRetry 重试的消息准备：截断到目标轮（messageID 空 = 最后一轮
// assistant）的 user 消息，补一条新的空 assistant 锚点，全量覆写持久化。
// 返回该轮的 user 消息内容（trace 展示用）。
func (i *Info) PrepareRetry(messageID string) (string, error) {
	if len(i.Messages) == 0 {
		return "", fmt.Errorf("no messages to retry")
	}

	targetAssistant := -1
	if messageID == "" {
		for k := len(i.Messages) - 1; k >= 0; k-- {
			if i.Messages[k].Role == schema.Assistant {
				targetAssistant = k
				break
			}
		}
	} else {
		for k, m := range i.Messages {
			if m.ID == messageID && m.Role == schema.Assistant {
				targetAssistant = k
				break
			}
		}
	}
	if targetAssistant == -1 {
		return "", fmt.Errorf("no assistant message found to retry")
	}

	userIndex := -1
	for k := targetAssistant - 1; k >= 0; k-- {
		if i.Messages[k].Role == schema.User {
			userIndex = k
			break
		}
	}
	if userIndex == -1 {
		return "", fmt.Errorf("no previous user message found to retry")
	}

	i.Messages = i.Messages[:userIndex+1]
	userText := i.Messages[userIndex].Content

	now := time.Now().UnixMilli()
	i.Messages = append(i.Messages, &store.Message{
		ID:        uuid.NewString(),
		Role:      schema.Assistant,
		CreatedAt: now,
	})
	i.UpdatedAt = now

	if err := store.GetSessionStore().RewriteMessages(i.ID, i.Messages); err != nil {
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
		return idx, store.GetSessionStore().RewriteMessages(i.ID, i.Messages)
	}
	return -1, fmt.Errorf("message not found: %s", messageID)
}

// EditUserMessage 就地编辑一条 user 消息的内容（不触发重新生成）。
func (i *Info) EditUserMessage(messageID, content string) error {
	for _, m := range i.Messages {
		if m.ID != messageID {
			continue
		}
		if m.Role != schema.User {
			break
		}
		m.Content = content
		i.UpdatedAt = time.Now().UnixMilli()
		return store.GetSessionStore().RewriteMessages(i.ID, i.Messages)
	}
	return fmt.Errorf("message not found or not editable: %s", messageID)
}

// SetTitle 重命名会话（内存 + 磁盘 meta）；事件通知由服务层负责。
func (i *Info) SetTitle(title string) error {
	i.Title = title
	i.UpdatedAt = time.Now().UnixMilli()
	return store.GetSessionStore().SaveMeta(i.ID, &store.SessionMeta{
		Title:     i.Title,
		CreatedAt: i.CreatedAt,
		UpdatedAt: i.UpdatedAt,
	})
}

// AppendMessage 追加消息：内存列表 + jsonl 持久化，一处完成。
func (i *Info) AppendMessage(updateAt int64, msg ...*store.Message) {
	i.Messages = append(i.Messages, msg...)
	i.UpdatedAt = updateAt

	if err := store.GetSessionStore().AppendMessage(i.ID, msg...); err != nil {
		slog.Warn("Failed to store message", "id", i.ID, "error", err)
	}
}

// IsRunning 报告会话是否有正在进行的轮。
func (i *Info) IsRunning() bool {
	return i.Cancel != nil
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

	if err := store.GetSessionStore().SaveMeta(i.ID, &store.SessionMeta{
		Title:     i.Title,
		CreatedAt: i.CreatedAt,
		UpdatedAt: i.UpdatedAt,
	}); err != nil {
		slog.Warn("Failed to save session meta", "id", i.ID, "error", err)
	}
}
