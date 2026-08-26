package skill

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

const (
	registryFile = "registry.yaml"
	tmpFile      = ".tmp"
	skillFile    = "SKILL.md"
)

var Categories = []string{
	"documents",   // 文档处理（Word/PDF/PPT）
	"office",      // 办公自动化
	"devops",      // 部署/运维/云
	"development", // 软件开发
	"data",        // 数据处理/分析
	"research",    // 研究/检索
	"system",      // 系统操作
	"design",      // 设计/图像
	"writing",     // 写作/内容
}

type SkillMeta struct {
	Name            string `json:"name" yaml:"-"`
	Description     string `json:"description" yaml:"-"`
	Category        string `json:"category" yaml:"category"`
	Source          string `json:"source" yaml:"source"`
	UpstreamVersion string `json:"upstreamVersion,omitempty" yaml:"upstream_version,omitempty"`
	InstalledAt     string `json:"installedAt,omitempty" yaml:"installed_at"`
	HasScripts      bool   `json:"hasScripts" yaml:"-"`
	FileCount       int    `json:"fileCount" yaml:"-"`
	// Enabled 为 false 时技能对 Agent 完全不可见（索引/检索/加载均排除），
	// 文件与注册表条目保留，可随时恢复——与卸载的区别仅在于可逆。
	// 无 omitempty：开/关状态都显式落盘（兼容见 UnmarshalYAML）。
	Enabled bool `json:"enabled" yaml:"enabled"`
}

// UnmarshalYAML 反序列化时缺省 enabled=true：旧版 registry.yaml 无 enabled 键，
// 直接升级不能把所有已装技能静默禁用。
func (m *SkillMeta) UnmarshalYAML(value *yaml.Node) error {
	type plain SkillMeta
	m.Enabled = true
	return value.Decode((*plain)(m))
}

type Registry struct {
	rootDir      string
	registryPath string
	Skills       map[string]*SkillMeta `yaml:"skills"`
}

func NewRegistry(rootDir string) *Registry {
	return &Registry{
		rootDir:      rootDir,
		registryPath: filepath.Join(rootDir, registryFile),
		Skills:       make(map[string]*SkillMeta),
	}
}

func (r *Registry) Load() error {
	raw, err := os.ReadFile(r.registryPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("skills: read registry: %w", err)
	}

	err = yaml.Unmarshal(raw, r)
	if err != nil {
		return fmt.Errorf("skills: parse registry: %w", err)
	}
	if r.Skills == nil {
		r.Skills = make(map[string]*SkillMeta)
	}

	var desc string
	for name, info := range r.Skills {
		info.Name = name
		raw, err = os.ReadFile(filepath.Join(r.SkillDir(name), skillFile))
		if err != nil {
			continue
		}
		_, desc, err = ParseFrontmatter(raw)
		if err != nil {
			continue
		}
		info.Description = desc
		info.HasScripts = dirExists(filepath.Join(r.SkillDir(name), scriptsDir))
		info.FileCount = countFiles(r.SkillDir(name))
	}
	return nil
}

func (r *Registry) Save() error {
	b, err := yaml.Marshal(r)
	if err != nil {
		return fmt.Errorf("skills: marshal registry: %w", err)
	}
	tmp := r.registryPath + tmpFile
	if err := os.WriteFile(tmp, b, 0644); err != nil {
		return fmt.Errorf("skills: write registry: %w", err)
	}
	return os.Rename(tmp, r.registryPath)
}

func (r *Registry) SkillDir(name string) string {
	return filepath.Join(r.rootDir, name)
}

func (r *Registry) RegistryPath() string { return r.registryPath }

func (r *Registry) Clone() *Registry {
	m := make(map[string]*SkillMeta, len(r.Skills))
	for k, v := range r.Skills {
		m[k] = v
	}
	return &Registry{
		rootDir:      r.rootDir,
		registryPath: r.registryPath,
		Skills:       m,
	}
}

func (r *Registry) FindSkill(name string) (*SkillMeta, bool) {
	info, ok := r.Skills[name]
	return info, ok
}

func (r *Registry) GetCategories() []string {
	// 推荐集去重（保持定义顺序）
	out := make([]string, 0, len(Categories))
	seen := make(map[string]bool)
	for _, c := range Categories {
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}

	// 注册表中的自定义分类（排除已出现的）
	var extra []string
	for _, e := range r.Skills {
		if e.Category != "" && !seen[e.Category] {
			seen[e.Category] = true
			extra = append(extra, e.Category)
		}
	}
	sort.Strings(extra)
	out = append(out, extra...)

	return out
}
