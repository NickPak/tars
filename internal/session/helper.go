package session

import "path/filepath"

const (
	// SessionsDirName 是项目目录下的会话子目录名（项目 = 工作区拥有者，
	// 会话嵌套其下：projects/<pid>/sessions/<sid>/）。
	SessionsDirName = "sessions"
	DataDir         = ".data"
	MetaFile        = "meta.json"
	MessageFile     = "messages.jsonl"
	CompactionFile  = "compaction.json"
	ArchiveDir      = "archive"
)

// 以下路径助手一律以项目目录为锚（项目级路径在 internal/project 定义）。

func GetSessionsDir(projectDir string) string {
	return filepath.Join(projectDir, SessionsDirName)
}

func GetSessionDir(projectDir, sessionID string) string {
	return filepath.Join(GetSessionsDir(projectDir), sessionID)
}

func GetDataDirFromSessionDir(sessionDir string) string {
	return filepath.Join(sessionDir, DataDir)
}
