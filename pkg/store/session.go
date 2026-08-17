package store

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type SessionStore struct {
	workDir string // 应用工作目录根（如 ~/tars）
}

// NewSessionStore 创建会话存储实例；workDir 为应用工作目录根。
// 会话存储为普通对象，由装配层（wire）创建并注入，不再使用全局单例。
func NewSessionStore(workDir string) (*SessionStore, error) {
	sessionsDir := filepath.Join(workDir, "sessions")
	if err := os.MkdirAll(sessionsDir, 0755); err != nil {
		return nil, fmt.Errorf("store: create sessions dir: %w", err)
	}
	return &SessionStore{workDir: workDir}, nil
}

func (s *SessionStore) WorkDir() string {
	return s.workDir
}

func (s *SessionStore) BaseDir() string {
	return filepath.Join(s.workDir, "sessions")
}

func (s *SessionStore) SessionDir(id string) string {
	return filepath.Join(s.BaseDir(), id)
}

func (s *SessionStore) DataDir(id string) string {
	return filepath.Join(s.SessionDir(id), ".data")
}

func (s *SessionStore) WorkspaceDir(id string) string {
	return filepath.Join(s.SessionDir(id), "workspace")
}

// ResolveWorkDir 返回会话的工作目录：用户自定义目录（存在才采纳），
// 否则会话的 workspace 目录。默认 workspace 按需创建（懒建，覆盖
// 存量会话）；自定义目录由用户侧保证存在，不代建。
func (s *SessionStore) ResolveWorkDir(id string) string {
	if meta, err := s.LoadMeta(id); err == nil && meta != nil && meta.CustomWorkDir != "" {
		if info, err := os.Stat(meta.CustomWorkDir); err == nil && info.IsDir() {
			return meta.CustomWorkDir
		}
	}
	dir := s.WorkspaceDir(id)
	if err := os.MkdirAll(dir, 0755); err != nil {
		slog.Warn("store: create workspace dir failed", "session", id, "error", err)
	}
	return dir
}

func (s *SessionStore) CreateSession(id, title string, createdAt int64) (*SessionMeta, error) {
	dir := s.DataDir(id)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("store: create session %s: %w", id, err)
	}
	meta := &SessionMeta{
		Title:     title,
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
	}
	if err := s.SaveMeta(id, meta); err != nil {
		return nil, err
	}
	return meta, nil
}

func (s *SessionStore) DeleteSession(id string) error {
	dir := s.SessionDir(id)

	// 目录不存在视为已删除，直接返回成功
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
	return fmt.Errorf("store: delete session %s: %w", id, lastErr)
}

func (s *SessionStore) messagesFile(id string) (string, error) {
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

func (s *SessionStore) AppendMessage(sessionID string, msg ...*Message) error {
	path, err := s.messagesFile(sessionID)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("store: open messages.jsonl for %s: %w", sessionID, err)
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)

	for i := range msg {
		if err := enc.Encode(msg[i]); err != nil {
			return fmt.Errorf("store: encode message for %s: %w", sessionID, err)
		}
	}
	return nil
}

func (s *SessionStore) LoadMessages(sessionID string) ([]*Message, error) {
	path, err := s.messagesFile(sessionID)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []*Message{}, nil
		}
		return nil, fmt.Errorf("store: read messages.jsonl for %s: %w", sessionID, err)
	}
	defer f.Close()

	// messages.jsonl 是快照日志：同一消息 ID 可能出现多条记录
	// （流式过程中的 assistant 快照）。按 ID 去重：保留该 ID 的
	// 最后一条记录（最新快照），位置取其首次出现处。
	seen := make(map[string]int)
	var msgs []*Message
	dec := json.NewDecoder(f)
	for dec.More() {
		m := &Message{}
		if err := dec.Decode(&m); err != nil {
			return msgs, fmt.Errorf("store: decode message in %s: %w", sessionID, err)
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

func (s *SessionStore) RewriteMessages(sessionID string, msgs []*Message) error {
	path, err := s.messagesFile(sessionID)
	if err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("store: rewrite messages.jsonl for %s: %w", sessionID, err)
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	for _, msg := range msgs {
		if err := enc.Encode(msg); err != nil {
			return fmt.Errorf("store: encode message for %s: %w", sessionID, err)
		}
	}
	return nil
}

func (s *SessionStore) SaveMeta(sessionID string, meta *SessionMeta) error {
	path := filepath.Join(s.DataDir(sessionID), "meta.json")
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("store: write meta.json for %s: %w", sessionID, err)
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(meta); err != nil {
		return fmt.Errorf("store: encode meta for %s: %w", sessionID, err)
	}
	return nil
}

func (s *SessionStore) LoadMeta(sessionID string) (*SessionMeta, error) {
	path := filepath.Join(s.DataDir(sessionID), "meta.json")
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("store: read meta.json for %s: %w", sessionID, err)
	}
	defer f.Close()

	var meta SessionMeta
	if err := json.NewDecoder(f).Decode(&meta); err != nil {
		return nil, fmt.Errorf("store: decode meta for %s: %w", sessionID, err)
	}
	return &meta, nil
}

func (s *SessionStore) TouchMeta(sessionID string) error {
	meta, err := s.LoadMeta(sessionID)
	if err != nil {
		return err
	}
	if meta == nil {
		meta = &SessionMeta{Title: "未命名", CreatedAt: time.Now().UnixMilli()}
	}
	meta.UpdatedAt = time.Now().UnixMilli()
	return s.SaveMeta(sessionID, meta)
}

type SessionSummary struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
}

func (s *SessionStore) ListSessions() ([]SessionSummary, error) {
	entries, err := os.ReadDir(s.BaseDir())
	if err != nil {
		if os.IsNotExist(err) {
			return []SessionSummary{}, nil
		}
		return nil, fmt.Errorf("store: read sessions dir: %w", err)
	}

	var summaries []SessionSummary
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		id := entry.Name()
		meta, err := s.LoadMeta(id)
		if err != nil || meta == nil {
			continue // skip corrupt / missing meta
		}
		summaries = append(summaries, SessionSummary{
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
