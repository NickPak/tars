// Package session 管理会话的内存运行态：会话索引、消息缓冲、取消函数，
// 以及与磁盘持久化（pkg/store）的衔接。通过 GetManager 全局单例访问。
package session

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"tars/pkg/store"
	"tars/pkg/trace"

	"github.com/google/uuid"
	oteltrace "go.opentelemetry.io/otel/trace"
)

const defaultSessionTitle = "新对话"

var (
	instance *Manager
)

// SessionState 是会话的内存运行态（会话级元数据 + 消息列表）。
type SessionState struct {
	ID        string          `json:"id"`
	Title     string          `json:"title"`
	CreatedAt int64           `json:"createdAt"`
	UpdatedAt int64           `json:"updatedAt"`
	Messages  []store.Message `json:"messages"`

	// Tracer 是会话级追踪器，生命周期与 Session 一致（创建/恢复时注入，
	// 删除/关闭时由 Manager 统一回收）。不参与 JSON 序列化。
	Tracer oteltrace.Tracer `json:"-"`
}

// Manager 管理全部会话的内存运行态。进程级单例，方法均并发安全。
type Manager struct {
	mu       sync.RWMutex
	sessions map[string]*SessionState
	cancels  map[string]context.CancelFunc
}

// InitManager 创建全局会话管理器单例；启动时调用一次。
func InitManager() *Manager {
	m := &Manager{
		sessions: make(map[string]*SessionState),
		cancels:  make(map[string]context.CancelFunc),
	}
	instance = m
	return m
}

// GetManager 返回全局会话管理器；InitManager 之前调用返回 nil。
func GetManager() *Manager { return instance }

// Restore 从磁盘恢复全部会话到内存；创建/恢复对应 tracer。
// 应在 InitManager 之后、服务就绪前调用。
func (m *Manager) Restore() error {
	summaries, err := store.GetSessionStore().ListSessions()
	if err != nil {
		return fmt.Errorf("list sessions: %w", err)
	}
	for _, sum := range summaries {
		meta, err := store.GetSessionStore().LoadMeta(sum.ID)
		if err != nil {
			slog.Warn("Failed to load meta", "id", sum.ID, "error", err)
			continue
		}
		msgs, err := store.GetSessionStore().LoadMessages(sum.ID)
		if err != nil {
			slog.Warn("Failed to load messages", "id", sum.ID, "error", err)
			continue
		}
		sess := &SessionState{
			ID:        sum.ID,
			Title:     meta.Title,
			CreatedAt: meta.CreatedAt,
			UpdatedAt: meta.UpdatedAt,
			Messages:  msgs, // store.Message 即 session.Message，直接使用
		}
		m.add(sess)
		// 为恢复的会话创建 tracer（全局 tp 已在 ServiceStartup 时 Init）
		sess.Tracer = trace.GetTracer()
	}
	slog.Info("Loaded sessions from store", "count", len(m.sessions))
	return nil
}

// Create 创建新会话（内存 + 磁盘 meta），返回会话状态。
func (m *Manager) Create() (*SessionState, error) {
	id := uuid.NewString()
	now := time.Now().UnixMilli()
	if _, err := store.GetSessionStore().CreateSession(id, defaultSessionTitle, now); err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}
	sess := &SessionState{ID: id, Title: defaultSessionTitle, CreatedAt: now, UpdatedAt: now}
	m.add(sess)
	sess.Tracer = trace.GetTracer()
	trace.LogSessionCreated(id, defaultSessionTitle)
	return sess, nil
}

// Delete 删除会话：先取消运行中的会话，再删内存、删磁盘。
// tracer 是全局单例，无需 per-session 关闭。
func (m *Manager) Delete(id string) error {
	m.Cancel(id) // 先取消运行中的会话，避免写入已删除的目录
	m.remove(id)
	return store.GetSessionStore().DeleteSession(id)
}

// List 返回全部会话的快照切片（浅拷贝，供序列化给前端）。
func (m *Manager) List() []SessionState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	list := make([]SessionState, 0, len(m.sessions))
	for _, c := range m.sessions {
		list = append(list, *c)
	}
	return list
}

// Has 报告会话是否存在于内存索引中。
func (m *Manager) Has(id string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.sessions[id]
	return ok
}

// Get 返回会话指针（调用方自行与 Manager 锁配合读写）。
func (m *Manager) Get(id string) (*SessionState, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	sess, ok := m.sessions[id]
	return sess, ok
}

// --- 取消函数管理 ---

// WithSession 先读锁找会话，找到后在写锁内执行 fn（修改该会话）。
// 避免外部在写锁内再调 Get/Has 导致死锁。
// 会话不存在时 fn 不执行，返回 false。
func (m *Manager) WithSession(id string, fn func(sess *SessionState)) bool {
	m.mu.RLock()
	sess, ok := m.sessions[id]
	m.mu.RUnlock()
	if !ok {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	fn(sess)
	return true
}

// RegisterCancel 注册会话的取消函数。
func (m *Manager) RegisterCancel(id string, cancel context.CancelFunc) {
	m.mu.Lock()
	m.cancels[id] = cancel
	m.mu.Unlock()
}

// UnregisterCancel 移除会话的取消函数。
func (m *Manager) UnregisterCancel(id string) {
	m.mu.Lock()
	delete(m.cancels, id)
	m.mu.Unlock()
}

// Cancel 触发会话的取消函数（若正在运行）。
func (m *Manager) Cancel(id string) {
	m.mu.RLock()
	cancel, ok := m.cancels[id]
	m.mu.RUnlock()
	if ok {
		cancel()
	}
}

// CancelAll 取消全部运行中的会话（进程退出时调用）。
func (m *Manager) CancelAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, cancel := range m.cancels {
		cancel()
	}
}



// --- 持久化 ---

// AppendMessage 把消息追加到会话的 messages.jsonl。
func (m *Manager) AppendMessage(sessionID string, msg store.Message) error {
	return store.GetSessionStore().AppendMessage(sessionID, msg)
}

// AppendAssistantSnapshot 把当前 assistant 消息追加到 messages.jsonl。
// JSONL 是 append-only：同一消息 ID 可能出现多次，LoadMessages 按 ID 去重
// 保留最后一条（即最新快照）。崩溃安全：部分进度不丢。
func (m *Manager) AppendAssistantSnapshot(sessionID string, index int) error {
	sess, ok := m.Get(sessionID)
	if !ok {
		return fmt.Errorf("session not found: %s", sessionID)
	}
	if index >= len(sess.Messages) {
		return fmt.Errorf("assistant index out of range: %d", index)
	}
	return m.AppendMessage(sessionID, sess.Messages[index])
}

// RewriteMessages 全量覆写 messages.jsonl。
func (m *Manager) RewriteMessages(sessionID string, msgs []store.Message) error {
	return store.GetSessionStore().RewriteMessages(sessionID, msgs)
}

// --- 内部 ---

func (m *Manager) add(sess *SessionState) {
	m.mu.Lock()
	m.sessions[sess.ID] = sess
	m.mu.Unlock()
}

func (m *Manager) remove(id string) {
	m.mu.Lock()
	delete(m.sessions, id)
	delete(m.cancels, id)
	m.mu.Unlock()
}
