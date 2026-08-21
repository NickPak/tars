package skills

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var (
	nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
)

func countFiles(dir string) int {
	n := 0
	filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		n++
		return nil
	})
	return n
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func ValidateName(name string) error {
	if !nameRe.MatchString(name) {
		return fmt.Errorf("invalid skill name %q: must match %s", name, nameRe.String())
	}
	return nil
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

// skillNamesLine 用类内全部技能名渲染一行概览：模型凭技能名即可判断类别
// 内容轮廓，description 留给 index/<category>.md 详情页（避免只展示第一个
// 技能的描述造成以偏概全）。
func skillNamesLine(items []*SkillMeta) string {
	names := make([]string, 0, len(items))
	for _, sk := range items {
		names = append(names, sk.Name)
	}
	return strings.Join(names, ", ")
}

func readFile(path string) []byte {
	b, _ := os.ReadFile(path)
	return b
}

func readArtifactInfo(dir string) (name, description string, err error) {
	md := filepath.Join(dir, skillFile)
	raw, err := os.ReadFile(md)
	if err != nil {
		return "", "", fmt.Errorf("skills: artifact must contain SKILL.md: %w", err)
	}
	return ParseFrontmatter(raw)
}

func extract(src, dst string) error {
	switch {
	case strings.HasSuffix(strings.ToLower(src), ".zip"):
		return extractZip(src, dst)
	case strings.HasSuffix(strings.ToLower(src), ".tar.gz"),
		strings.HasSuffix(strings.ToLower(src), ".tgz"):
		return extractTarGz(src, dst)
	default:
		return fmt.Errorf("skills: unsupported archive format (expect .zip / .tar.gz): %s", src)
	}
}

func extractZip(src, dst string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()
	for _, f := range r.File {
		if err := extractZipFile(f, dst); err != nil {
			return err
		}
	}
	return nil
}

func extractZipFile(f *zip.File, dst string) error {
	// 防路径穿越（zip slip）
	if strings.Contains(f.Name, "..") {
		return fmt.Errorf("skills: unsafe path in archive: %s", f.Name)
	}
	target := filepath.Join(dst, filepath.FromSlash(f.Name))
	if f.FileInfo().IsDir() {
		return os.MkdirAll(target, 0755)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return err
	}
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.Create(target)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, rc)
	return err
}

func extractTarGz(src, dst string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if strings.Contains(hdr.Name, "..") {
			return fmt.Errorf("skills: unsafe path in archive: %s", hdr.Name)
		}
		target := filepath.Join(dst, filepath.FromSlash(hdr.Name))
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			out, err := os.Create(target)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			out.Close()
		}
	}
	return nil
}

func locateSKILL(root string) (string, error) {
	var found []string
	filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if strings.EqualFold(d.Name(), "SKILL.md") {
			rel, _ := filepath.Rel(root, path)
			if depth := strings.Count(rel, string(filepath.Separator)); depth <= 2 {
				found = append(found, path)
			}
		}
		return nil
	})
	if len(found) == 0 {
		return "", fmt.Errorf("skills: no SKILL.md found in archive (depth ≤ 2)")
	}
	if len(found) > 1 {
		return "", fmt.Errorf("skills: multiple SKILL.md found in archive; please organize manually")
	}
	return filepath.Dir(found[0]), nil
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		return copyFile(path, target)
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
