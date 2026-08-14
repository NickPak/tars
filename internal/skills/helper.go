package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
