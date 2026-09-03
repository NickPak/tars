package skill

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	// maxSkillDescriptionLen 限制 description 长度：它会被全量注入索引
	// （系统前缀），超长描述直接抬高每轮前缀成本。
	maxSkillDescriptionLen = 500
	// maxSkillBodyBytes 限制 SKILL.md 正文大小：技能应是一份聚焦的操作
	// 手册，而非资料堆积。
	maxSkillBodyBytes = 64 * 1024
)

// WriteSkill 创建或覆盖一个技能的 SKILL.md（write_skill 工具的落盘端，
// 调用前已过审批门）。
//
// 与 Install（外部制品安装）的差异：只写 SKILL.md 单文件，不动 scripts/；
// 新建技能默认禁用（Enabled=false）——模型写入与 Agent 可见之间保留一道
// 人工闸门（设置页启用）；覆盖时保留原技能的启用状态、来源、安装日期与
// 分类（未显式传 category 时）。返回 created 表示本次是新建（false 为覆盖）。
func (s *Manager) WriteSkill(name, description, category, body string, overwrite bool) (created bool, err error) {
	if err := ValidateName(name); err != nil {
		return false, err
	}
	description = strings.TrimSpace(description)
	if description == "" {
		return false, fmt.Errorf("skills: description is required (one line, shown in the skill catalog)")
	}
	if len(description) > maxSkillDescriptionLen {
		return false, fmt.Errorf("skills: description too long (%d > %d chars); keep it one line", len(description), maxSkillDescriptionLen)
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return false, fmt.Errorf("skills: content is required (the SKILL.md markdown body)")
	}
	if len(body) > maxSkillBodyBytes {
		return false, fmt.Errorf("skills: content too large (%d > %d bytes); keep the skill a focused playbook", len(body), maxSkillBodyBytes)
	}

	var old *SkillMeta
	if o, ok := s.GetRegistry().FindSkill(name); ok {
		old = o
	}
	existed := old != nil || dirExists(s.SkillDir(name))
	if existed && !overwrite {
		return false, fmt.Errorf("skills: skill %q already exists; call again with overwrite=true to replace it", name)
	}

	dst := s.SkillDir(name)
	if err := os.MkdirAll(dst, 0755); err != nil {
		return false, fmt.Errorf("skills: create skill dir: %w", err)
	}
	md := filepath.Join(dst, skillFile)
	tmp := md + tmpFile
	if err := os.WriteFile(tmp, renderSKILLMD(name, description, body), 0644); err != nil {
		return false, fmt.Errorf("skills: write SKILL.md: %w", err)
	}
	if err := os.Rename(tmp, md); err != nil {
		return false, fmt.Errorf("skills: commit SKILL.md: %w", err)
	}

	// 元信息：覆盖时保留原条目的启用状态/来源/安装日期；新建默认禁用、
	// 来源 agent（与 GUI 安装的 local 区分，供审计）。
	info := &SkillMeta{
		Name:        name,
		Description: description,
		Category:    normalizeCategory(category),
		Source:      "agent",
		InstalledAt: time.Now().Format("2006-01-02"),
		Enabled:     false,
	}
	if old != nil {
		info.Enabled = old.Enabled
		info.Source = old.Source
		info.InstalledAt = old.InstalledAt
		if info.Source == "" {
			info.Source = "agent"
		}
		if category == "" && old.Category != "" {
			info.Category = old.Category
		}
	}
	info.HasScripts = dirExists(filepath.Join(dst, scriptsDir))
	info.FileCount = countFiles(dst)

	if err := s.AddSkill(name, info); err != nil {
		return false, err
	}
	return !existed, s.GenerateIndex()
}

// renderSKILLMD 渲染 SKILL.md 全文：frontmatter 由工具参数生成（yaml.Marshal
// 保证特殊字符转义正确），正文原样追加。
func renderSKILLMD(name, description, body string) []byte {
	fm, _ := yaml.Marshal(struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}{name, description})
	var b strings.Builder
	b.WriteString("---\n")
	b.Write(fm)
	b.WriteString("---\n\n")
	b.WriteString(body)
	b.WriteByte('\n')
	return []byte(b.String())
}

func normalizeCategory(category string) string {
	normalized := strings.ToLower(strings.TrimSpace(category))
	if normalized == "" {
		return "misc"
	}
	return normalized
}
