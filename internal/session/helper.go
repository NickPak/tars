package session

import "path/filepath"

const (
	SessionDir     = "sessions"
	DataDir        = ".data"
	WorkspaceDir   = "workspaces"
	MetaFile       = "meta.json"
	MessageFile    = "messages.jsonl"
	CompactionFile = "compaction.json"
	ArchiveDir     = "archive"
)

func GetBaseDir(workDir string) string {
	return filepath.Join(workDir, SessionDir)
}

func GetSessionDir(workDir string, id string) string {
	return filepath.Join(GetBaseDir(workDir), id)
}

func GetDataDir(workDir string, id string) string {
	return filepath.Join(GetSessionDir(workDir, id), DataDir)
}

func GetWorkspaceDir(workDir string, id string) string {
	return filepath.Join(GetSessionDir(workDir, id), WorkspaceDir)
}
