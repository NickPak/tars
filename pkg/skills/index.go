package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

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
	switch {
	case len(list) <= s.cfg.TierFullMax:
		content = renderFullIndex(list)
	case len(list) <= s.cfg.TierResidentMax:
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

func renderFullIndex(list []*SkillMeta) string {
	var b strings.Builder
	b.WriteString("# Available Skills\n\n")
	if len(list) == 0 {
		b.WriteString("_No skills installed._\n")
	}
	for _, sk := range list {
		fmt.Fprintf(&b, "## %s\n%s\n\n", sk.Name, sk.Description)
	}
	return b.String()
}

func (s *Manager) renderCategoryIndex(list []*SkillMeta) (string, error) {
	groups := groupsByCategory(list)
	if err := os.MkdirAll(filepath.Join(s.rootDir, "index"), 0755); err != nil {
		return "", err
	}

	var b strings.Builder
	b.WriteString("# Available Skills by Category\n\n")
	b.WriteString("Read `index/<category>.md` for the skills in a category, or use ")
	b.WriteString("`discover_tools` to search by need.\n\n")

	// 类别名排序，稳定输出
	names := make([]string, 0, len(groups))
	for c := range groups {
		names = append(names, c)
	}
	sort.Strings(names)

	for _, c := range names {
		items := groups[c]
		fmt.Fprintf(&b, "- **%s**（%d 个）— %s\n", c, len(items), oneLineDesc(items))

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

func renderDiscoverHint(groups map[string][]*SkillMeta) string {
	names := make([]string, 0, len(groups))
	for c := range groups {
		names = append(names, c)
	}
	sort.Strings(names)

	var b strings.Builder
	b.WriteString("# Skills\n\n")
	b.WriteString("Skills are available but not listed inline (too many). When you encounter an unfamiliar task, call `discover_tools` with a natural-language description of what you need.\n\n")
	b.WriteString("Installed categories: ")
	b.WriteString(strings.Join(names, ", "))
	b.WriteByte('\n')
	return b.String()
}

func groupsByCategory(list []*SkillMeta) map[string][]*SkillMeta {
	groups := map[string][]*SkillMeta{}
	for _, sk := range list {
		c := sk.Category
		if c == "" {
			c = "misc"
		}
		groups[c] = append(groups[c], sk)
	}
	for c := range groups {
		sort.Slice(groups[c], func(i, j int) bool { return groups[c][i].Name < groups[c][j].Name })
	}
	return groups
}

func oneLineDesc(items []*SkillMeta) string {
	if len(items) == 0 {
		return ""
	}
	return items[0].Description
}
