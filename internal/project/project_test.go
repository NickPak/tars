package project

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Create：project.json 落盘 + 默认工作区目录急式创建；GetWorkspaceDir
// 解析到默认目录。
func TestCreateProject(t *testing.T) {
	m := NewManager(t.TempDir())
	p, err := m.Create()
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if p.ID == "" {
		t.Fatal("empty project id")
	}
	if _, err := os.Stat(filepath.Join(m.DirOf(p), MetaFile)); err != nil {
		t.Fatalf("project.json should exist: %v", err)
	}
	ws := p.GetWorkspaceDir()
	if ws != DefaultWorkspaceDir(m.workDir, p.ID) {
		t.Fatalf("default workspace = %q", ws)
	}
	if info, err := os.Stat(ws); err != nil || !info.IsDir() {
		t.Fatalf("default workspace should be created eagerly: %v", err)
	}
}

// List 扫描 projects/（目录即真相）：按创建时间升序；无 meta 的目录跳过。
func TestListProjects(t *testing.T) {
	m := NewManager(t.TempDir())
	p1, _ := m.Create()
	time.Sleep(2 * time.Millisecond)
	p2, _ := m.Create()
	// 噪声：空目录与非目录文件
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

// Get：不存在返回 (nil, nil)。
func TestGetMissing(t *testing.T) {
	m := NewManager(t.TempDir())
	p, err := m.Get("nope")
	if err != nil || p != nil {
		t.Fatalf("missing project should be (nil, nil), got %v, %v", p, err)
	}
}

// SetWorkspaceDir：自定义目录必须已存在；设置后持久化且 GetWorkspaceDir
// 返回新值；不存在的目录拒绝。
func TestSetWorkspaceDir(t *testing.T) {
	m := NewManager(t.TempDir())
	p, _ := m.Create()

	if err := m.SetWorkspaceDir(p, filepath.Join(t.TempDir(), "nonexistent")); err == nil {
		t.Fatal("should reject nonexistent dir")
	}

	custom := t.TempDir()
	if err := m.SetWorkspaceDir(p, custom); err != nil {
		t.Fatalf("set workspace: %v", err)
	}
	if p.GetWorkspaceDir() != custom {
		t.Fatalf("GetWorkspaceDir = %q, want %q", p.GetWorkspaceDir(), custom)
	}

	// 重新加载后保持（持久化）
	p2, err := m.Get(p.ID)
	if err != nil || p2 == nil {
		t.Fatalf("reload: %v", err)
	}
	if p2.GetWorkspaceDir() != custom {
		t.Fatalf("custom workspace not persisted: %q", p2.GetWorkspaceDir())
	}
}

// 恢复场景：默认工作区被外部删除后，GetWorkspaceDir 惰性重建（默认路径
// 才自动创建；自定义目录不自动创建——见 SetWorkspaceDir 的存在性校验）。
func TestDefaultWorkspaceRecreatedLazily(t *testing.T) {
	m := NewManager(t.TempDir())
	p, _ := m.Create()
	def := DefaultWorkspaceDir(m.workDir, p.ID)
	if err := os.RemoveAll(def); err != nil {
		t.Fatal(err)
	}
	if got := p.GetWorkspaceDir(); got != def {
		t.Fatalf("GetWorkspaceDir = %q", got)
	}
	if info, err := os.Stat(def); err != nil || !info.IsDir() {
		t.Fatal("default workspace should be recreated lazily")
	}
}

// SetTitle：显式命名写穿磁盘。
func TestSetTitle(t *testing.T) {
	m := NewManager(t.TempDir())
	p, _ := m.Create()
	if p.Title != "" {
		t.Fatal("new project should be untitled (derived display)")
	}
	if err := m.SetTitle(p, "我的项目"); err != nil {
		t.Fatalf("set title: %v", err)
	}
	p2, _ := m.Get(p.ID)
	if p2.Title != "我的项目" {
		t.Fatalf("title not persisted: %q", p2.Title)
	}
}

// SweepIfEmpty：零会话空项目（默认工作区且为空）删除；有产出物或自定义
// 工作区的保留。
func TestSweepIfEmpty(t *testing.T) {
	m := NewManager(t.TempDir())

	empty, _ := m.Create()
	if ok, err := m.SweepIfEmpty(empty); err != nil || !ok {
		t.Fatalf("empty default project should be swept: %v, %v", ok, err)
	}
	if _, err := os.Stat(m.DirOf(empty)); !os.IsNotExist(err) {
		t.Fatal("swept project dir should be gone")
	}

	withFile, _ := m.Create()
	if err := os.WriteFile(filepath.Join(DefaultWorkspaceDir(m.workDir, withFile.ID), "out.txt"), nil, 0644); err != nil {
		t.Fatal(err)
	}
	if ok, _ := m.SweepIfEmpty(withFile); ok {
		t.Fatal("project with workspace files must be kept")
	}

	custom, _ := m.Create()
	if err := m.SetWorkspaceDir(custom, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if ok, _ := m.SweepIfEmpty(custom); ok {
		t.Fatal("project with custom workspace must be kept")
	}
}

// Delete：级联删除项目目录（含其下会话数据与默认工作区）。
func TestDeleteProject(t *testing.T) {
	m := NewManager(t.TempDir())
	p, _ := m.Create()
	sessDir := filepath.Join(m.DirOf(p), SessionsDirName, "s-1", ".data")
	if err := os.MkdirAll(sessDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := m.Delete(p.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := os.Stat(m.DirOf(p)); !os.IsNotExist(err) {
		t.Fatal("project dir should be gone")
	}
}
