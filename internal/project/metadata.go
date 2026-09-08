package project

import (
	"encoding/json"
	"fmt"
	"time"
)

// DefaultProjectTitle 是新项目的默认标题
const DefaultProjectTitle = "新项目"

// Metadata 是项目实体：工作区的拥有者、会话的分组单位。
type Metadata struct {
	ID string `json:"id"`
	// Title 是项目标题：创建时即填充 DefaultProjectTitle（空值仅存在于
	// 旧数据，展示层对其仍可走会话标题推导）。
	Title        string `json:"title,omitempty"`
	WorkspaceDir string `json:"workspaceDir,omitempty"`
	CreatedAt    int64  `json:"createdAt"`
	UpdatedAt    int64  `json:"updatedAt"`
	workDir      string // 应用数据根（Manager 注入，不入盘）
}

func NewMetaData(workDir string, id string) *Metadata {
	now := time.Now().UnixMilli()
	return &Metadata{
		ID:           id,
		Title:        DefaultProjectTitle,
		WorkspaceDir: GetWorkspaceDir(workDir, id),
		CreatedAt:    now,
		UpdatedAt:    now,
		workDir:      workDir,
	}
}

func (p *Metadata) GetID() string {
	return p.ID
}

func (p *Metadata) GetTitle() string {
	return p.Title
}

func (p *Metadata) SetTitle(title string) {
	p.Title = title
	p.UpdatedAt = time.Now().UnixMilli()
}

// GetWorkspaceDir 解析生效工作区：自定义优先；否则默认目录（惰性创建——
// 仅默认路径自动创建，自定义目录不存在由后续文件/命令操作如实报错）。
// 满足 session.WorkspaceSource（结构式，无需互相 import）。
func (p *Metadata) GetWorkspaceDir() string {
	return p.WorkspaceDir
}

func (p *Metadata) SetWorkspaceDir(dir string) {
	p.WorkspaceDir = dir
	p.UpdatedAt = time.Now().UnixMilli()
}

// GetProjectDir 返回项目的根目录
func (p *Metadata) GetProjectDir() string {
	return GetProjectDir(p.workDir, p.ID)
}

// GetSessionsDir 返回项目的会话存储目录。
func (p *Metadata) GetSessionsDir() string {
	return GetSessionsDir(p.workDir, p.ID)
}

func (p *Metadata) GetMetaFile() string {
	return GetProjectMetaFile(p.workDir, p.ID)
}

func (p *Metadata) Marshal() ([]byte, error) {
	return json.MarshalIndent(p, "", "  ")
}

func (p *Metadata) Unmarshal(raw []byte) error {
	err := json.Unmarshal(raw, p)
	if err != nil {
		return fmt.Errorf("project: decode meta for %s: %w", p.ID, err)
	}
	return nil
}
