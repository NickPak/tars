package memory

import (
	"os"
	"path/filepath"
	"sync/atomic"
)

const (
	MemoryDir = "memory"
)

// Manager 是事实记忆的进程级磁盘权威与工厂（与 skill/mcp 三层模式同构）：
// 持有全局记忆根（<workDir>/memory/）与配置，按项目目录解析项目级
// 记忆根，为每个会话产出 Runtime。
type Manager struct {
	workDir string
	cfg     atomic.Pointer[Config]
}

// NewManager 创建记忆管理器（cfg 为 nil 时用默认配置）。
func NewManager(workDir string, cfg *Config) *Manager {
	if cfg == nil {
		cfg = NewConfig()
	}
	m := &Manager{workDir: workDir}
	m.cfg.Store(cfg)
	return m
}

// Startup 初始化全局记忆根并重建索引（索引是派生品，启动时对齐磁盘真相）。
func (m *Manager) Startup() error {
	memDir := m.GetGlobalMemoryDir()
	err := os.MkdirAll(memDir, 0755)
	if err != nil {
		return err
	}

	return GenerateIndex(memDir)
}

func (m *Manager) Shutdown() error { return nil }

// GetConfig 返回当前配置（原子读；返回值视为只读）。
func (m *Manager) GetConfig() *Config {
	return m.cfg.Load()
}

// UpdateConfig 原子替换配置（配置保存流调用；nil 忽略）。
func (m *Manager) UpdateConfig(v *Config) error {
	if v == nil {
		return nil
	}
	m.cfg.Store(v)
	return nil
}

// GetGlobalMemoryDir 返回全局用户记忆根目录。
func (m *Manager) GetGlobalMemoryDir() string {
	return filepath.Join(m.workDir, MemoryDir)
}

// GetProjectMemoryDir 返回项目级记忆根目录（projectDir 即 projects/<pid>）。
func (m *Manager) GetProjectMemoryDir(projectDir string) string {
	return filepath.Join(projectDir, MemoryDir)
}

// GlobalAgentsFile 返回用户级指令记忆路径（<workDir>/AGENTS.md，即
// ~/.tars/AGENTS.md——P4 全局指令记忆，与项目级 AGENTS.md 合并注入，
// 冲突时项目级优先）。
func (m *Manager) GlobalAgentsFile() string {
	return filepath.Join(m.workDir, AgentsFile)
}

// NewRuntime 工厂方法：为一次会话产出记忆运行时。ws 是项目指令记忆
// （AGENTS.md）的工作区来源；projectDir 解析项目级记忆根；modelID
// 在 remember 写入时标注 written_by（防模型特化记忆污染溯源）。
func (m *Manager) NewRuntime(ws StateProvider, projectDir string, modelID func() string) *Runtime {
	return &Runtime{
		ws:         ws,
		mgr:        m,
		projectDir: projectDir,
		modelID:    modelID,
	}
}
