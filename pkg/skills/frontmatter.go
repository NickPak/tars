package skills

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

type frontmatter struct {
	Name        string            `yaml:"name"`
	Description string            `yaml:"description"`
	License     string            `yaml:"license"`
	Metadata    map[string]string `yaml:"metadata"`
}

func ParseFrontmatter(raw []byte) (name, description string, err error) {
	fm, _, err := parseSKILL(raw)
	if err != nil {
		return "", "", err
	}
	if err := ValidateName(fm.Name); err != nil {
		return "", "", err
	}
	if strings.TrimSpace(fm.Description) == "" {
		return "", "", fmt.Errorf("skill %q: description is required in frontmatter", fm.Name)
	}
	return fm.Name, strings.TrimSpace(fm.Description), nil
}

func parseSKILL(raw []byte) (frontmatter, string, error) {
	var fm frontmatter
	text := strings.TrimPrefix(string(raw), "\uFEFF") // 去 BOM

	if !strings.HasPrefix(text, "---") {
		return fm, "", fmt.Errorf("SKILL.md missing YAML frontmatter (must start with ---)")
	}
	// 找到结束分隔符：第二个独立的 --- 行
	lines := strings.Split(text, "\n")
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end == -1 {
		return fm, "", fmt.Errorf("SKILL.md frontmatter not closed (missing ---)")
	}

	block := strings.Join(lines[1:end], "\n")
	if err := yaml.Unmarshal([]byte(block), &fm); err != nil {
		return fm, "", fmt.Errorf("SKILL.md frontmatter parse: %w", err)
	}
	body := strings.Join(lines[end+1:], "\n")
	return fm, body, nil
}
