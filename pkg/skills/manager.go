package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
)

const (
	skillsDir  = "skills"
	indexFile  = "INDEX.md"
	scriptsDir = "scripts"
)

type Manager struct {
	cfg       atomic.Pointer[Config]
	rootDir   string
	indexPath string
	registry  atomic.Pointer[Registry]
}

// NewManager 创建技能管理器并加载磁盘注册表。
// 技能管理器为普通对象，由装配层（internal/boot）创建并注入；
// 已发布的 registry 视为不可变（读路径无锁），变更一律 copy-on-write。
func NewManager(workDir string, cfg *Config) (*Manager, error) {
	rootDir := filepath.Join(workDir, skillsDir)
	err := os.MkdirAll(rootDir, 0755)
	if err != nil {
		return nil, fmt.Errorf("skills: create root dir: %w", err)
	}
	if cfg == nil {
		cfg = &Config{}
		cfg.Validate()
	}

	m := &Manager{
		rootDir:   rootDir,
		indexPath: filepath.Join(rootDir, indexFile),
	}
	m.cfg.Store(cfg)

	reg := NewRegistry(rootDir)
	err = reg.Load()
	if err != nil {
		return nil, err
	}
	m.registry.Store(reg)

	return m, nil
}

// GetConfig 返回当前配置（原子读；返回值视为只读）。
func (s *Manager) GetConfig() *Config {
	return s.cfg.Load()
}

// UpdateConfig 原子替换配置（配置保存流调用；nil 忽略）。
func (s *Manager) UpdateConfig(v *Config) {
	if v == nil {
		return
	}
	s.cfg.Store(v)
}

// SetTiers 设置索引档位阈值（测试辅助；运行时由配置决定）。
func (s *Manager) SetTiers(fullMax, residentMax int) {
	c := *s.GetConfig()
	c.TierFullMax = fullMax
	c.TierResidentMax = residentMax
	s.cfg.Store(&c)
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

// List 返回全部已安装技能（从内存 registry 读，按 name 排序），含禁用项（GUI 展示用）。
// 返回条目为拷贝：registry 发布后不可变，调用方拿到的不与任何读路径共享。
func (s *Manager) List() []*SkillMeta {
	reg := s.GetRegistry()
	if reg == nil {
		return nil
	}

	out := make([]*SkillMeta, 0, len(reg.Skills))
	for name, info := range reg.Skills {
		cp := *info
		cp.Name = name
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Enabled 返回启用中的技能（索引生成、检索、加载的可见口径）。
func (s *Manager) Enabled() []*SkillMeta {
	all := s.List()
	out := make([]*SkillMeta, 0, len(all))
	for _, sk := range all {
		if sk.Enabled {
			out = append(out, sk)
		}
	}
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

	info, ok := reg.FindSkill(name)
	if !ok {
		return "", fmt.Errorf("skills: skill %q not found", name)
	}
	if !info.Enabled {
		return "", fmt.Errorf("skills: skill %q is disabled", name)
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
	cp := *info // 防御性拷贝：调用方事后改其持有的指针不污染已发布 registry
	next.Skills[name] = &cp
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

// SetCategory 更新已安装技能的分类并重跑索引（类别目录/索引页消费分类）。
// category 大小写不敏感，空值归一为 "misc"；技能不存在时报错。
func (s *Manager) SetCategory(name, category string) error {
	reg := s.GetRegistry()
	old, ok := reg.FindSkill(name)
	if !ok {
		return fmt.Errorf("skills: skill %q not found", name)
	}
	normalized := strings.ToLower(strings.TrimSpace(category))
	if normalized == "" {
		normalized = "misc"
	}
	if normalized == old.Category {
		return nil
	}

	next := reg.Clone()
	entry := *old // Clone 浅拷贝共享条目指针，先拷贝条目再改
	entry.Category = normalized
	next.Skills[name] = &entry
	if err := next.Save(); err != nil {
		return err
	}
	s.registry.Store(next)

	return s.GenerateIndex()
}

// SetEnabled 启用/禁用技能（copy-on-write：先写盘成功再替换内存），并重跑索引。
// 禁用后技能对 Agent 不可见（索引/检索/加载排除），文件与条目保留。
func (s *Manager) SetEnabled(name string, enabled bool) error {
	reg := s.GetRegistry()
	old, ok := reg.FindSkill(name)
	if !ok {
		return fmt.Errorf("skills: skill %q not found", name)
	}
	if old.Enabled == enabled {
		return nil
	}

	next := reg.Clone()
	entry := *old // Clone 浅拷贝共享条目指针，先拷贝条目再改
	entry.Enabled = enabled
	next.Skills[name] = &entry
	if err := next.Save(); err != nil {
		return err
	}
	s.registry.Store(next)

	return s.GenerateIndex()
}
