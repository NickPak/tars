package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"

	"tars/internal/boot"
	"tars/pkg/sandbox"
)

// FileService —— 工作区文件树浏览与系统级打开/定位（工作区页面）。
type FileService struct{}

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

// workspaceDirOf 解析会话所属项目的生效工作区（项目属性，多会话共享）。
// 层级表达：sessionID → FindProject 定位所属 Project → GetWorkspaceDir。
func (s *FileService) workspaceDirOf(sessionID string) (string, error) {
	proj, ok := boot.GetApp().FindProject(sessionID)
	if !ok {
		return "", fmt.Errorf("session not found: %s", sessionID)
	}
	return proj.GetWorkspaceDir(), nil
}

// ListWorkspaceFiles returns a recursive file tree of the given session's
// workspace directory（工作区是项目级：同项目多会话共享同一目录）。
// If the directory doesn't exist yet (new project), an empty slice is returned.
func (s *FileService) ListWorkspaceFiles(sessionID string) ([]FileEntry, error) {
	wsDir, err := s.workspaceDirOf(sessionID)
	if err != nil {
		return nil, err
	}

	if _, err := os.Stat(wsDir); os.IsNotExist(err) {
		return []FileEntry{}, nil
	}

	entries, err := scanDir(wsDir, "", 0)
	if err != nil {
		return nil, fmt.Errorf("scan workspace: %w", err)
	}
	return entries, nil
}

// ---- 用户侧文件管理（文件树查看器/编辑器 + 右键 CRUD）----
// 全部经 sandbox.NativeFs 的 confine 校验：相对路径解析到工作区根内，
// 逃逸（../ 或绝对路径越界）一律拒绝。

// maxEditorFileSize 编辑器读取上限（2MB）——超大文件交给外部程序打开。
const maxEditorFileSize = 2 << 20

// workspaceFs 解析会话工作区并构造受限文件系统（每次新建：无状态对象，
// 根在构造时锁定，避免跨调用根变更窗口）。
func (s *FileService) workspaceFs(sessionID string) (*sandbox.NativeFs, error) {
	wsDir, err := s.workspaceDirOf(sessionID)
	if err != nil {
		return nil, err
	}
	return sandbox.NewNativeFs(wsDir), nil
}

// ReadWorkspaceFile 读取工作区文件内容（编辑器加载用）。超过 2MB 或
// 内容含 NUL（二进制）时报错，引导用户用外部程序打开。
func (s *FileService) ReadWorkspaceFile(sessionID string, relPath string) (string, error) {
	fs, err := s.workspaceFs(sessionID)
	if err != nil {
		return "", err
	}
	info, err := fs.Stat(relPath)
	if err != nil {
		return "", fmt.Errorf("file not found: %s", relPath)
	}
	if info.IsDir {
		return "", fmt.Errorf("path is a directory: %s", relPath)
	}
	if info.Size > maxEditorFileSize {
		return "", fmt.Errorf("文件过大（>2MB），请用外部程序打开")
	}
	data, err := fs.ReadFile(relPath)
	if err != nil {
		return "", err
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return "", fmt.Errorf("二进制文件不支持编辑，请用外部程序打开")
	}
	return string(data), nil
}

// WriteWorkspaceFile 保存工作区文件（编辑器保存；不存在则创建）。
func (s *FileService) WriteWorkspaceFile(sessionID string, relPath string, content string) error {
	fs, err := s.workspaceFs(sessionID)
	if err != nil {
		return err
	}
	return fs.WriteFile(relPath, []byte(content))
}

// CreateWorkspaceEntry 新建文件或目录（父目录自动创建）。
func (s *FileService) CreateWorkspaceEntry(sessionID string, relPath string, isDir bool) error {
	fs, err := s.workspaceFs(sessionID)
	if err != nil {
		return err
	}
	if isDir {
		return fs.MkdirAll(relPath)
	}
	// 文件：父目录先行，内容留空
	if err := fs.MkdirAll(filepath.Dir(relPath)); err != nil {
		return err
	}
	return fs.WriteFile(relPath, nil)
}

// RenameWorkspaceEntry 重命名/移动文件或目录。
func (s *FileService) RenameWorkspaceEntry(sessionID string, oldRel string, newRel string) error {
	fs, err := s.workspaceFs(sessionID)
	if err != nil {
		return err
	}
	return fs.Rename(oldRel, newRel)
}

// DeleteWorkspaceEntry 删除文件或目录（目录递归删除；前端负责确认）。
func (s *FileService) DeleteWorkspaceEntry(sessionID string, relPath string) error {
	fs, err := s.workspaceFs(sessionID)
	if err != nil {
		return err
	}
	return fs.RemoveAll(relPath)
}

// InsertEditorReference 编辑器窗口框选引用（path:Lx-Ly）→ 广播给主窗口
// 插入对话输入框（发送时由 Controller 展开为真实代码片段）。
func (s *FileService) InsertEditorReference(sessionID string, ref string) error {
	if strings.TrimSpace(ref) == "" {
		return fmt.Errorf("引用为空")
	}
	if app := application.Get(); app != nil {
		app.Event.Emit("editor:insert-reference", ref)
	}
	return nil
}

// OpenFile opens a file with the OS default application (not hardcoded to any
// specific editor). The path should be relative to the session's workspace.
func (s *FileService) OpenFile(sessionID string, relPath string) error {
	wsDir, err := s.workspaceDirOf(sessionID)
	if err != nil {
		return err
	}

	fullPath := filepath.Join(wsDir, relPath)

	if _, err := os.Stat(fullPath); err != nil {
		return fmt.Errorf("path not found: %s", fullPath)
	}

	return openWithSystemDefault(fullPath)
}

// RevealInExplorer opens the OS file manager at the session's workspace
// directory. On Windows this is Explorer, on macOS Finder, on Linux the
// default file manager via xdg-open.
func (s *FileService) RevealInExplorer(sessionID string) error {
	wsDir, err := s.workspaceDirOf(sessionID)
	if err != nil {
		return err
	}

	if _, err := os.Stat(wsDir); err != nil {
		return fmt.Errorf("workspace not found: %s", wsDir)
	}

	return openFolderInExplorer(wsDir)
}

// RevealProjectWorkspace opens the OS file manager at the project's workspace
// directory（项目级入口：零会话项目没有 Controller，直接经项目管理器解析）。
func (s *FileService) RevealProjectWorkspace(projectID string) error {
	wsDir, err := boot.GetApp().GetProjectWorkspaceDir(projectID)
	if err != nil {
		return err
	}
	if _, err := os.Stat(wsDir); err != nil {
		return fmt.Errorf("workspace not found: %s", wsDir)
	}
	return openFolderInExplorer(wsDir)
}

// RevealFileInExplorer reveals a specific file in the OS file manager
// (selects the file in Explorer/Finder). The path should be relative to the
// session's workspace.
func (s *FileService) RevealFileInExplorer(sessionID string, relPath string) error {
	wsDir, err := s.workspaceDirOf(sessionID)
	if err != nil {
		return err
	}

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
