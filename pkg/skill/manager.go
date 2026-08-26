package skill

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"tars/pkg/search"
	"time"
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
// 已发布的 registry 视为不可变（读路径无锁），变更一律 copy-on-write。
func NewManager(workDir string, cfg *Config) *Manager {
	rootDir := filepath.Join(workDir, skillsDir)
	m := &Manager{
		rootDir:   rootDir,
		indexPath: filepath.Join(rootDir, indexFile),
	}
	m.cfg.Store(cfg)
	m.registry.Store(NewRegistry(rootDir))

	return m
}

func (s *Manager) Startup() error {
	err := os.MkdirAll(s.rootDir, 0755)
	if err != nil {
		return fmt.Errorf("skills: create root dir: %w", err)
	}

	err = s.registry.Load().Load()
	if err != nil {
		return err
	}
	return s.GenerateIndex()
}

func (s *Manager) Shutdown() error {
	return nil
}

// GetConfig 返回当前配置（原子读；返回值视为只读）。
func (s *Manager) GetConfig() *Config {
	return s.cfg.Load()
}

// UpdateConfig 原子替换配置（配置保存流调用；nil 忽略）。
func (s *Manager) UpdateConfig(v *Config) error {
	if v == nil {
		return nil
	}
	s.cfg.Store(v)

	return s.GenerateIndex()
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

func (s *Manager) GenerateIndex() error {
	list := s.Enabled() // 禁用技能不进索引（三档阈值计数同口径）

	// 每次重建前清掉旧的类别索引页（避免切档残留）
	if err := os.RemoveAll(filepath.Join(s.rootDir, "index")); err != nil {
		return fmt.Errorf("skills: clear index dir: %w", err)
	}

	// 无技能：写空文件（前缀注入据此跳过，避免注入 "_No skills installed_" 噪音）
	if len(list) == 0 {
		return os.WriteFile(s.IndexPath(), nil, 0644)
	}

	var content string
	cfg := s.GetConfig()
	switch {
	case len(list) <= cfg.TierFullMax:
		content = renderFullIndex(list)
	case len(list) <= cfg.TierResidentMax:
		var err error
		content, err = s.renderCategoryIndex(list)
		if err != nil {
			return err
		}
	default:
		content = renderDiscoverHint(groupsByCategory(list))
	}

	tmp := s.IndexPath() + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0644); err != nil {
		return fmt.Errorf("skills: write INDEX.md: %w", err)
	}
	return os.Rename(tmp, s.IndexPath())
}

func (s *Manager) RenderIndex() string {
	raw, err := os.ReadFile(s.IndexPath())
	if err != nil {
		return ""
	}
	return string(raw)
}

func (s *Manager) renderCategoryIndex(list []*SkillMeta) (string, error) {
	groups := groupsByCategory(list)
	if err := os.MkdirAll(filepath.Join(s.rootDir, "index"), 0755); err != nil {
		return "", err
	}

	var b strings.Builder
	b.WriteString("# Available Skills by Category\n\n")
	// 表头说明文档格式（每行 = 类别 + 该类全部技能名）与两跳导航；
	// discover_tools 的用法教学由工具自身 description 承担（单一事实源），不在此重复。
	b.WriteString("Each line below is a category followed by the names of all skills in it. ")
	b.WriteString("Read `index/<category>.md` for their full descriptions, or use ")
	b.WriteString("`discover_tools` to search by need.\n\n")

	// 类别名排序，稳定输出
	names := make([]string, 0, len(groups))
	for c := range groups {
		names = append(names, c)
	}
	sort.Strings(names)

	for _, c := range names {
		items := groups[c]
		fmt.Fprintf(&b, "- **%s** (%d skills) — %s\n", c, len(items), skillNamesLine(items))

		// 生成类别索引页
		var cb strings.Builder
		fmt.Fprintf(&cb, "# %s Skills\n\n", c)
		for _, sk := range items {
			fmt.Fprintf(&cb, "## %s\n%s\n\n", sk.Name, sk.Description)
		}
		page := filepath.Join(s.rootDir, "index", c+".md")
		if err := os.WriteFile(page, []byte(cb.String()), 0644); err != nil {
			return "", fmt.Errorf("skills: write index/%s.md: %w", c, err)
		}
	}
	return b.String(), nil
}

func (s *Manager) Install(srcPath, category string, overwrite bool) (string, error) {
	artifact, cleanup, err := s.normalize(srcPath)
	if err != nil {
		return "", err
	}
	if cleanup != nil {
		defer cleanup()
	}

	name, desc, err := readArtifactInfo(artifact)
	if err != nil {
		return "", err
	}

	dst := s.SkillDir(name)
	if dirExists(dst) && !overwrite {
		return "", fmt.Errorf("skill %q already installed (choose overwrite or cancel)", name)
	}

	// 覆盖：先清旧目录（保留"以 frontmatter name 为准"的存储约定）
	if dirExists(dst) {
		if err := os.RemoveAll(dst); err != nil {
			return "", fmt.Errorf("skills: remove existing %q: %w", name, err)
		}
	}
	if err := copyDir(artifact, dst); err != nil {
		return "", err
	}

	// 登记完整元信息（渠道 local）：规范外字段写盘，规范内字段从磁盘读出后缓存
	if category == "" {
		category = "misc"
	}
	info := &SkillMeta{
		Name:        name,
		Description: desc,
		Category:    strings.ToLower(strings.TrimSpace(category)),
		Source:      "local",
		InstalledAt: time.Now().Format("2006-01-02"),
		HasScripts:  dirExists(filepath.Join(dst, scriptsDir)),
		FileCount:   countFiles(dst),
		Enabled:     true,
	}
	if err := s.AddSkill(name, info); err != nil {
		return "", err
	}

	// 重跑索引
	if err := s.GenerateIndex(); err != nil {
		return "", err
	}
	return name, nil
}

func (s *Manager) Uninstall(name string) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	if err := os.RemoveAll(s.SkillDir(name)); err != nil {
		return fmt.Errorf("skills: remove %q: %w", name, err)
	}
	if err := s.RemoveSkill(name); err != nil {
		return err
	}
	return s.GenerateIndex()
}

func (s *Manager) normalize(srcPath string) (dir string, cleanup func(), err error) {
	info, err := os.Stat(srcPath)
	if err != nil {
		return "", nil, fmt.Errorf("skills: stat artifact: %w", err)
	}

	switch {
	case info.IsDir():
		return srcPath, nil, nil // 直接校验，不拷贝

	case strings.EqualFold(info.Name(), "SKILL.md"):
		// 单文件：包装为 <name>/SKILL.md
		name, _, perr := ParseFrontmatter(readFile(srcPath))
		if perr != nil {
			return "", nil, perr
		}
		tmp := filepath.Join(os.TempDir(), "tars-skill-"+name)
		if err := os.RemoveAll(tmp); err != nil {
			return "", nil, err
		}
		if err := os.MkdirAll(tmp, 0755); err != nil {
			return "", nil, err
		}
		if err := copyFile(srcPath, filepath.Join(tmp, "SKILL.md")); err != nil {
			return "", nil, err
		}
		return tmp, func() { _ = os.RemoveAll(tmp) }, nil

	default:
		// 压缩包：解压到临时目录后定位 SKILL.md
		tmp, err := os.MkdirTemp("", "tars-skill-*")
		if err != nil {
			return "", nil, err
		}
		if err := extract(srcPath, tmp); err != nil {
			_ = os.RemoveAll(tmp)
			return "", nil, err
		}
		dir, err := locateSKILL(tmp)
		if err != nil {
			_ = os.RemoveAll(tmp)
			return "", nil, err
		}
		return dir, func() { _ = os.RemoveAll(tmp) }, nil
	}
}

// Search 按自然语言需求检索启用中的技能（BM25 + 前缀索引，引擎在
// pkg/search，与 MCP 工具检索共用同一实现）。
func (s *Manager) Search(query string, limit int) ([]*SkillMeta, error) {
	list := s.Enabled() // 禁用技能不参与检索

	items := make([]search.Item[*SkillMeta], 0, len(list))
	for _, sk := range list {
		items = append(items, search.Item[*SkillMeta]{
			Text:    sk.Name + " " + sk.Description + " " + sk.Category,
			Payload: sk,
		})
	}
	return search.Search(items, query, limit), nil
}
