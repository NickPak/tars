package main

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// WorkspaceInfo describes the current workspace state of a session.
type WorkspaceInfo struct {
	// Path is the effective workspace directory path.
	Path string `json:"path"`
	// Name is the base name of the workspace directory, for display.
	Name string `json:"name"`
}

// OpenDirectoryDialog shows the OS native directory picker and returns the
// selected path (empty string if the user cancels). This does NOT change the
// session's workspace — call SetWorkspaceDir to apply the selection.
func (s *AgentService) OpenDirectoryDialog() (string, error) {
	dialog := application.Get().Dialog.OpenFile().
		SetTitle("选择工作区目录").
		CanChooseDirectories(true).
		CanChooseFiles(false).
		CanCreateDirectories(true)

	result, err := dialog.PromptForSingleSelection()
	if err != nil {
		if isDialogCancelled(err) {
			return "", nil
		}
		return "", fmt.Errorf("open directory dialog: %w", err)
	}
	return result, nil
}

// isDialogCancelled 报告原生文件对话框的"用户取消"。Wails 的取消哨兵定义在
// internal 包（cfd.ErrorCancelled），无法 import 做 errors.Is，按消息匹配
// （Contains 兼容个别平台对取消错误的包装）。取消是正常用户操作：各对话框
// 方法按约定返回空串与 nil 错误。
func isDialogCancelled(err error) bool {
	return err != nil && strings.Contains(err.Error(), "cancelled by user")
}

// SetWorkspaceDir sets a custom workspace directory for the given session.
// 仅零消息窗口内生效：一旦产生对话消息即锁定（session.Manager 权威守卫），
// 不存在"重置为默认"入口——该功能从未工作过（空串过不了 os.Stat 校验），
// 已于 2026-09-01 删除。
//
// 两道守卫：轮运行中禁止（瞬态防并发）；已有对话消息禁止（静态防"锁定后
// 仍能改"——历史消息里的相对路径会静默失效，模型无从察觉）。
func (s *AgentService) SetWorkspaceDir(sessionID string, dir string) error {
	ctrl, ok := s.app.FindController(sessionID)
	if !ok {
		return fmt.Errorf("session not found: %s", sessionID)
	}
	if ctrl.IsRunning() {
		return fmt.Errorf("turn in progress, cancel it first")
	}

	// 会话层守卫 + 持久化 + sandbox 根同步，一处完成
	if err := ctrl.SetWorkspaceDir(dir); err != nil {
		return fmt.Errorf("set workspace dir: %w", err)
	}

	slog.Info("Workspace changed", "session", sessionID, "dir", dir, "custom", dir != "")
	return nil
}

// GetWorkspaceInfo returns the current workspace info for a session.
func (s *AgentService) GetWorkspaceInfo(sessionID string) (*WorkspaceInfo, error) {
	ctrl, ok := s.app.FindController(sessionID)
	if !ok {
		return nil, fmt.Errorf("session not found: %s", sessionID)
	}

	workspaceDir := ctrl.GetSessionMgr().GetWorkspaceDir()

	return &WorkspaceInfo{
		Path: workspaceDir,
		Name: filepath.Base(workspaceDir),
	}, nil
}
