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
	"strings"
	"time"
)

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
