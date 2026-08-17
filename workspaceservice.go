package main

import (
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// WorkspaceInfo describes the current workspace state of a session.
type WorkspaceInfo struct {
	// Path is the effective workspace directory path.
	Path string `json:"path"`
	// IsCustom indicates whether the workspace is a user-chosen external
	// directory (true) or the default isolated workspace (false).
	IsCustom bool `json:"isCustom"`
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
		return "", fmt.Errorf("open directory dialog: %w", err)
	}
	return result, nil
}

// SetWorkspaceDir sets a custom workspace directory for the given session.
// All subsequent agent file operations will operate within this directory.
// Pass an empty string to reset to the default isolated workspace.
func (s *AgentService) SetWorkspaceDir(sessionID string, dir string) error {
	if !s.rt.Sessions.Has(sessionID) {
		return fmt.Errorf("session not found: %s", sessionID)
	}

	// Persist to meta.json
	meta, err := s.rt.Store.LoadMeta(sessionID)
	if err != nil || meta == nil {
		return fmt.Errorf("load meta for %s: %w", sessionID, err)
	}
	meta.CustomWorkDir = dir
	if err := s.rt.Store.SaveMeta(sessionID, meta); err != nil {
		return fmt.Errorf("save meta: %w", err)
	}

	// Emit event so frontend updates（工作区路径按需从 meta 读取，内存无需同步）
	application.Get().Event.Emit("workspace:changed", WorkspaceChangedEvent{
		SessionID: sessionID,
		Path:      dir,
		IsCustom:  dir != "",
	})

	slog.Info("Workspace changed", "session", sessionID, "dir", dir, "custom", dir != "")
	return nil
}

// GetWorkspaceInfo returns the current workspace info for a session.
func (s *AgentService) GetWorkspaceInfo(sessionID string) (*WorkspaceInfo, error) {
	if !s.rt.Sessions.Has(sessionID) {
		return nil, fmt.Errorf("session not found: %s", sessionID)
	}

	meta, err := s.rt.Store.LoadMeta(sessionID)
	if err != nil || meta == nil {
		return nil, fmt.Errorf("load meta: %w", err)
	}

	path := s.rt.Store.WorkspaceDir(sessionID)
	isCustom := false
	if meta.CustomWorkDir != "" {
		path = meta.CustomWorkDir
		isCustom = true
	}

	return &WorkspaceInfo{
		Path:     path,
		IsCustom: isCustom,
		Name:     filepath.Base(path),
	}, nil
}

// WorkspaceChangedEvent is the payload of the "workspace:changed" event.
type WorkspaceChangedEvent struct {
	SessionID string `json:"sessionId"`
	Path      string `json:"path"`
	IsCustom  bool   `json:"isCustom"`
}
