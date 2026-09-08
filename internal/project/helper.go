package project

import "path/filepath"

const (
	// BaseDirName 是项目存储的顶层目录名（<workDir>/projects/）。
	BaseDirName = "projects"
	// MetaFile 是项目元数据文件名。
	MetaFile = "project.json"
	// WorkspaceDirName 是项目默认工作区的目录名。
	WorkspaceDirName = "workspace"
	// SessionsDirName 是项目下会话存储的子目录名。
	SessionsDirName = "sessions"
)

// --- 路径助手（全部路径计算的唯一家） ---

func GetBaseDir(workDir string) string {
	return filepath.Join(workDir, BaseDirName)
}

func GetProjectDir(workDir, projectID string) string {
	return filepath.Join(GetBaseDir(workDir), projectID)
}

// GetWorkspaceDir 返回项目级默认工作区路径。
func GetWorkspaceDir(workDir, projectID string) string {
	return filepath.Join(GetProjectDir(workDir, projectID), WorkspaceDirName)
}

// GetSessionsDir 返回项目下的会话存储目录。
func GetSessionsDir(workDir, projectID string) string {
	return filepath.Join(GetProjectDir(workDir, projectID), SessionsDirName)
}

func GetProjectMetaFile(workDir, projectID string) string {
	return filepath.Join(GetProjectDir(workDir, projectID), MetaFile)
}
