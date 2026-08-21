package main

import (
	"fmt"
	"strings"
	"tars/pkg/skills"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// ListSkills returns all installed skills (frontmatter + registry metadata).
func (s *AgentService) ListSkills() ([]*skills.SkillMeta, error) {
	st := s.app.GetSkillMgr()
	if st == nil {
		return nil, fmt.Errorf("skills store not initialized")
	}
	return st.List(), nil
}

// SkillCategories returns the distinct categories seen in the registry,
// for the install dialog's category dropdown.
func (s *AgentService) SkillCategories() ([]string, error) {
	st := s.app.GetSkillMgr()
	if st == nil {
		return nil, fmt.Errorf("skills store not initialized")
	}
	return st.Categories(), nil
}

// InstallSkill installs a skill from a local artifact (SKILL.md file,
// directory, or .zip/.tar.gz archive). Returns the installed skill name.
func (s *AgentService) InstallSkill(srcPath, category string, overwrite bool) (string, error) {
	st := s.app.GetSkillMgr()
	if st == nil {
		return "", fmt.Errorf("skills store not initialized")
	}
	if srcPath == "" {
		return "", fmt.Errorf("artifact path is required")
	}
	return st.Install(srcPath, category, overwrite)
}

// UninstallSkill removes an installed skill (directory + registry entry)
// and regenerates the index.
func (s *AgentService) UninstallSkill(name string) error {
	st := s.app.GetSkillMgr()
	if st == nil {
		return fmt.Errorf("skills store not initialized")
	}
	return st.Uninstall(name)
}

// SetSkillCategory updates an installed skill's category (registry + index
// regeneration; takes effect in the next conversation turn).
func (s *AgentService) SetSkillCategory(name, category string) error {
	st := s.app.GetSkillMgr()
	if st == nil {
		return fmt.Errorf("skills store not initialized")
	}
	return st.SetCategory(name, category)
}

// SetSkillEnabled enables/disables an installed skill (registry + index
// regeneration; a disabled skill is invisible to the agent: excluded from
// the index, discovery search and load_skill).
func (s *AgentService) SetSkillEnabled(name string, enabled bool) error {
	st := s.app.GetSkillMgr()
	if st == nil {
		return fmt.Errorf("skills store not initialized")
	}
	return st.SetEnabled(name, enabled)
}

// SearchSkills searches installed skills by natural-language query — the same
// BM25 retrieval and result limit as the agent-facing discover_tools tool,
// so the settings page shows exactly what the model would get. An empty query
// returns the full list.
func (s *AgentService) SearchSkills(query string) ([]*skills.SkillMeta, error) {
	st := s.app.GetSkillMgr()
	if st == nil {
		return nil, fmt.Errorf("skills store not initialized")
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return st.List(), nil
	}
	return st.Search(query, st.GetConfig().DiscoverResultLimit)
}

// OpenSkillFileDialog shows the OS native picker for a skill artifact FILE
// (SKILL.md, .zip, or .tar.gz). Returns the selected path (empty if cancelled).
// 独立于目录对话框：Windows 原生对话框启用"仅文件夹"模式后无法同时显示文件。
func (s *AgentService) OpenSkillFileDialog() (string, error) {
	dialog := application.Get().Dialog.OpenFile().
		SetTitle("选择技能制品文件（SKILL.md / zip / tar.gz）").
		CanChooseFiles(true).
		CanChooseDirectories(false).
		AddFilter("技能制品", "*.md;*.zip;*.tar.gz;*.tgz").
		AddFilter("SKILL.md 文件", "*.md").
		AddFilter("压缩包", "*.zip;*.tar.gz;*.tgz").
		AddFilter("所有文件", "*.*")

	result, err := dialog.PromptForSingleSelection()
	if err != nil {
		if isDialogCancelled(err) {
			return "", nil
		}
		return "", fmt.Errorf("open skill file dialog: %w", err)
	}
	return result, nil
}

// OpenSkillDirDialog shows the OS native picker for a skill artifact DIRECTORY.
// Returns the selected path (empty if cancelled).
func (s *AgentService) OpenSkillDirDialog() (string, error) {
	dialog := application.Get().Dialog.OpenFile().
		SetTitle("选择技能目录").
		CanChooseFiles(false).
		CanChooseDirectories(true).
		CanCreateDirectories(false)

	result, err := dialog.PromptForSingleSelection()
	if err != nil {
		if isDialogCancelled(err) {
			return "", nil
		}
		return "", fmt.Errorf("open skill dir dialog: %w", err)
	}
	return result, nil
}
