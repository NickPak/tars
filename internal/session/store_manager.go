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
		comp, err := s.LoadCompaction(sum.ID)
		if err != nil {
			// 压缩态损坏回退恒等投影（03 篇安全降级），不影响会话恢复。
			slog.Warn("Failed to load compaction, fallback to identity projection", "id", sum.ID, "error", err)
		}
		infos = append(infos, &Data{
			Metadata:   sum,
			Messages:   msgs,
			Compaction: comp,
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

// CreateSession 创建新会话：生成 ID、meta 落盘（存储目录随首次写入惰性
// 创建），返回就绪的 Data。工作目录的创建在 session.Manager.Startup。
func (s *StoreManager) CreateSession() (*Data, error) {
	id := uuid.NewString()
	data := NewData(id)

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

// --- 压缩态持久化（plan/context 03 篇：compaction.json + archive/） ---

// SaveCompaction 原子写压缩态（tmp+rename，03 篇 §3：崩溃无中间态）。
func (s *StoreManager) SaveCompaction(sessionID string, c *CompactionData) error {
	dataDir := GetDataDir(s.workDir, sessionID)
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return fmt.Errorf("session store: create data dir for %s: %w", sessionID, err)
	}
	path := filepath.Join(dataDir, CompactionFile)
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("session store: write compaction for %s: %w", sessionID, err)
	}
	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(c); err != nil {
		f.Close()
		return fmt.Errorf("session store: encode compaction for %s: %w", sessionID, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("session store: close compaction for %s: %w", sessionID, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("session store: rename compaction for %s: %w", sessionID, err)
	}
	return nil
}

// LoadCompaction 读取压缩态：文件不存在返回 (nil, nil)；
// 损坏返回 (nil, error)——调用方回退恒等投影（03 篇安全降级）。
func (s *StoreManager) LoadCompaction(sessionID string) (*CompactionData, error) {
	path := filepath.Join(GetDataDir(s.workDir, sessionID), CompactionFile)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("session store: read compaction for %s: %w", sessionID, err)
	}
	defer f.Close()
	c := &CompactionData{}
	if err := json.NewDecoder(f).Decode(c); err != nil {
		return nil, fmt.Errorf("session store: decode compaction for %s: %w", sessionID, err)
	}
	return c, nil
}

// DeleteCompaction 删除压缩态（作废）；不存在视为已删除。
func (s *StoreManager) DeleteCompaction(sessionID string) error {
	err := os.Remove(filepath.Join(GetDataDir(s.workDir, sessionID), CompactionFile))
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("session store: delete compaction for %s: %w", sessionID, err)
	}
	return nil
}

// WriteArchive 惰性创建归档目录并写入归档文件（data/archive/<rangeLabel>.md，
// 03 篇 §2），返回文件路径。存储类目录均由 StoreManager 写路径自闭合，
// 外部调用者无需关心创建。
func (s *StoreManager) WriteArchive(sessionID, rangeLabel string, content []byte) (string, error) {
	dir := filepath.Join(GetDataDir(s.workDir, sessionID), ArchiveDir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("session store: create archive dir for %s: %w", sessionID, err)
	}
	path := filepath.Join(dir, rangeLabel+".md")
	if err := os.WriteFile(path, content, 0644); err != nil {
		return "", fmt.Errorf("session store: write archive for %s: %w", sessionID, err)
	}
	return path, nil
}

// ReadArchive 读取归档原文（data/archive/<name>）。
// name 经 filepath.Base 归一，无论调用方传什么都不可能穿越出归档目录
// （纵深防御：调用侧 toolkit 已做白名单校验，此处再兜一层结构性保证）。
func (s *StoreManager) ReadArchive(sessionID, name string) ([]byte, error) {
	path := filepath.Join(GetDataDir(s.workDir, sessionID), ArchiveDir, filepath.Base(name))
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("session store: read archive for %s: %w", sessionID, err)
	}
	return data, nil
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
