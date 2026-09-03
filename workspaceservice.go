package main

import (
	"fmt"
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

// SetWorkspaceDir sets a custom workspace directory for the given session's
// PROJECT（工作区是项目属性：同项目全部会话共享）。守卫在 App 层：
// 项目内所有会话零消息且无运行中的轮才允许；锁定后不迁移。
// 不存在"重置为默认"入口。
func (s *AgentService) SetWorkspaceDir(sessionID string, dir string) error {
	if err := s.app.SetSessionWorkspace(sessionID, dir); err != nil {
		return fmt.Errorf("set workspace dir: %w", err)
	}
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
