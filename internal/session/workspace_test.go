package session

import (
	"os"
	"path/filepath"
	"testing"

	"tars/pkg/event"
)

// 策略（目录创建两处各归其位）：
//   - 存储类目录：StoreManager 写路径惰性自闭合（外部无需关心）；
//   - 非存储类目录（工作目录）：session.Manager.Startup 在初始创建与恢复
//     加载两个生命周期点集中检测与创建。

// CreateSession 只写 meta（存储目录随写入惰性创建），不创建工作目录——
// 工作目录是 Manager.Startup 的职责。
func TestCreateSessionDoesNotCreateWorkspaceDir(t *testing.T) {
	InitStoreManager(t.TempDir())
	data, err := GetStoreManager().CreateSession()
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	def := GetWorkspaceDir(GetStoreManager().GetWorkDir(), data.ID)
	if _, err := os.Stat(def); !os.IsNotExist(err) {
		t.Fatal("CreateSession should NOT create workspace dir (Manager.Startup's job)")
	}
	// meta 已落盘（存储目录随首次写入创建）
	if _, err := os.Stat(filepath.Join(GetDataDir(GetStoreManager().GetWorkDir(), data.ID), MetaFile)); err != nil {
		t.Fatalf("meta.json should exist: %v", err)
	}
}

// Manager.Startup 创建工作目录（创建路径：CreateSession → NewManager → Startup）。
func TestStartupCreatesWorkspaceDir(t *testing.T) {
	m := newTestManager(t)
	def := GetWorkspaceDir(GetStoreManager().GetWorkDir(), m.GetID())
	if err := os.RemoveAll(def); err != nil {
		t.Fatal(err)
	}
	if err := m.Startup(); err != nil {
		t.Fatalf("startup: %v", err)
	}
	if info, err := os.Stat(def); err != nil || !info.IsDir() {
		t.Fatalf("workspace dir should exist after Startup: %v", err)
	}
}

// 旧 meta（WorkspaceDir 为空）：Startup 回填默认路径、建目录并持久化。
func TestStartupBackfillsLegacyWorkspaceDir(t *testing.T) {
	m := newTestManager(t)
	sm := GetStoreManager()
	meta, err := sm.LoadMetadata(m.GetID())
	if err != nil || meta == nil {
		t.Fatalf("load meta: %v", err)
	}
	meta.WorkspaceDir = ""
	if err := sm.SaveMetadata(m.GetID(), meta); err != nil {
		t.Fatal(err)
	}
	def := GetWorkspaceDir(sm.GetWorkDir(), m.GetID())
	if err := os.RemoveAll(def); err != nil {
		t.Fatal(err)
	}

	m2 := NewManager(&Data{Metadata: meta}, event.Discard)
	if err := m2.Startup(); err != nil {
		t.Fatalf("startup: %v", err)
	}
	if m2.GetWorkspaceDir() != def {
		t.Fatalf("WorkspaceDir = %q, want default %q", m2.GetWorkspaceDir(), def)
	}
	if _, err := os.Stat(def); err != nil {
		t.Fatalf("workspace dir should be created: %v", err)
	}
	meta2, err := sm.LoadMetadata(m.GetID())
	if err != nil || meta2 == nil {
		t.Fatalf("reload meta: %v", err)
	}
	if meta2.WorkspaceDir != def {
		t.Fatal("backfill not persisted to meta.json")
	}
}

// 自定义工作目录（用户项目）不自动创建——目录已删除时保持原值，
// 避免掩盖"项目目录已被删除"的事实。
func TestStartupDoesNotCreateCustomWorkspaceDir(t *testing.T) {
	m := newTestManager(t)
	custom := filepath.Join(t.TempDir(), "deleted-project")
	m.data.WorkspaceDir = custom

	if err := m.Startup(); err != nil {
		t.Fatalf("startup: %v", err)
	}
	if _, err := os.Stat(custom); !os.IsNotExist(err) {
		t.Fatal("custom workspace dir must not be auto-created")
	}
	if m.GetWorkspaceDir() != custom {
		t.Fatal("custom workspace dir should be kept as-is")
	}
}

// 自定义目录必须已存在才能设置（chdir 失败的提前暴露）。
func TestSetWorkspaceDirRequiresExistingDir(t *testing.T) {
	m := newTestManager(t)
	if err := m.SetWorkspaceDir(filepath.Join(t.TempDir(), "nonexistent")); err == nil {
		t.Fatal("SetWorkspaceDir should reject nonexistent dir")
	}
	existing := t.TempDir()
	if err := m.SetWorkspaceDir(existing); err != nil {
		t.Fatalf("SetWorkspaceDir with existing dir: %v", err)
	}
	if m.GetWorkspaceDir() != existing {
		t.Fatalf("WorkspaceDir = %q, want %q", m.GetWorkspaceDir(), existing)
	}
}

// user 消息必须落盘 messages.jsonl（缺失会导致重启后用户输入丢失）。
func TestAppendUserMessagePersists(t *testing.T) {
	m := newTestManager(t)
	id := m.AppendUserMessage("hello world")
	if id == "" {
		t.Fatal("empty message id")
	}
	msgs, err := GetStoreManager().LoadMessages(m.GetID())
	if err != nil {
		t.Fatalf("load messages: %v", err)
	}
	found := false
	for _, msg := range msgs {
		if msg.ID == id && msg.Role == "user" && msg.Content == "hello world" {
			found = true
		}
	}
	if !found {
		t.Fatal("user message not persisted to messages.jsonl")
	}
}

// 归档写入：目录由 StoreManager 惰性自闭合。
func TestWriteArchiveSelfEnsuresDir(t *testing.T) {
	m := newTestManager(t)
	path, err := m.WriteArchive("turn_1-2", []byte("# test"))
	if err != nil {
		t.Fatalf("write archive: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("archive should exist: %v", err)
	}
	if string(data) != "# test" {
		t.Fatalf("archive content = %q", data)
	}
}
