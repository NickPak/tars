package session

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"time"

	"tars/internal/event"
	"tars/pkg/schema"

	"github.com/google/uuid"
)

// Meta 是会话的元数据（持久化在 .data/meta.json）。
type Meta struct {
	Title         string `json:"title"`
	CreatedAt     int64  `json:"createdAt"`
	UpdatedAt     int64  `json:"updatedAt"`
	CustomWorkDir string `json:"customWorkDir,omitempty"`
}

// Summary 是磁盘上的会话摘要（RestoreAll 遍历时使用）。
type Summary struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
}

// Store 是会话的存储与工厂：目录布局、jsonl 快照、meta.json，
// 以及 Info 的创建与恢复。会话的持久化细节全部封装在本包，
// 外部（boot）只面对 Info 与本类型的少量方法。
// Store 为普通对象，由装配层（boot）创建并注入。
type Store struct {
	workDir string // 应用工作目录根（如 ~/tars）
	sink    event.Sink
}

// NewStore 创建会话存储；workDir 为应用工作目录根，sink 注入每个
// 创建/恢复出的会话（消息追加时发射事件），nil 时静默。
func NewStore(workDir string, sink event.Sink) (*Store, error) {
	sessionsDir := filepath.Join(workDir, "sessions")
	if err := os.MkdirAll(sessionsDir, 0755); err != nil {
		return nil, fmt.Errorf("session store: create sessions dir: %w", err)
	}
	if sink == nil {
		sink = event.Discard
	}
	return &Store{workDir: workDir, sink: sink}, nil
}

func (s *Store) WorkDir() string {
	return s.workDir
}

func (s *Store) BaseDir() string {
	return filepath.Join(s.workDir, "sessions")
}

func (s *Store) SessionDir(id string) string {
	return filepath.Join(s.BaseDir(), id)
}

func (s *Store) DataDir(id string) string {
	return filepath.Join(s.SessionDir(id), ".data")
}

func (s *Store) WorkspaceDir(id string) string {
	return filepath.Join(s.SessionDir(id), "workspace")
}

// ResolveWorkDir 返回会话的工作目录：用户自定义目录（存在才采纳），
// 否则会话的 workspace 目录。默认 workspace 按需创建（懒建，覆盖
// 存量会话）；自定义目录由用户侧保证存在，不代建。
func (s *Store) ResolveWorkDir(id string) string {
	if meta, err := s.LoadMeta(id); err == nil && meta != nil && meta.CustomWorkDir != "" {
		if info, err := os.Stat(meta.CustomWorkDir); err == nil && info.IsDir() {
			return meta.CustomWorkDir
		}
	}
	dir := s.WorkspaceDir(id)
	if err := os.MkdirAll(dir, 0755); err != nil {
		slog.Warn("session store: create workspace dir failed", "session", id, "error", err)
	}
	return dir
}

// --- 会话生命周期（Info 的创建/恢复/删除） ---

// Create 创建新会话：生成 ID、磁盘 meta 落盘、返回就绪的 Info。
func (s *Store) Create() (*Info, error) {
	id := uuid.NewString()
	sess := NewInfo(id, s, s.sink)

	dir := s.DataDir(id)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("session store: create session %s: %w", id, err)
	}
	meta := &Meta{
		Title:     sess.Title,
		CreatedAt: sess.CreatedAt,
		UpdatedAt: sess.CreatedAt,
	}
	if err := s.SaveMeta(id, meta); err != nil {
		return nil, err
	}
	return sess, nil
}

// RestoreAll 遍历磁盘会话存储区，加载每个会话的 meta 与消息，
// 返回恢复好的 Info 列表。单个会话损坏只跳过（Warn 日志），
// 不影响其余会话。
func (s *Store) RestoreAll() ([]*Info, error) {
	summaries, err := s.List()
	if err != nil {
		return nil, err
	}

	infos := make([]*Info, 0, len(summaries))
	for _, sum := range summaries {
		meta, err := s.LoadMeta(sum.ID)
		if err != nil {
			slog.Warn("Failed to load session meta", "id", sum.ID, "error", err)
			continue
		}
		msgs, err := s.LoadMessages(sum.ID)
		if err != nil {
			slog.Warn("Failed to load session messages", "id", sum.ID, "error", err)
			continue
		}
		infos = append(infos, &Info{
			ID:        sum.ID,
			Title:     meta.Title,
			CreatedAt: meta.CreatedAt,
			UpdatedAt: meta.UpdatedAt,
			Messages:  msgs,
			store:     s,
			sink:      s.sink,
		})
	}
	slog.Info("Loaded sessions from store", "count", len(infos))
	return infos, nil
}

// Delete 删除会话的磁盘数据。目录不存在视为已删除，直接成功；
// 删除失败重试 5 次（Windows 偶发文件占用）。
func (s *Store) Delete(id string) error {
	dir := s.SessionDir(id)

	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil
	}

	var lastErr error
	for range 5 {
		if err := os.RemoveAll(dir); err == nil {
			return nil
		} else {
			lastErr = err
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("session store: delete session %s: %w", id, lastErr)
}

// --- 消息持久化（jsonl 快照日志；Info 内部使用） ---

func (s *Store) messagesFile(id string) (string, error) {
	newPath := filepath.Join(s.DataDir(id), "messages.jsonl")
	if _, err := os.Stat(newPath); err == nil {
		return newPath, nil // 新文件已存在
	}

	// 兼容：旧文件 session.jsonl → messages.jsonl
	oldPath := filepath.Join(s.DataDir(id), "session.jsonl")
	if _, err := os.Stat(oldPath); err == nil {
		if err := os.Rename(oldPath, newPath); err == nil {
			slog.Info("Migrated legacy session.jsonl to messages.jsonl", "session", id)
		} else {
			// 重命名失败，继续用旧文件
			return oldPath, nil
		}
	}
	return newPath, nil
}

// AppendMessage 追加快照：同一消息 ID 可多次出现（流式过程中的
// assistant 快照），读取时按 ID 去重取最后一条。
func (s *Store) AppendMessage(sessionID string, msg ...*schema.Message) error {
	path, err := s.messagesFile(sessionID)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("session store: open messages.jsonl for %s: %w", sessionID, err)
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)

	for i := range msg {
		if err := enc.Encode(msg[i]); err != nil {
			return fmt.Errorf("session store: encode message for %s: %w", sessionID, err)
		}
	}
	return nil
}

func (s *Store) LoadMessages(sessionID string) ([]*schema.Message, error) {
	path, err := s.messagesFile(sessionID)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []*schema.Message{}, nil
		}
		return nil, fmt.Errorf("session store: read messages.jsonl for %s: %w", sessionID, err)
	}
	defer f.Close()

	// messages.jsonl 是快照日志：同一消息 ID 可能出现多条记录
	// （流式过程中的 assistant 快照）。按 ID 去重：保留该 ID 的
	// 最后一条记录（最新快照），位置取其首次出现处。
	seen := make(map[string]int)
	var msgs []*schema.Message
	dec := json.NewDecoder(f)
	for dec.More() {
		m := &schema.Message{}
		if err := dec.Decode(&m); err != nil {
			return msgs, fmt.Errorf("session store: decode message in %s: %w", sessionID, err)
		}
		if idx, ok := seen[m.ID]; ok {
			msgs[idx] = m
			continue
		}
		seen[m.ID] = len(msgs)
		msgs = append(msgs, m)
	}
	return msgs, nil
}

// RewriteMessages 全量覆写消息文件（重试/删除/编辑等截断操作后）。
func (s *Store) RewriteMessages(sessionID string, msgs []*schema.Message) error {
	path, err := s.messagesFile(sessionID)
	if err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("session store: rewrite messages.jsonl for %s: %w", sessionID, err)
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	for _, msg := range msgs {
		if err := enc.Encode(msg); err != nil {
			return fmt.Errorf("session store: encode message for %s: %w", sessionID, err)
		}
	}
	return nil
}

// --- 元数据 ---

func (s *Store) SaveMeta(sessionID string, meta *Meta) error {
	path := filepath.Join(s.DataDir(sessionID), "meta.json")
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("session store: write meta.json for %s: %w", sessionID, err)
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(meta); err != nil {
		return fmt.Errorf("session store: encode meta for %s: %w", sessionID, err)
	}
	return nil
}

func (s *Store) LoadMeta(sessionID string) (*Meta, error) {
	path := filepath.Join(s.DataDir(sessionID), "meta.json")
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("session store: read meta.json for %s: %w", sessionID, err)
	}
	defer f.Close()

	var meta Meta
	if err := json.NewDecoder(f).Decode(&meta); err != nil {
		return nil, fmt.Errorf("session store: decode meta for %s: %w", sessionID, err)
	}
	return &meta, nil
}

// TouchMeta 刷新元数据的更新时间（无 meta 时补一条默认的）。
func (s *Store) TouchMeta(sessionID string) error {
	meta, err := s.LoadMeta(sessionID)
	if err != nil {
		return err
	}
	if meta == nil {
		meta = &Meta{Title: "未命名", CreatedAt: time.Now().UnixMilli()}
	}
	meta.UpdatedAt = time.Now().UnixMilli()
	return s.SaveMeta(sessionID, meta)
}

// List 按更新时间降序列出磁盘上的会话摘要（跳过 meta 损坏/缺失的目录）。
func (s *Store) List() ([]Summary, error) {
	entries, err := os.ReadDir(s.BaseDir())
	if err != nil {
		if os.IsNotExist(err) {
			return []Summary{}, nil
		}
		return nil, fmt.Errorf("session store: read sessions dir: %w", err)
	}

	var summaries []Summary
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		id := entry.Name()
		meta, err := s.LoadMeta(id)
		if err != nil || meta == nil {
			continue // skip corrupt / missing meta
		}
		summaries = append(summaries, Summary{
			ID:        id,
			Title:     meta.Title,
			CreatedAt: meta.CreatedAt,
			UpdatedAt: meta.UpdatedAt,
		})
	}

	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].UpdatedAt > summaries[j].UpdatedAt
	})
	return summaries, nil
}
