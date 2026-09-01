package session

import (
	"os"
	"path/filepath"
	"strings"
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

	m2 := NewManager(&Data{Metadata: meta}, event.Discard, testLLMManager(t, 128000),
		testThreshold, testKeepTurns, testMinBatch, testMaxFailures)
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

// 锁定：有任何对话消息后禁止再改工作目录（后端权威守卫）。
// 原因：历史消息里含相对旧根的路径与内容，改目录后模型照旧路径操作全错。
// 用「有消息」而非「轮运行中」——零消息 ⇒ 从未启动过轮（SubmitMessage 先追加
// 消息再启动），静态无竞态。
func TestSetWorkspaceDirLocksAfterFirstMessage(t *testing.T) {
	m := newTestManager(t)
	dir1, dir2 := t.TempDir(), t.TempDir()

	// 零消息窗口内：可改，且可反复改
	if err := m.SetWorkspaceDir(dir1); err != nil {
		t.Fatalf("set before any message: %v", err)
	}
	if err := m.SetWorkspaceDir(dir2); err != nil {
		t.Fatalf("change within the pre-message window: %v", err)
	}
	if m.GetWorkspaceDir() != dir2 {
		t.Fatalf("WorkspaceDir = %q, want %q", m.GetWorkspaceDir(), dir2)
	}

	// 第一条消息落地 → 锁定
	m.AppendUserMessage("hi")
	if err := m.SetWorkspaceDir(dir1); err == nil {
		t.Fatal("SetWorkspaceDir must be rejected once the session has messages")
	}
	if m.GetWorkspaceDir() != dir2 {
		t.Fatalf("WorkspaceDir changed after lock: %q", m.GetWorkspaceDir())
	}
	// 连"改回默认"也禁止（锁定后任何变更都拒绝）
	if err := m.SetWorkspaceDir(""); err == nil {
		t.Fatal("reset to default must also be locked")
	}
}

// 首条消息自动命名：标题改为输入截断，且发射 session:renamed 事件——
// 前端会话列表靠这个事件即时刷新；漏发时标题要重启重新拉列表才显示
// （回归：事件链路齐全但发射点缺失）。
func TestFirstMessageAutoTitlesAndEmits(t *testing.T) {
	sink := &recordingSink{}
	m, _ := newManagerWithSink(t, sink, nil)

	long := strings.Repeat("帮我把这个模块重构一下，", 10) // 远超 50 字截断
	m.AppendUserMessage(long)

	if m.GetData().Title == DefaultSessionTitle {
		t.Fatal("title should be renamed from the first user message")
	}
	if len(m.GetData().Title) > DefaultSessionTitleLength {
		t.Fatalf("title len = %d, want <= %d", len(m.GetData().Title), DefaultSessionTitleLength)
	}

	var renamed *event.SessionRenamedEvent
	for _, e := range sink.events {
		if e.Kind == event.KindSessionRenamed {
			renamed = e.SessionRenamed
		}
	}
	if renamed == nil || renamed.SessionID != m.GetID() || renamed.Title != m.GetData().Title {
		t.Fatalf("session:renamed event = %+v, title = %q", renamed, m.GetData().Title)
	}

	// 第二条消息不再改名（标题已被占用），也不再发事件
	before := len(sink.events)
	m.AppendUserMessage("第二条")
	for _, e := range sink.events[before:] {
		if e.Kind == event.KindSessionRenamed {
			t.Fatal("second message must not re-emit rename")
		}
	}
	if m.GetData().Title == "第二条" {
		t.Fatal("second message must not overwrite the title")
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

// 归档回读：写入后经 ReadArchive 取回（read_file 的 archive:// 通道消费此方法）。
// 这是"摘要不足时读回原文"的落地，指针必须真能解析。
func TestReadArchiveRoundtrip(t *testing.T) {
	m := newTestManager(t)
	if _, err := m.WriteArchive("turn_3-5", []byte("# Archived turns\n\nbody\n")); err != nil {
		t.Fatalf("write archive: %v", err)
	}

	data, err := m.ReadArchive("turn_3-5.md")
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	if !strings.Contains(string(data), "body") {
		t.Fatalf("archive content = %q", data)
	}

	// 缺失文件明确报错（模型据此知道指针写错了）
	if _, err := m.ReadArchive("turn_9-9.md"); err == nil {
		t.Fatal("missing archive should error")
	}
	// 穿越尝试被 filepath.Base 归一化，不可能读到归档目录之外
	if _, err := m.ReadArchive(filepath.Join("..", MetaFile)); err == nil {
		t.Fatal("traversal must not resolve outside the archive dir")
	}
}
