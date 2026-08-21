package mcp

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const rootDirName = "mcp"

// ServerInfo 是一个 MCP 服务器的展示视图（配置快照，指针不共享）。
type ServerInfo struct {
	Name        string `json:"name"`
	Command     string `json:"command"`
	Description string `json:"description"`
	SourceType  string `json:"sourceType"`
	Enabled     bool   `json:"enabled"`
	Risk        Risk   `json:"risk"`
	ToolCount   int    `json:"toolCount"` // 探测缓存中的工具数（#3 填充；0 表示未探测）
}

// Manager 是 MCP 能力的生命周期门面：持有服务器配置（servers.yaml，
// 原子可换）、工具清单探测缓存（registry.yaml）与懒启动连接池。
// 与 skills.Manager 同契约：配置自管磁盘读写、即改即存，读路径无锁，
// 配置与注册表整体替换（COW），连接池由独立互斥锁保护（运行态，非快照）。
type Manager struct {
	cfg      atomic.Pointer[Config]
	registry atomic.Pointer[Registry] // 工具清单探测缓存（发布后不可变）
	rootDir  string                   // <workDir>/mcp（servers.yaml / registry.yaml 的父目录）

	// mu 保护 clients（懒启动连接池，见 pool.go）。
	mu      sync.Mutex
	clients map[string]*clientHandle // name → 连接槽
}

// NewManager 创建 MCP 管理器并加载磁盘配置（不拉起任何服务器进程——
// 进程延迟启动，首次命中才连接）。servers.yaml 缺失按空配置工作
// （MCP 是可选能力）。
func NewManager(workDir string) *Manager {
	rootDir := filepath.Join(workDir, rootDirName)

	m := &Manager{
		cfg:      atomic.Pointer[Config]{},
		registry: atomic.Pointer[Registry]{},
		rootDir:  rootDir,
		mu:       sync.Mutex{},
		clients:  make(map[string]*clientHandle),
	}

	cfg := NewConfig(rootDir)
	m.cfg.Store(cfg)

	reg := NewRegistry(rootDir)
	m.registry.Store(reg)

	return m
}

func (m *Manager) Startup() error {
	err := os.MkdirAll(m.rootDir, 0o755)
	if err != nil {
		return fmt.Errorf("mcp: create root dir: %w", err)
	}

	err = m.cfg.Load().Load()
	if err != nil {
		return err
	}

	err = m.registry.Load().Load()
	if err != nil {
		return err
	}

	return nil
}

func (m *Manager) Shutdown() error {
	m.CloseAll() // 再统一关闭 MCP 服务器进程
	return nil
}

// GetConfig 返回当前配置（原子读；返回值视为只读）。
func (m *Manager) GetConfig() *Config {
	return m.cfg.Load()
}

// UpsertServer 登记/覆盖一个服务器（COW：先写盘成功再替换内存）。
// 覆盖既有服务器时其运行中连接随即回收（下次调用懒重启，新配置生效）。
func (m *Manager) UpsertServer(name string, srv *ServerConfig) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	if srv == nil {
		return fmt.Errorf("mcp: server %q: config is empty", name)
	}

	old := m.GetConfig()
	_, existed := old.Servers[name]

	next := old.Clone()
	cp := *srv // 防御性拷贝（含 Args/Env 深拷贝）：调用方事后改其持有的指针不污染已发布配置
	cp.Args = slices.Clone(srv.Args)
	cp.Env = maps.Clone(srv.Env)
	next.Servers[name] = &cp
	if err := next.Save(); err != nil {
		return err
	}
	m.cfg.Store(next)

	if existed {
		m.Close(name)
	}
	return nil
}

// RemoveServer 移除一个服务器（COW）：连接回收 + 探测缓存条目清理
// （配置与缓存同步，与 skills.Uninstall 同款语义）。幂等：不存在时无事发生。
func (m *Manager) RemoveServer(name string) error {
	old := m.GetConfig()
	if _, ok := old.Servers[name]; !ok {
		return nil
	}

	next := old.Clone()
	delete(next.Servers, name)
	if err := next.Save(); err != nil {
		return err
	}
	m.cfg.Store(next)

	m.Close(name)
	m.pruneServerTools(name)
	return nil
}

// pruneServerTools 清理被删服务器的探测缓存条目（registry.yaml，COW 整体
// 替换）。检索/索引只遍历配置内服务器，缓存残留本无功能影响，清理只为磁盘整洁。
func (m *Manager) pruneServerTools(name string) {
	reg := m.GetRegistry()
	if reg == nil {
		return
	}
	if _, ok := reg.FindServer(name); !ok {
		return
	}
	next := reg.Clone()
	delete(next.Servers, name)
	if err := next.Save(); err == nil {
		m.registry.Store(next)
	}
}

// SetEnabled 启用/禁用服务器（COW：先写盘成功再替换内存）。
// 禁用后服务器对 Agent 不可见（索引/检索/连接排除）且连接即回收；
// 配置与探测缓存保留。服务器不存在时报错。
func (m *Manager) SetEnabled(name string, enabled bool) error {
	old := m.GetConfig()
	cur, ok := old.Servers[name]
	if !ok {
		return fmt.Errorf("mcp: server %q not configured", name)
	}
	if cur.Enabled == enabled {
		return nil
	}

	next := old.Clone()
	next.Servers[name].Enabled = enabled // Clone 是深拷贝，直接改
	if err := next.Save(); err != nil {
		return err
	}
	m.cfg.Store(next)

	if !enabled {
		m.Close(name)
	}
	return nil
}

// List 返回全部已配置服务器（按 name 排序），含禁用项（GUI 展示用）。
func (m *Manager) List() []*ServerInfo {
	cfg := m.GetConfig()
	if cfg == nil {
		return nil
	}
	out := make([]*ServerInfo, 0, len(cfg.Servers))
	reg := m.GetRegistry()
	for name, srv := range cfg.Servers {
		info := &ServerInfo{
			Name:        name,
			Command:     srv.Command,
			Description: srv.Description,
			SourceType:  srv.SourceType,
			Enabled:     srv.Enabled,
			Risk:        srv.Risk,
		}
		if reg != nil {
			if st, ok := reg.FindServer(name); ok {
				info.ToolCount = len(st.Tools)
			}
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Enabled 返回启用中的服务器（系统消息索引、检索、动态注册的可见口径）。
func (m *Manager) Enabled() []*ServerInfo {
	all := m.List()
	out := make([]*ServerInfo, 0, len(all))
	for _, srv := range all {
		if srv.Enabled {
			out = append(out, srv)
		}
	}
	return out
}

// Server 返回指定服务器的配置视图；不存在时返回 nil。
func (m *Manager) Server(name string) *ServerInfo {
	cfg := m.GetConfig()
	if cfg == nil {
		return nil
	}
	srv, ok := cfg.Servers[name]
	if !ok {
		return nil
	}
	return &ServerInfo{
		Name:        name,
		Command:     srv.Command,
		Description: srv.Description,
		SourceType:  srv.SourceType,
		Enabled:     srv.Enabled,
		Risk:        srv.Risk,
	}
}

// RootDir 返回磁盘制品根目录（探测缓存等，#3 使用）。
func (m *Manager) RootDir() string { return m.rootDir }

// GetRegistry 返回当前工具清单缓存（原子读；返回值视为只读）。
func (m *Manager) GetRegistry() *Registry {
	return m.registry.Load()
}

// Probe 探测一个已配置服务器：拉起进程→抓取工具清单→进程退出→缓存落盘
// （整体替换，COW）。这是"配置时探测缓存"决策（D2）的实现：此后会话
// 启动零进程，discover_tools 用缓存检索，命中后首次调用才懒启动。
// 服务器必须已配置且启用；ctx 由调用方控制超时。
func (m *Manager) Probe(ctx context.Context, name string) error {
	cfg := m.GetConfig()
	if cfg == nil {
		return fmt.Errorf("mcp: no config")
	}
	srv, ok := cfg.Servers[name]
	if !ok {
		return fmt.Errorf("mcp: server %q not configured", name)
	}
	if !srv.Enabled {
		return fmt.Errorf("mcp: server %q is disabled", name)
	}

	var tools []*ToolInfo
	err := withServer(ctx, name, srv, func(cs *mcpsdk.ClientSession) error {
		var err error
		tools, err = listToolInfos(ctx, cs)
		return err
	})
	if err != nil {
		return err
	}

	reg := m.GetRegistry()
	next := NewRegistry(m.rootDir)
	if reg != nil {
		next = reg.Clone()
	}
	next.Servers[name] = &ServerTools{
		ProbedAt: time.Now().Format("2006-01-02 15:04:05"),
		Tools:    tools,
	}
	if err := next.Save(); err != nil {
		return err
	}
	m.registry.Store(next)
	return nil
}

// Tools 返回一个服务器的缓存工具清单；未探测返回空切片。
// 返回条目为拷贝：缓存发布后不可变，调用方拿到的不与任何读路径共享。
func (m *Manager) Tools(name string) []*ToolInfo {
	reg := m.GetRegistry()
	if reg == nil {
		return nil
	}
	st, ok := reg.FindServer(name)
	if !ok {
		return nil
	}
	out := make([]*ToolInfo, 0, len(st.Tools))
	for _, t := range st.Tools {
		cp := *t
		out = append(out, &cp)
	}
	return out
}
