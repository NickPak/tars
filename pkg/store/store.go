// Package store provides conversation persistence using JSONL files.
// Each conversation lives in its own directory named by UUID under a
// shared conversations root. Messages are appended line-by-line to
// session.jsonl (same approach as Codex, Claude Code, Pi).
package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// ConvMeta holds conversation-level metadata stored alongside messages.
type ConvMeta struct {
	Title     string `json:"title"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
	// CustomWorkDir is an external directory the user chose as the workspace.
	// When empty, the conversation uses the default isolated workspace
	// (conversations/{id}/workspace/).
	CustomWorkDir string `json:"customWorkDir,omitempty"`
}

// Message is one line in session.jsonl. Fields match agentservice.Message.
type Message struct {
	ID         string     `json:"id"`
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"toolCalls,omitempty"`
	ToolCallID string     `json:"toolCallId,omitempty"`
	CreatedAt  int64      `json:"createdAt"`
}

// ToolCall is a single tool invocation within an assistant message.
type ToolCall struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Args string `json:"args"`
}

// Store is a file-based conversation store backed by a conversations root
// directory on disk. Every method is safe for concurrent use.
type Store struct {
	convsDir string // e.g. {workDir}/.agent/conversations
}

// NewStore creates (if needed) the conversations root directory and returns a Store.
func NewStore(convsDir string) (*Store, error) {
	if err := os.MkdirAll(convsDir, 0755); err != nil {
		return nil, fmt.Errorf("store: create conversations dir: %w", err)
	}
	return &Store{convsDir: convsDir}, nil
}

// ConversationsDir returns the root directory containing all conversations.
func (s *Store) ConversationsDir() string {
	return s.convsDir
}

// ConvDir returns the root directory for a single conversation.
func (s *Store) ConvDir(id string) string {
	return filepath.Join(s.convsDir, id)
}

// DataDir returns the internal data directory for a conversation (.data/).
func (s *Store) DataDir(id string) string {
	return filepath.Join(s.ConvDir(id), ".data")
}

// LogsDir returns the logs directory for a conversation (.logs/).
func (s *Store) LogsDir(id string) string {
	return filepath.Join(s.ConvDir(id), ".logs")
}

// WorkspaceDir returns the workspace directory for a conversation (workspace/).
// This is the root for all tool file operations (read/edit/list/search/run_command).
func (s *Store) WorkspaceDir(id string) string {
	return filepath.Join(s.ConvDir(id), "workspace")
}

// ResolveWorkDir returns the effective workspace directory for a conversation.
// If the conversation has a CustomWorkDir set in meta.json, that path is used;
// otherwise the default isolated workspace is returned.
func (s *Store) ResolveWorkDir(id string) string {
	if meta, err := s.LoadMeta(id); err == nil && meta != nil && meta.CustomWorkDir != "" {
		if info, err := os.Stat(meta.CustomWorkDir); err == nil && info.IsDir() {
			return meta.CustomWorkDir
		}
	}
	return s.WorkspaceDir(id)
}

// DefaultDataDir returns the default data directory under the user's
// home directory: ~/tars on all platforms.
func DefaultDataDir() string {
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, "tars")
	}
	return "."
}

// CreateConversation initialises a conversation on disk: creates the directory
// tree and writes meta.json. Returns the created ConvMeta.
func (s *Store) CreateConversation(id, title string, createdAt int64) (*ConvMeta, error) {
	dir := s.DataDir(id)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("store: create conversation %s: %w", id, err)
	}
	meta := &ConvMeta{
		Title:     title,
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
	}
	if err := s.SaveMeta(id, meta); err != nil {
		return nil, err
	}
	return meta, nil
}

// AppendMessage serialises msg to JSON and appends it as one line to
// session.jsonl. The file is created if it doesn't exist.
func (s *Store) AppendMessage(convID string, msg Message) error {
	path := filepath.Join(s.DataDir(convID), "session.jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("store: open session.jsonl for %s: %w", convID, err)
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(msg); err != nil {
		return fmt.Errorf("store: encode message for %s: %w", convID, err)
	}
	return nil
}

// LoadMessages reads session.jsonl for a conversation and returns all messages
// in order. Returns an empty slice (not nil) if the file doesn't exist yet.
func (s *Store) LoadMessages(convID string) ([]Message, error) {
	path := filepath.Join(s.DataDir(convID), "session.jsonl")
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []Message{}, nil
		}
		return nil, fmt.Errorf("store: read session.jsonl for %s: %w", convID, err)
	}
	defer f.Close()

	var msgs []Message
	dec := json.NewDecoder(f)
	for dec.More() {
		var m Message
		if err := dec.Decode(&m); err != nil {
			return msgs, fmt.Errorf("store: decode message in %s: %w", convID, err)
		}
		msgs = append(msgs, m)
	}
	return msgs, nil
}

// SaveMeta writes ConvMeta to meta.json inside the conversation's .data/ directory.
func (s *Store) SaveMeta(convID string, meta *ConvMeta) error {
	path := filepath.Join(s.DataDir(convID), "meta.json")
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("store: write meta.json for %s: %w", convID, err)
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(meta); err != nil {
		return fmt.Errorf("store: encode meta for %s: %w", convID, err)
	}
	return nil
}

// LoadMeta reads meta.json for a conversation. Returns nil if the file
// doesn't exist (the caller should treat this as a new conversation).
func (s *Store) LoadMeta(convID string) (*ConvMeta, error) {
	path := filepath.Join(s.DataDir(convID), "meta.json")
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("store: read meta.json for %s: %w", convID, err)
	}
	defer f.Close()

	var meta ConvMeta
	if err := json.NewDecoder(f).Decode(&meta); err != nil {
		return nil, fmt.Errorf("store: decode meta for %s: %w", convID, err)
	}
	return &meta, nil
}

// ConvSummary is a lightweight representation returned by ListConversations.
type ConvSummary struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
}

// ListConversations scans the conversations root directory, reads meta.json for
// each subdirectory, and returns summaries sorted by UpdatedAt descending.
func (s *Store) ListConversations() ([]ConvSummary, error) {
	entries, err := os.ReadDir(s.convsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []ConvSummary{}, nil
		}
		return nil, fmt.Errorf("store: read conversations dir: %w", err)
	}

	var summaries []ConvSummary
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		id := entry.Name()
		meta, err := s.LoadMeta(id)
		if err != nil || meta == nil {
			continue // skip corrupt / missing meta
		}
		summaries = append(summaries, ConvSummary{
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

// RewriteMessages overwrites session.jsonl with the given message list.
// Used after message deletion to keep the file in sync with memory.
func (s *Store) RewriteMessages(convID string, msgs []Message) error {
	path := filepath.Join(s.DataDir(convID), "session.jsonl")
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("store: rewrite session.jsonl for %s: %w", convID, err)
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	for _, msg := range msgs {
		if err := enc.Encode(msg); err != nil {
			return fmt.Errorf("store: encode message for %s: %w", convID, err)
		}
	}
	return nil
}

// DeleteConversation removes the entire conversation directory from disk.
// On Windows, a just-closed file handle may keep the file locked for a brief
// moment, so RemoveAll is retried a few times before giving up.
func (s *Store) DeleteConversation(convID string) error {
	dir := s.ConvDir(convID)

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
	return fmt.Errorf("store: delete conversation %s: %w", convID, lastErr)
}

// TouchMeta updates UpdatedAt to now and writes meta.json. Convenience for
// callers that only want to bump the timestamp.
func (s *Store) TouchMeta(convID string) error {
	meta, err := s.LoadMeta(convID)
	if err != nil {
		return err
	}
	if meta == nil {
		meta = &ConvMeta{Title: "未命名", CreatedAt: time.Now().UnixMilli()}
	}
	meta.UpdatedAt = time.Now().UnixMilli()
	return s.SaveMeta(convID, meta)
}
