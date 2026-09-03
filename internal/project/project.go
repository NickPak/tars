// Package project 定义项目实体与磁盘存储：项目是工作区（项目根目录）的
// 拥有者，一个项目下辖多个会话（projects/<pid>/sessions/<sid>/）。
// 领域模型即存储模型——目录即真相，扫描即列表，无注册表。
//
// 工作区是项目属性：自定义绝对路径存于 project.json（workspaceDir），
// 为空时使用项目级默认目录 projects/<pid>/workspace/（仅默认路径自动
// 创建——自定义目录不存在是用户侧事实，不自动创建以免掩盖）。
package project

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/google/uuid"
)

const (
	// BaseDirName 是项目存储的顶层目录名（<workDir>/projects/）。
	BaseDirName = "projects"
	// MetaFile 是项目元数据文件名。
	MetaFile = "project.json"
	// WorkspaceDirName 是项目默认工作区的目录名。
	WorkspaceDirName = "workspace"
	// SessionsDirName 是项目下会话存储的子目录名。
	SessionsDirName = "sessions"
)

// Project 是项目实体：工作区的拥有者、会话的分组单位。
type Project struct {
	ID string `json:"id"`
	// Title 是用户显式命名的项目标题；空 = 未命名（展示层从项目内最近
	// 会话的标题推导）。显式优先：一旦命名，不再跟随会话自动命名。
	Title string `json:"title,omitempty"`
	// WorkspaceDir 是用户显式设置的自定义工作区绝对路径；空 = 使用
	// 项目级默认目录（GetWorkspaceDir 解析）。
	WorkspaceDir string `json:"workspaceDir,omitempty"`
	CreatedAt    int64  `json:"createdAt"`
	UpdatedAt    int64  `json:"updatedAt"`
	workDir      string // 应用数据根（Manager 注入，不入盘）
}

// GetWorkspaceDir 解析生效工作区：自定义优先；否则默认目录（惰性创建——
// 仅默认路径自动创建，自定义目录不存在由后续文件/命令操作如实报错）。
// 满足 session.WorkspaceSource（结构式，无需互相 import）。
func (p *Project) GetWorkspaceDir() string {
	if p.WorkspaceDir != "" {
		return p.WorkspaceDir
	}
	def := DefaultWorkspaceDir(p.workDir, p.ID)
	_ = os.MkdirAll(def, 0755) // best-effort：真实错误由工具执行层暴露
	return def
}

// --- 路径助手（全部路径计算的唯一家） ---

func GetBaseDir(workDir string) string {
	return filepath.Join(workDir, BaseDirName)
}

func GetDir(workDir, projectID string) string {
	return filepath.Join(GetBaseDir(workDir), projectID)
}

// DefaultWorkspaceDir 返回项目级默认工作区路径。
func DefaultWorkspaceDir(workDir, projectID string) string {
	return filepath.Join(GetDir(workDir, projectID), WorkspaceDirName)
}

// GetSessionsDir 返回项目下的会话存储目录。
func GetSessionsDir(workDir, projectID string) string {
	return filepath.Join(GetDir(workDir, projectID), SessionsDirName)
}

// Manager 是项目的磁盘权威：创建/列举/读取/改工作区/删除（级联会话数据）。
type Manager struct {
	workDir string
}

func NewManager(workDir string) *Manager {
	return &Manager{workDir: workDir}
}

// DirOf 返回项目目录（会话路径由 session 包在此基础上派生）。
func (m *Manager) DirOf(p *Project) string {
	return GetDir(m.workDir, p.ID)
}

// SessionsDirOf 返回项目的会话存储目录。
func (m *Manager) SessionsDirOf(p *Project) string {
	return GetSessionsDir(m.workDir, p.ID)
}

// Create 创建新项目：project.json 落盘 + 默认工作区目录创建（急式——
// 创建即就绪，后续会话启动无需再补）。
func (m *Manager) Create() (*Project, error) {
	now := time.Now().UnixMilli()
	p := &Project{
		ID:        uuid.NewString(),
		CreatedAt: now,
		UpdatedAt: now,
		workDir:   m.workDir,
	}
	if err := m.save(p); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(DefaultWorkspaceDir(m.workDir, p.ID), 0755); err != nil {
		return nil, fmt.Errorf("project: create default workspace: %w", err)
	}
	return p, nil
}

// List 扫描 projects/ 列出全部项目（按创建时间升序；meta 损坏/缺失的
// 目录跳过）。
func (m *Manager) List() ([]*Project, error) {
	entries, err := os.ReadDir(GetBaseDir(m.workDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("project: read projects dir: %w", err)
	}
	var out []*Project
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p, err := m.Get(e.Name())
		if err != nil || p == nil {
			continue
		}
		out = append(out, p)
	}
	slices.SortFunc(out, func(a, b *Project) int {
		return cmp64(a.CreatedAt, b.CreatedAt)
	})
	return out, nil
}

// Get 读取单个项目；不存在返回 (nil, nil)。
func (m *Manager) Get(id string) (*Project, error) {
	raw, err := os.ReadFile(filepath.Join(GetDir(m.workDir, id), MetaFile))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("project: read meta for %s: %w", id, err)
	}
	p := &Project{}
	if err := json.Unmarshal(raw, p); err != nil {
		return nil, fmt.Errorf("project: decode meta for %s: %w", id, err)
	}
	p.workDir = m.workDir
	return p, nil
}

// SetWorkspaceDir 设置项目的自定义工作区（零消息守卫在 App 层——它要
// 检查项目内全部会话）。目录必须已存在。
func (m *Manager) SetWorkspaceDir(p *Project, dir string) error {
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return fmt.Errorf("workspace 目录不存在: %s", dir)
	}
	p.WorkspaceDir = dir
	p.UpdatedAt = time.Now().UnixMilli()
	return m.save(p)
}

// SetTitle 显式命名项目（写穿 project.json；此后不再跟随会话自动命名）。
func (m *Manager) SetTitle(p *Project, title string) error {
	p.Title = title
	p.UpdatedAt = time.Now().UnixMilli()
	return m.save(p)
}

// Delete 删除项目目录（级联其下全部会话数据与默认工作区；自定义工作区
// 在用户文件系统上，不动）。
func (m *Manager) Delete(id string) error {
	if err := os.RemoveAll(GetDir(m.workDir, id)); err != nil {
		return fmt.Errorf("project: delete %s: %w", id, err)
	}
	return nil
}

// SweepIfEmpty 清理零会话空项目（启动时由 App 调用）：无自定义工作区且
// 默认工作区为空目录 → 删除整个项目目录，返回 true。自定义工作区或目录
// 内有文件（agent 产出物）的项目保留——用户数据不擅自清理。
func (m *Manager) SweepIfEmpty(p *Project) (bool, error) {
	if p.WorkspaceDir != "" {
		return false, nil
	}
	entries, err := os.ReadDir(DefaultWorkspaceDir(m.workDir, p.ID))
	if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("project: sweep check %s: %w", p.ID, err)
	}
	if len(entries) > 0 {
		return false, nil
	}
	return true, m.Delete(p.ID)
}

func (m *Manager) save(p *Project) error {
	dir := GetDir(m.workDir, p.ID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("project: create dir for %s: %w", p.ID, err)
	}
	raw, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return fmt.Errorf("project: encode meta for %s: %w", p.ID, err)
	}
	path := filepath.Join(dir, MetaFile)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0644); err != nil {
		return fmt.Errorf("project: write meta for %s: %w", p.ID, err)
	}
	return os.Rename(tmp, path)
}

func cmp64(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}
