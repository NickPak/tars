package project

import (
	"fmt"
	"os"
	"slices"
)

var (
	instance *StoreManager
)

func InitStoreManager(workDir string) {
	instance = NewStoreManager(workDir)
}

func GetStoreManager() *StoreManager {
	return instance
}

// StoreManager 是项目的磁盘权威：创建/列举/读取/改名/改工作区/删除（级联会话
// 数据）。目录即真相——扫描即列表，本结构无内存注册表；项目/会话的运行时
// 索引（Controller 挂载）在 boot 层（boot.ProjectManager）。
type StoreManager struct {
	workDir string
}

func NewStoreManager(workDir string) *StoreManager {
	return &StoreManager{workDir: workDir}
}

func (m *StoreManager) Startup() error {
	baseDir := GetBaseDir(m.workDir)
	err := os.MkdirAll(baseDir, 0755)
	if err != nil {
		return fmt.Errorf("project: create base dir: %w", err)
	}
	return nil
}

func (m *StoreManager) Shutdown() error {
	return nil
}

// List 扫描 projects/ 列出全部项目（按创建时间升序；meta 损坏/缺失的
// 目录跳过）。
func (m *StoreManager) List() ([]*Metadata, error) {
	entries, err := os.ReadDir(GetBaseDir(m.workDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("project: read projects dir: %w", err)
	}
	var out []*Metadata
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}

		p, err := m.Load(e.Name())
		if err != nil || p == nil {
			continue
		}
		out = append(out, p)
	}
	slices.SortFunc(out, func(a, b *Metadata) int {
		switch {
		case a.CreatedAt < b.CreatedAt:
			return -1
		case a.CreatedAt > b.CreatedAt:
			return 1
		default:
			return 0
		}
	})
	return out, nil
}

// Load 读取单个项目；不存在返回 (nil, nil)。
func (m *StoreManager) Load(id string) (*Metadata, error) {
	metaFile := GetProjectMetaFile(m.workDir, id)
	_, err := os.Stat(metaFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("project: stat meta for %s: %w", id, err)
	}

	raw, err := os.ReadFile(metaFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("project: read meta for %s: %w", id, err)
	}

	// 空结构体反序列化（不经 NewMetaData）：默认值只属于创建点——
	// 旧数据缺失的 title 必须保持为空，展示层的会话标题推导才有效；
	// 若预填默认值再 Unmarshal，缺失字段会残留默认值，把旧项目名
	// "重置"成 DefaultProjectTitle。
	p := &Metadata{}
	if err := p.Unmarshal(raw); err != nil {
		return nil, err
	}
	p.workDir = m.workDir
	if p.WorkspaceDir == "" {
		p.WorkspaceDir = GetWorkspaceDir(m.workDir, p.ID)
	}

	if err := m.MakeWorkspaceDir(p); err != nil {
		return nil, err
	}
	return p, nil
}

func (m *StoreManager) Save(p *Metadata) error {
	err := m.MakeProjectDir(p)
	if err != nil {
		return err
	}

	raw, err := p.Marshal()
	if err != nil {
		return fmt.Errorf("project: encode meta for %s: %w", p.ID, err)
	}

	path := p.GetMetaFile()
	tmp := path + ".tmp"
	err = os.WriteFile(tmp, raw, 0644)
	if err != nil {
		return fmt.Errorf("project: write meta for %s: %w", p.ID, err)
	}

	err = os.Rename(tmp, path)
	if err != nil {
		return fmt.Errorf("project: rename meta for %s: %w", p.ID, err)
	}

	return m.MakeWorkspaceDir(p)
}

// Delete 删除项目目录（级联其下全部会话数据与默认工作区；自定义工作区
// 在用户文件系统上，不动）。
func (m *StoreManager) Delete(id string) error {
	if err := os.RemoveAll(GetProjectDir(m.workDir, id)); err != nil {
		return fmt.Errorf("project: delete %s: %w", id, err)
	}
	return nil
}

func (m *StoreManager) MakeWorkspaceDir(p *Metadata) error {
	err := os.MkdirAll(p.GetWorkspaceDir(), 0755)
	if err != nil {
		return fmt.Errorf("project: create default workspace: %w", err)
	}
	return nil
}

func (m *StoreManager) MakeProjectDir(p *Metadata) error {
	dir := p.GetProjectDir()
	err := os.MkdirAll(dir, 0755)
	if err != nil {
		return fmt.Errorf("project: create dir for %s: %w", p.ID, err)
	}
	return nil
}
