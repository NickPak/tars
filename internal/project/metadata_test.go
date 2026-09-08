package project

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// Startup：创建 projects/ 顶层目录。
func TestStartup(t *testing.T) {
	workDir := t.TempDir()
	m := NewStoreManager(workDir)
	if err := m.Startup(); err != nil {
		t.Fatalf("startup: %v", err)
	}
	if info, err := os.Stat(GetBaseDir(workDir)); err != nil || !info.IsDir() {
		t.Fatalf("base dir should exist: %v", err)
	}
}

// Save：project.json 落盘 + 默认工作区目录急式创建。
func TestSave(t *testing.T) {
	m := NewStoreManager(t.TempDir())
	p := NewMetaData(m.workDir, "p-1")
	if err := m.Save(p); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := os.Stat(filepath.Join(p.GetProjectDir(), MetaFile)); err != nil {
		t.Fatalf("project.json should exist: %v", err)
	}
	ws := p.GetWorkspaceDir()
	if ws != GetWorkspaceDir(m.workDir, p.ID) {
		t.Fatalf("default workspace = %q", ws)
	}
	if info, err := os.Stat(ws); err != nil || !info.IsDir() {
		t.Fatalf("default workspace should be created eagerly: %v", err)
	}
}

// List 扫描 projects/（目录即真相）：按创建时间升序；无 meta 的目录跳过。
func TestList(t *testing.T) {
	m := NewStoreManager(t.TempDir())
	p1 := NewMetaData(m.workDir, "p-1")
	if err := m.Save(p1); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	p2 := NewMetaData(m.workDir, "p-2")
	if err := m.Save(p2); err != nil {
		t.Fatal(err)
	}
	// 噪声：无 meta 的空目录与非目录文件
	_ = os.MkdirAll(filepath.Join(GetBaseDir(m.workDir), "corrupt"), 0755)
	_ = os.WriteFile(filepath.Join(GetBaseDir(m.workDir), "stray.txt"), nil, 0644)

	list, err := m.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 || list[0].ID != p1.ID || list[1].ID != p2.ID {
		t.Fatalf("list = %v projects, want [p1 p2] in creation order", len(list))
	}
}

// Load：不存在返回 (nil, nil)。
func TestLoadMissing(t *testing.T) {
	m := NewStoreManager(t.TempDir())
	p, err := m.Load("nope")
	if err != nil || p != nil {
		t.Fatalf("missing project should be (nil, nil), got %v, %v", p, err)
	}
}

// Save → Load 回环：标题与自定义工作区持久化。
func TestSaveLoadRoundtrip(t *testing.T) {
	m := NewStoreManager(t.TempDir())
	p := NewMetaData(m.workDir, "p-1")
	if p.Title != DefaultProjectTitle {
		t.Fatalf("new project title = %q, want default %q", p.Title, DefaultProjectTitle)
	}
	p.SetTitle("我的项目")
	custom := t.TempDir()
	p.SetWorkspaceDir(custom)
	if err := m.Save(p); err != nil {
		t.Fatalf("save: %v", err)
	}

	p2, err := m.Load(p.ID)
	if err != nil || p2 == nil {
		t.Fatalf("load: %v, %v", p2, err)
	}
	if p2.Title != "我的项目" {
		t.Fatalf("title not persisted: %q", p2.Title)
	}
	if p2.GetWorkspaceDir() != custom {
		t.Fatalf("custom workspace not persisted: %q", p2.GetWorkspaceDir())
	}
}

// Load 旧数据兼容：project.json 缺 title 字段时保持为空（展示层走会话
// 标题推导）——默认值只属于创建点（NewMetaData），加载不得预填。
func TestLoadLegacyUntitled(t *testing.T) {
	m := NewStoreManager(t.TempDir())
	p := NewMetaData(m.workDir, "p-1")
	if err := m.Save(p); err != nil {
		t.Fatal(err)
	}
	// 模拟旧格式：写入无 title 字段的 project.json
	legacy := []byte(`{"id":"p-1","workspaceDir":` + strconv.Quote(p.WorkspaceDir) + `,"createdAt":1,"updatedAt":1}`)
	if err := os.WriteFile(p.GetMetaFile(), legacy, 0644); err != nil {
		t.Fatal(err)
	}

	loaded, err := m.Load("p-1")
	if err != nil || loaded == nil {
		t.Fatalf("load: %v, %v", loaded, err)
	}
	if loaded.Title != "" {
		t.Fatalf("legacy title should stay empty, got %q", loaded.Title)
	}
	if loaded.GetWorkspaceDir() != p.WorkspaceDir {
		t.Fatalf("workspace = %q", loaded.GetWorkspaceDir())
	}
}

// Delete：级联删除项目目录（含其下会话数据与默认工作区）。
func TestDelete(t *testing.T) {
	m := NewStoreManager(t.TempDir())
	p := NewMetaData(m.workDir, "p-1")
	if err := m.Save(p); err != nil {
		t.Fatal(err)
	}
	sessDir := filepath.Join(p.GetProjectDir(), SessionsDirName, "s-1", ".data")
	if err := os.MkdirAll(sessDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := m.Delete(p.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := os.Stat(p.GetProjectDir()); !os.IsNotExist(err) {
		t.Fatal("project dir should be gone")
	}
}
