package session

import (
	"fmt"
	"log/slog"
	"tars/pkg/store"
	"tars/pkg/trace"
	"time"

	"github.com/google/uuid"
)

// Manager 管理全部会话的内存运行态。普通对象，方法均并发安全，
// 由装配层（wire）创建并注入。
type Manager struct {
	store    *store.SessionStore
	sessions map[string]*Info
}

// NewManager 创建会话管理器；store 用于会话的持久化读写。
func NewManager(store *store.SessionStore) *Manager {
	return &Manager{
		store:    store,
		sessions: make(map[string]*Info),
	}
}

// Restore 从磁盘恢复全部会话到内存。
// 应在 NewManager 之后、服务就绪前调用。
func (m *Manager) Restore() error {
	summaries, err := m.store.ListSessions()
	if err != nil {
		return fmt.Errorf("list sessions: %w", err)
	}
	for _, sum := range summaries {
		meta, err := m.store.LoadMeta(sum.ID)
		if err != nil {
			slog.Warn("Failed to load meta", "id", sum.ID, "error", err)
			continue
		}
		msgs, err := m.store.LoadMessages(sum.ID)
		if err != nil {
			slog.Warn("Failed to load messages", "id", sum.ID, "error", err)
			continue
		}
		sess := &Info{
			ID:        sum.ID,
			Title:     meta.Title,
			CreatedAt: meta.CreatedAt,
			UpdatedAt: meta.UpdatedAt,
			Messages:  msgs,
			store:     m.store,
		}
		m.add(sess)
	}
	slog.Info("Loaded sessions from store", "count", len(m.sessions))
	return nil
}

// Create 创建新会话（内存 + 磁盘 meta），返回会话状态。
func (m *Manager) Create() (*Info, error) {
	id := uuid.NewString()
	now := time.Now().UnixMilli()
	if _, err := m.store.CreateSession(id, DefaultSessionTitle, now); err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}
	sess := NewInfo(id, m.store)
	m.add(sess)
	trace.LogSessionCreated(id, DefaultSessionTitle)
	return sess, nil
}

// Delete 删除会话：先取消运行中的会话，再删内存、删磁盘。
func (m *Manager) Delete(id string) error {
	m.Cancel(id) // 先取消运行中的会话，避免写入已删除的目录
	m.remove(id)
	return m.store.DeleteSession(id)
}

// List 返回全部会话的快照切片（浅拷贝，供序列化给前端）。
func (m *Manager) List() []*Info {
	list := make([]*Info, 0, len(m.sessions))
	for _, c := range m.sessions {
		list = append(list, c)
	}
	return list
}

// Has 报告会话是否存在于内存索引中。
func (m *Manager) Has(id string) bool {
	_, ok := m.sessions[id]
	return ok
}

// Find 返回会话指针（调用方自行保证并发读写纪律）。
func (m *Manager) Find(id string) (*Info, bool) {
	sess, ok := m.sessions[id]
	return sess, ok
}

// Cancel 触发会话的取消函数（若正在运行）。
func (m *Manager) Cancel(id string) {
	sess, ok := m.Find(id)
	if ok {
		if sess.Cancel != nil {
			sess.Cancel()
		}
	}
}

// CancelAll 取消全部运行中的会话（进程退出时调用）。
func (m *Manager) CancelAll() {
	for _, sess := range m.sessions {
		if sess.Cancel != nil {
			sess.Cancel()
		}
	}
}

// --- 内部 ---

func (m *Manager) add(sess *Info) {
	m.sessions[sess.ID] = sess
}

func (m *Manager) remove(id string) {
	delete(m.sessions, id)
}
