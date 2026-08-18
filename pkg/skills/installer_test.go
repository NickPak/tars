package skills

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func makeSkillTree(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(filepath.Join(dir, "scripts"), 0755); err != nil {
		t.Fatal(err)
	}
	md := "---\nname: " + name + "\ndescription: test skill\n---\n# Body\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(md), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "scripts", "run.sh"), []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestInstallDir(t *testing.T) {
	s := mustInit(t)
	src := makeSkillTree(t, "demo-skill")

	name, err := s.Install(src, "devops", false)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if name != "demo-skill" {
		t.Fatalf("got name %q", name)
	}
	// SKILL.md 原样安装
	raw, err := os.ReadFile(filepath.Join(s.SkillDir("demo-skill"), "SKILL.md"))
	if err != nil || string(raw) != "---\nname: demo-skill\ndescription: test skill\n---\n# Body\n" {
		t.Errorf("SKILL.md not copied verbatim: %q %v", raw, err)
	}
	// registry 有记录，分类归一
	info, ok := s.GetRegistry().FindSkill("demo-skill")
	if !ok || info.Category != "devops" || info.Source != "local" {
		t.Errorf("registry wrong: %+v", info)
	}
	// 索引已生成
	if s.RenderIndex() == "" {
		t.Error("INDEX.md not generated")
	}
	// 同名冲突
	if _, err := s.Install(src, "devops", false); err == nil {
		t.Error("expected conflict error")
	}
}

func TestInstallSingleSKILLFile(t *testing.T) {
	s := mustInit(t)
	md := "---\nname: solo\ndescription: single file skill\n---\n# B\n"
	src := filepath.Join(t.TempDir(), "SKILL.md")
	if err := os.WriteFile(src, []byte(md), 0644); err != nil {
		t.Fatal(err)
	}
	name, err := s.Install(src, "misc", false)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if name != "solo" {
		t.Fatalf("got %q", name)
	}
	if _, err := os.Stat(filepath.Join(s.SkillDir("solo"), "SKILL.md")); err != nil {
		t.Error("wrapped dir missing SKILL.md")
	}
}

func TestInstallZip(t *testing.T) {
	s := mustInit(t)
	// 打包：SKILL.md 在包根
	zipPath := filepath.Join(t.TempDir(), "skill.zip")
	f, _ := os.Create(zipPath)
	zw := zip.NewWriter(f)
	w, _ := zw.Create("SKILL.md")
	w.Write([]byte("---\nname: zipped\ndescription: z\n---\n# B\n"))
	zw.Create("reference.md")
	zw.Close()
	f.Close()

	name, err := s.Install(zipPath, "docs", false)
	if err != nil {
		t.Fatalf("Install zip: %v", err)
	}
	if name != "zipped" {
		t.Fatalf("got %q", name)
	}
	if _, err := os.Stat(filepath.Join(s.SkillDir("zipped"), "reference.md")); err != nil {
		t.Error("reference.md not extracted")
	}
}

func TestInstallTarGz(t *testing.T) {
	s := mustInit(t)
	tgz := filepath.Join(t.TempDir(), "skill.tar.gz")
	f, _ := os.Create(tgz)
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	body := []byte("---\nname: tarskill\ndescription: t\n---\n# B\n")
	tw.WriteHeader(&tar.Header{Name: "tarskill/SKILL.md", Mode: 0644, Size: int64(len(body))})
	tw.Write(body)
	tw.Close()
	gz.Close()
	f.Close()

	name, err := s.Install(tgz, "misc", false)
	if err != nil {
		t.Fatalf("Install tar.gz: %v", err)
	}
	if name != "tarskill" {
		t.Fatalf("got %q", name)
	}
}

func TestUninstall(t *testing.T) {
	s := mustInit(t)
	src := makeSkillTree(t, "gone")
	if _, err := s.Install(src, "misc", false); err != nil {
		t.Fatal(err)
	}
	if err := s.Uninstall("gone"); err != nil {
		t.Fatal(err)
	}
	if dirExists(s.SkillDir("gone")) {
		t.Error("dir still exists after uninstall")
	}
	if _, ok := s.GetRegistry().FindSkill("gone"); ok {
		t.Error("registry entry not removed")
	}
}
