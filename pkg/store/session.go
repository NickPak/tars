// Package store provides session persistence using JSONL files.
// Each session lives in its own directory named by UUID under a shared
// sessions root. Messages are appended line-by-line to messages.jsonl
// (same approach as Codex, Claude Code, Pi).
package store

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/cloudwego/eino/schema"
)

var (
	instance *SessionStore
)

// SessionMeta holds session-level metadata stored alongside messages.
type SessionMeta struct {
	Title     string `json:"title"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
	// CustomWorkDir is an external directory the user chose as the workspace.
	// When empty, the session uses the default isolated workspace
	// (sessions/{id}/workspace/).
	CustomWorkDir string `json:"customWorkDir,omitempty"`
}

// Message is one line in messages.jsonl. 本类型是会话消息的唯一权威定义：
// 磁盘格式、内存形态、前端 API 共用，internal/session 以类型别名直接复用
// （session.Message = store.Message）。
type Message struct {
	ID         string          `json:"id"`
	Role       schema.RoleType `json:"role"`
	Content    string          `json:"content"`
	ToolCalls  []ToolCall      `json:"toolCalls,omitempty"`
	ToolCallID string          `json:"toolCallId,omitempty"`
	CreatedAt  int64           `json:"createdAt"`
	// Reasoning 仅 assistant 消息有值：模型思考过程（thinking/reasoning
	// content），多轮迭代时逐轮累加。
	Reasoning string `json:"reasoning,omitempty"`
	// Usage/ElapsedMs 仅 assistant 消息有值：本轮 token 消耗与总耗时。
	Usage     *UsageInfo `json:"usage,omitempty"`
	ElapsedMs int64      `json:"elapsedMs,omitempty"`
}

// ToSchemaMessage 把 Message 转成 Eino schema.Message（发送给模型的协议形态）。
// role 映射：user→User、assistant→Assistant、system→System、tool→Tool，
// 未知角色回退为 User。Reasoning 仅在 assistant 角色上有意义。
func (m Message) ToSchemaMessage() *schema.Message {
	msg := &schema.Message{
		Content:          m.Content,
		ReasoningContent: m.Reasoning,
		ToolCallID:       m.ToolCallID,
	}
	switch m.Role {
	case schema.Assistant:
		msg.Role = schema.Assistant
		if len(m.ToolCalls) > 0 {
			msg.ToolCalls = make([]schema.ToolCall, len(m.ToolCalls))
			for i, tc := range m.ToolCalls {
				msg.ToolCalls[i] = schema.ToolCall{
					ID:   tc.ID,
					Type: "function",
					Function: schema.FunctionCall{
						Name:      tc.Name,
						Arguments: tc.Args,
					},
				}
			}
		}
	case schema.System:
		msg.Role = schema.System
	case schema.Tool:
		msg.Role = schema.Tool
	default:
		msg.Role = schema.User
	}
	return msg
}

// UsageInfo 一次 assistant 回复的 token 统计。
type UsageInfo struct {
	PromptTokens     int `json:"promptTokens"`
	CompletionTokens int `json:"completionTokens"`
	TotalTokens      int `json:"totalTokens"`
	CachedTokens     int `json:"cachedTokens,omitempty"`
	// ModelEntry 产生该用量的模型条目 ID，多模型下按条目价格表核算费用
	ModelEntry string `json:"modelEntry,omitempty"`
}

// ToolCall is a single tool invocation within an assistant message.
type ToolCall struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Args string `json:"args"`
}

// SessionStore is a file-based session store backed by the work directory
// on disk. Every method is safe for concurrent use. SessionStore is stateless
// (holds only the workDir path) and is used as a global singleton via
// InitSessionStore/GetSessionStore.
//
// 目录布局由 store 内部决定：{workDir}/sessions/{id}/...
type SessionStore struct {
	workDir string // 应用工作目录根（如 ~/tars）
}

// InitSessionStore initializes the global SessionStore singleton with the
// app work directory. Call once at process startup.
// 会话目录等子路径由 store 内部拼接，调用方只需传工作目录根。
func InitSessionStore(workDir string) error {
	sessionsDir := filepath.Join(workDir, "sessions")
	if err := os.MkdirAll(sessionsDir, 0755); err != nil {
		return fmt.Errorf("store: create sessions dir: %w", err)
	}
	instance = &SessionStore{workDir: workDir}
	return nil
}

// GetSessionStore returns the global SessionStore instance. Returns nil
// if InitSessionStore has not been called yet.
func GetSessionStore() *SessionStore { return instance }

// --- 目录路径 ---

// WorkDir returns the app work directory root passed to InitSessionStore.
func (s *SessionStore) WorkDir() string {
	return s.workDir
}

// BaseDir returns the root directory containing all sessions.
func (s *SessionStore) BaseDir() string {
	return filepath.Join(s.workDir, "sessions")
}

// SessionDir returns the directory for a single session (BaseDir + id).
func (s *SessionStore) SessionDir(id string) string {
	return filepath.Join(s.BaseDir(), id)
}

// DataDir returns the internal data directory for a session (.data/).
func (s *SessionStore) DataDir(id string) string {
	return filepath.Join(s.SessionDir(id), ".data")
}

// WorkspaceDir returns the workspace directory for a session (workspace/).
// This is the root for all tool file operations (read/edit/list/search/run_command).
func (s *SessionStore) WorkspaceDir(id string) string {
	return filepath.Join(s.SessionDir(id), "workspace")
}

// ResolveWorkDir returns the effective workspace directory for a session.
// If the session has a CustomWorkDir set in meta.json, that path is used;
// otherwise the default isolated workspace is returned.
func (s *SessionStore) ResolveWorkDir(id string) string {
	if meta, err := s.LoadMeta(id); err == nil && meta != nil && meta.CustomWorkDir != "" {
		if info, err := os.Stat(meta.CustomWorkDir); err == nil && info.IsDir() {
			return meta.CustomWorkDir
		}
	}
	return s.WorkspaceDir(id)
}

// --- 会话生命周期 ---

// CreateSession initialises a session on disk: creates the directory
// tree and writes meta.json. Returns the created SessionMeta.
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

// DeleteSession removes the entire session directory from disk.
// On Windows, a just-closed file handle may keep the file locked for a brief
// moment, so RemoveAll is retried a few times before giving up.
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

// --- 消息 I/O ---

// messagesFile returns the path to messages.jsonl for a session.
// If a legacy "session.jsonl" exists, it is auto-migrated to "messages.jsonl".
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

// AppendMessage serialises msg to JSON and appends it as one line to
// messages.jsonl. The file is created if it doesn't exist.
func (s *SessionStore) AppendMessage(sessionID string, msg Message) error {
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
	if err := enc.Encode(msg); err != nil {
		return fmt.Errorf("store: encode message for %s: %w", sessionID, err)
	}
	return nil
}

// LoadMessages reads messages.jsonl for a session and returns all messages
// in order. Returns an empty slice (not nil) if the file doesn't exist yet.
func (s *SessionStore) LoadMessages(sessionID string) ([]Message, error) {
	path, err := s.messagesFile(sessionID)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []Message{}, nil
		}
		return nil, fmt.Errorf("store: read messages.jsonl for %s: %w", sessionID, err)
	}
	defer f.Close()

	var msgs []Message
	dec := json.NewDecoder(f)
	for dec.More() {
		var m Message
		if err := dec.Decode(&m); err != nil {
			return msgs, fmt.Errorf("store: decode message in %s: %w", sessionID, err)
		}
		msgs = append(msgs, m)
	}
	return msgs, nil
}

// RewriteMessages overwrites messages.jsonl with the given message list.
// Used after message deletion to keep the file in sync with memory.
func (s *SessionStore) RewriteMessages(sessionID string, msgs []Message) error {
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

// --- 元数据 I/O ---

// SaveMeta writes SessionMeta to meta.json inside the session's .data/ directory.
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

// LoadMeta reads meta.json for a session. Returns nil if the file
// doesn't exist (the caller should treat this as a new session).
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

// TouchMeta updates UpdatedAt to now and writes meta.json. Convenience for
// callers that only want to bump the timestamp.
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

// --- 列表 ---

// SessionSummary is a lightweight representation returned by ListSessions.
type SessionSummary struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
}

// ListSessions scans the sessions root directory, reads meta.json for
// each subdirectory, and returns summaries sorted by UpdatedAt descending.
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
