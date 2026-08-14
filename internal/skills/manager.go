package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync/atomic"
)

const (
	skillsDir  = "skills"
	indexFile  = "INDEX.md"
	scriptsDir = "scripts"
)

var instance *Manager

type Manager struct {
	cfg       *Config
	rootDir   string
	indexPath string
	registry  atomic.Pointer[Registry]
}

func GetManager() *Manager { return instance }

func InitManager(workDir string, cfg *Config) error {
	rootDir := filepath.Join(workDir, skillsDir)
	err := os.MkdirAll(rootDir, 0755)
	if err != nil {
		return fmt.Errorf("skills: create root dir: %w", err)
	}

	m := &Manager{
		cfg:       cfg,
		rootDir:   rootDir,
		indexPath: filepath.Join(rootDir, indexFile),
	}

	reg := NewRegistry(rootDir)
	err = reg.Load()
	if err != nil {
		return err
	}
	m.registry.Store(reg)

	instance = m
	return nil
}

func (s *Manager) UpdateConfig(v *Config) {
	s.cfg = v
}

func (s *Manager) RootDir() string {
	return s.rootDir
}

func (s *Manager) IndexPath() string {
	return s.indexPath
}

func (s *Manager) SkillDir(name string) string {
	return filepath.Join(s.rootDir, name)
}

func (s *Manager) GetRegistry() *Registry {
	return s.registry.Load()
}

// List 返回全部已安装技能（从内存 registry 读，按 name 排序）。
func (s *Manager) List() []*SkillMeta {
	reg := s.GetRegistry()
	if reg == nil {
		return nil
	}

	out := make([]*SkillMeta, 0, len(reg.Skills))
	for name, info := range reg.Skills {
		info.Name = name
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Categories 返回分类下拉框选项：内置推荐集在前，注册表自定义分类在后。
func (s *Manager) Categories() []string {
	reg := s.GetRegistry()
	if reg == nil {
		return nil
	}
	return reg.GetCategories()
}

// LoadSkill 返回技能 SKILL.md 的全文。
func (s *Manager) LoadSkill(name string) (string, error) {
	reg := s.GetRegistry()
	if reg == nil {
		return "", fmt.Errorf("skills: registry not loaded")
	}

	_, ok := reg.FindSkill(name)
	if !ok {
		return "", fmt.Errorf("skills: skill %q not found", name)
	}
	raw, err := os.ReadFile(filepath.Join(s.SkillDir(name), skillFile))
	if err != nil {
		return "", fmt.Errorf("skills: load %q: %w", name, err)
	}
	return string(raw), nil
}

// AddSkill 登记/覆盖一个技能（copy-on-write：先写盘成功再替换内存）。
func (s *Manager) AddSkill(name string, info *SkillMeta) error {
	next := s.GetRegistry().Clone()
	next.Skills[name] = info
	if err := next.Save(); err != nil {
		return err
	}
	s.registry.Store(next)
	return nil
}

// RemoveSkill 移除一个技能（copy-on-write）。
func (s *Manager) RemoveSkill(name string) error {
	next := s.GetRegistry().Clone()
	delete(next.Skills, name)
	if err := next.Save(); err != nil {
		return err
	}
	s.registry.Store(next)
	return nil
}
