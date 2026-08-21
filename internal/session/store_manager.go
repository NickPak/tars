package session

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"tars/pkg/schema"
	"time"

	"github.com/google/uuid"
)

var (
	instance *StoreManager
)

func InitStoreManager(workdir string) {
	instance = NewStoreManager(workdir)
}

func GetStoreManager() *StoreManager {
	return instance
}

type StoreManager struct {
	workDir string
}

func NewStoreManager(workDir string) *StoreManager {
	return &StoreManager{workDir: workDir}
}

func (s *StoreManager) GetWorkDir() string {
	return s.workDir
}

// ListSession 按创建时间降序列出磁盘上的会话摘要（跳过 meta 损坏/缺失的目录）。
func (s *StoreManager) ListSession() ([]*Metadata, error) {
	baseDir := GetBaseDir(s.workDir)

	entries, err := os.ReadDir(baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []*Metadata{}, nil
		}
		return nil, fmt.Errorf("session store: read sessions dir: %w", err)
	}

	var summaries []*Metadata
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		sessionID := entry.Name()
		meta, err := s.LoadMetadata(sessionID)
		if err != nil || meta == nil {
			continue // skip corrupt / missing meta
		}
		summaries = append(summaries, meta)
	}

	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].CreatedAt > summaries[j].CreatedAt
	})
	return summaries, nil
}

// LoadAllSessionData 遍历磁盘会话存储区，加载每个会话的 meta 与消息，
// 返回恢复好的 Info 列表。单个会话损坏只跳过（Warn 日志），
// 不影响其余会话。
func (s *StoreManager) LoadAllSessionData() ([]*Data, error) {
	summaries, err := s.ListSession()
	if err != nil {
		return nil, err
	}

	infos := make([]*Data, 0, len(summaries))
	for _, sum := range summaries {
		msgs, err := s.LoadMessages(sum.ID)
		if err != nil {
			slog.Warn("Failed to load session messages", "id", sum.ID, "error", err)
			continue
		}
		infos = append(infos, &Data{
			Metadata: sum,
			Messages: msgs,
		})
	}
	slog.Info("Loaded sessions from store", "count", len(infos))
	return infos, nil
}

func (s *StoreManager) LoadMessages(sessionID string) ([]*schema.Message, error) {
	path := filepath.Join(GetDataDir(s.workDir, sessionID), MessageFile)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
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

// CreateSession 创建新会话：生成 ID、磁盘 meta 落盘、返回就绪的 Data。
func (s *StoreManager) CreateSession() (*Data, error) {
	id := uuid.NewString()
	data := NewData(id)

	dir := GetDataDir(s.workDir, id)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("session store: create session %s: %w", id, err)
	}
	if err := s.SaveMetadata(id, data.Metadata); err != nil {
		return nil, err
	}
	return data, nil
}

// DeleteSession 删除会话的磁盘数据。目录不存在视为已删除，直接成功；
// 删除失败重试 5 次（Windows 偶发文件占用）。
func (s *StoreManager) DeleteSession(id string) error {
	dir := GetSessionDir(s.workDir, id)

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

func (s *StoreManager) SaveMetadata(sessionID string, meta *Metadata) error {
	dataDir := GetDataDir(s.workDir, sessionID)
	err := os.MkdirAll(dataDir, 0755)
	if err != nil {
		return fmt.Errorf("session store: create data dir for %s: %w", sessionID, err)
	}

	path := filepath.Join(dataDir, MetaFile)
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

func (s *StoreManager) LoadMetadata(sessionID string) (*Metadata, error) {
	path := filepath.Join(GetDataDir(s.workDir, sessionID), MetaFile)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("session store: read meta.json for %s: %w", sessionID, err)
	}
	defer f.Close()

	meta := NewMetadata(sessionID)
	err = json.NewDecoder(f).Decode(&meta)
	if err != nil {
		return nil, fmt.Errorf("session store: decode meta for %s: %w", sessionID, err)
	}
	return meta, nil
}

// TouchMetadata 刷新元数据的更新时间（无 metadata 时补一条默认的）。
func (s *StoreManager) TouchMetadata(sessionID string) error {
	meta, err := s.LoadMetadata(sessionID)
	if err != nil {
		return err
	}
	if meta == nil {
		meta = NewMetadata(sessionID)
	} else {
		meta.UpdatedAt = time.Now().UnixMilli()
	}
	return s.SaveMetadata(sessionID, meta)
}

// AppendSaveMessage 追加快照：同一消息 ID 可多次出现（流式过程中的
// assistant 快照），读取时按 ID 去重取最后一条。
func (s *StoreManager) AppendSaveMessage(sessionID string, msg ...*schema.Message) error {
	dataDir := GetDataDir(s.workDir, sessionID)
	err := os.MkdirAll(dataDir, 0755)
	if err != nil {
		return fmt.Errorf("session store: create data dir for %s: %w", sessionID, err)
	}

	path := filepath.Join(dataDir, MessageFile)
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

// RewriteMessages 全量覆写消息文件（重试/删除/编辑等截断操作后）。
func (s *StoreManager) RewriteMessages(sessionID string, msgs []*schema.Message) error {
	dataDir := GetDataDir(s.workDir, sessionID)
	err := os.MkdirAll(dataDir, 0755)
	if err != nil {
		return fmt.Errorf("session store: create data dir for %s: %w", sessionID, err)
	}

	path := filepath.Join(dataDir, MessageFile)
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
