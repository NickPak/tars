package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tars/pkg/event"
)

// 工作区语义（2026-09 项目化改造后）：
//   - 工作区是项目属性（internal/project）：会话 Manager 经 WorkspaceSource
//     现取，不持有、不持久化、不创建；
//   - 会话存储目录 = projects/<pid>/sessions/<sid>/（CreateSession 的 meta
//     落盘即证据）；
//   - 零消息锁定守卫在 App 层（项目级：同项目全部会话共享同一工作区，
//     单个会话无权单独改）。

// CreateSession 把 meta 落盘到项目嵌套目录（projects/<pid>/sessions/<sid>/
// .data/meta.json），不碰工作区——工作区创建是项目层的职责。
func TestCreateSessionLayout(t *testing.T) {
	InitStoreManager()
	projectDir := t.TempDir()
	data, sessionDir, err := GetStoreManager().CreateSession(projectDir, "proj-1")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if data.ProjectID != "proj-1" {
		t.Fatalf("ProjectID = %q, want proj-1", data.ProjectID)
	}
	wantDir := GetSessionDir(projectDir, data.ID)
	if sessionDir != wantDir {
		t.Fatalf("sessionDir = %q, want %q", sessionDir, wantDir)
	}
	metaPath := filepath.Join(GetDataDirFromSessionDir(sessionDir), MetaFile)
	if _, err := os.Stat(metaPath); err != nil {
		t.Fatalf("meta.json should exist at project-nested path: %v", err)
	}
}

// Manager.GetWorkspaceDir 只是 WorkspaceSource 的直通：项目换绑工作区后
// 同一 Manager 立即看到新值（多会话共享的结构性保障）。
func TestGetWorkspaceDirDelegatesToSource(t *testing.T) {
	m := newTestManager(t)
	ws := t.TempDir()
	m.ws = fakeWorkspace{dir: ws}
	if m.GetWorkspaceDir() != ws {
		t.Fatalf("GetWorkspaceDir = %q, want %q", m.GetWorkspaceDir(), ws)
	}
}

// 首条消息自动命名：标题改为输入截断，且发射 session:renamed 事件——
// 前端会话列表靠这个事件即时刷新；漏发时标题要重启重新拉列表才显示
// （回归：事件链路齐全但发射点缺失）。
func TestFirstMessageAutoTitlesAndEmits(t *testing.T) {
	sink := &recordingSink{}
	m, _ := newManagerWithSink(t, sink, nil)

	long := strings.Repeat("帮我把这个模块重构一下，", 10) // 远超 50 字截断
	m.AppendUserMessage(long, nil)

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
	m.AppendUserMessage("第二条", nil)
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
	id := m.AppendUserMessage("hello world", nil)
	if id == "" {
		t.Fatal("empty message id")
	}
	msgs, err := GetStoreManager().LoadMessages(m.GetSessionDir())
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
