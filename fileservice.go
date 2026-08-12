package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"tars/internal/session"
	"tars/pkg/store"
)

// FileEntry represents a single file or directory in the workspace file tree.
type FileEntry struct {
	Name     string      `json:"name"`
	Path     string      `json:"path"` // path relative to workspace root
	IsDir    bool        `json:"isDir"`
	Size     int64       `json:"size"` // file size in bytes (0 for dirs)
	Children []FileEntry `json:"children,omitempty"`
}

// maxTreeDepth limits recursive directory scanning to avoid performance issues
// with very deep or large directory trees.
const maxTreeDepth = 5

// ListWorkspaceFiles returns a recursive file tree of the given session's
// workspace directory. The workspace dir is per-session: {workDir}/sessions/{id}/workspace/.
// If the directory doesn't exist yet (new session), an empty slice is returned.
func (s *AgentService) ListWorkspaceFiles(sessionID string) ([]FileEntry, error) {
	if !session.GetManager().Has(sessionID) {
		return nil, fmt.Errorf("session not found: %s", sessionID)
	}

	wsDir := store.GetSessionStore().ResolveWorkDir(sessionID)

	if _, err := os.Stat(wsDir); os.IsNotExist(err) {
		return []FileEntry{}, nil
	}

	entries, err := scanDir(wsDir, "", 0)
	if err != nil {
		return nil, fmt.Errorf("scan workspace: %w", err)
	}
	return entries, nil
}

// OpenFile opens a file with the OS default application (not hardcoded to any
// specific editor). The path should be relative to the session's workspace.
func (s *AgentService) OpenFile(sessionID string, relPath string) error {
	if !session.GetManager().Has(sessionID) {
		return fmt.Errorf("session not found: %s", sessionID)
	}

	wsDir := store.GetSessionStore().ResolveWorkDir(sessionID)
	fullPath := filepath.Join(wsDir, relPath)

	if _, err := os.Stat(fullPath); err != nil {
		return fmt.Errorf("path not found: %s", fullPath)
	}

	return openWithSystemDefault(fullPath)
}

// RevealInExplorer opens the OS file manager at the session's workspace
// directory. On Windows this is Explorer, on macOS Finder, on Linux the
// default file manager via xdg-open.
func (s *AgentService) RevealInExplorer(sessionID string) error {
	if !session.GetManager().Has(sessionID) {
		return fmt.Errorf("session not found: %s", sessionID)
	}

	wsDir := store.GetSessionStore().ResolveWorkDir(sessionID)
	if _, err := os.Stat(wsDir); err != nil {
		return fmt.Errorf("workspace not found: %s", wsDir)
	}

	return openFolderInExplorer(wsDir)
}

// RevealFileInExplorer reveals a specific file in the OS file manager
// (selects the file in Explorer/Finder). The path should be relative to the
// session's workspace.
func (s *AgentService) RevealFileInExplorer(sessionID string, relPath string) error {
	if !session.GetManager().Has(sessionID) {
		return fmt.Errorf("session not found: %s", sessionID)
	}

	wsDir := store.GetSessionStore().ResolveWorkDir(sessionID)
	fullPath := filepath.Join(wsDir, relPath)

	if _, err := os.Stat(fullPath); err != nil {
		return fmt.Errorf("path not found: %s", fullPath)
	}

	return revealFileInExplorer(fullPath)
}

// scanDir recursively scans a directory and returns sorted file entries.
// relPath is the path relative to the workspace root (empty for root).
func scanDir(absDir, relPath string, depth int) ([]FileEntry, error) {
	if depth > maxTreeDepth {
		return nil, nil
	}

	dirEntries, err := os.ReadDir(absDir)
	if err != nil {
		return nil, err
	}

	// Collect and sort: directories first, then files, alphabetically
	var dirs, files []os.DirEntry
	for _, e := range dirEntries {
		// Skip hidden files/directories (dotfiles)
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if e.IsDir() {
			dirs = append(dirs, e)
		} else {
			files = append(files, e)
		}
	}

	sort.Slice(dirs, func(i, j int) bool { return dirs[i].Name() < dirs[j].Name() })
	sort.Slice(files, func(i, j int) bool { return files[i].Name() < files[j].Name() })

	entries := make([]FileEntry, 0, len(dirs)+len(files))

	for _, d := range dirs {
		childRelPath := filepath.Join(relPath, d.Name())
		childAbs := filepath.Join(absDir, d.Name())
		entry := FileEntry{
			Name:  d.Name(),
			Path:  childRelPath,
			IsDir: true,
		}
		children, err := scanDir(childAbs, childRelPath, depth+1)
		if err == nil {
			entry.Children = children
		}
		entries = append(entries, entry)
	}

	for _, f := range files {
		info, err := f.Info()
		size := int64(0)
		if err == nil {
			size = info.Size()
		}
		entries = append(entries, FileEntry{
			Name:  f.Name(),
			Path:  filepath.Join(relPath, f.Name()),
			IsDir: false,
			Size:  size,
		})
	}

	return entries, nil
}

// openWithSystemDefault opens a file or folder with the OS default application.
// On Windows we go through explorer.exe instead of `cmd /c start`: explorer
// uses shell association semantics — files with a registered handler open
// directly, and files WITHOUT an association (e.g. .go on a machine where the
// IDE never registered it) reliably trigger the "Open with" dialog, whereas
// `start` fails silently in a detached process environment.
func openWithSystemDefault(path string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("explorer", path)
	case "darwin":
		cmd = exec.Command("open", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	return startDetached(cmd)
}

// openFolderInExplorer opens a folder in the OS file manager.
func openFolderInExplorer(dir string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("explorer", dir)
	case "darwin":
		cmd = exec.Command("open", dir)
	default:
		cmd = exec.Command("xdg-open", dir)
	}
	return startDetached(cmd)
}

// startDetached starts the command and reaps it in the background so the
// launcher process does not become a zombie after handing off to the target
// application. Errors from Wait (e.g. the target app returning a non-zero
// exit code after successfully opening the file) are intentionally ignored.
func startDetached(cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() {
		_ = cmd.Wait()
	}()
	return nil
}

// revealFileInExplorer reveals a file in the OS file manager, selecting it.
func revealFileInExplorer(path string) error {
	switch runtime.GOOS {
	case "windows":
		// explorer /select,"C:\path\to\file.txt"
		return exec.Command("explorer", "/select,", path).Start()
	case "darwin":
		// open -R reveals the file in Finder
		return exec.Command("open", "-R", path).Start()
	default:
		// Linux: no standard "reveal" command, open parent directory
		return exec.Command("xdg-open", filepath.Dir(path)).Start()
	}
}
