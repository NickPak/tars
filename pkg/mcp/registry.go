package mcp

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const (
	// registryFile 是 MCP 工具清单缓存的文件名（磁盘格式即内存格式）。
	registryFile = "registry.yaml"
	// cacheVersion 缓存格式版本：SDK 协议或缓存结构变更时递增，
	// 版本不符的缓存视为缺失（触发重新探测）。
	cacheVersion = 1
)

// ToolInfo 是一个 MCP 工具的缓存视图（探测时抓取；模型可见文本均为英文）。
type ToolInfo struct {
	Name        string         `json:"name" yaml:"name"`
	Description string         `json:"description,omitempty" yaml:"description,omitempty"`
	// InputSchema 是工具的 JSON Schema（原样缓存；#6 检索、#7 包装 Definition
	// 时直接作为 schema.ToolSchema.Parameters 使用）。
	InputSchema map[string]any `json:"inputSchema,omitempty" yaml:"input_schema,omitempty"`
}

// ServerTools 是一个服务器的探测结果（工具清单 + 抓取时间）。
type ServerTools struct {
	ProbedAt string      `json:"probedAt" yaml:"probed_at"`
	Tools    []*ToolInfo `json:"tools" yaml:"tools"`
}

// Registry 是 MCP 工具清单缓存：磁盘注册表（<workDir>/mcp/registry.yaml）
// 的内存映射，探测（Probe）时整体刷新。
// 与 skills.Registry 同契约：Load/Store 后视为不可变，读路径无锁，
// 变更一律整体替换（COW）。
type Registry struct {
	Version int                    `json:"version" yaml:"version"`
	Servers map[string]*ServerTools `json:"servers" yaml:"servers"`
	rootDir string
}

// NewRegistry 创建一个 Registry（rootDir 是 mcp 目录）。
func NewRegistry(rootDir string) *Registry {
	return &Registry{Servers: make(map[string]*ServerTools), rootDir: rootDir}
}

// Load 从磁盘加载注册表。文件缺失或版本不符视为首次运行（空注册表，非错误）。
func (r *Registry) Load() error {
	data, err := os.ReadFile(filepath.Join(r.rootDir, registryFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("mcp: load registry: %w", err)
	}
	if err := yaml.Unmarshal(data, r); err != nil {
		return fmt.Errorf("mcp: parse registry: %w", err)
	}
	if r.Servers == nil {
		r.Servers = make(map[string]*ServerTools)
	}
	if r.Version != cacheVersion { // 格式漂移：视为缺失，等重新探测
		r.Servers = make(map[string]*ServerTools)
	}
	return nil
}

// Save 原子写盘（tmp + rename）。
func (r *Registry) Save() error {
	r.Version = cacheVersion
	data, err := yaml.Marshal(r)
	if err != nil {
		return fmt.Errorf("mcp: marshal registry: %w", err)
	}
	path := filepath.Join(r.rootDir, registryFile)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("mcp: write registry tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("mcp: rename registry: %w", err)
	}
	return nil
}

// FindServer 返回一个服务器的缓存工具清单；未探测时返回 nil, false。
func (r *Registry) FindServer(name string) (*ServerTools, bool) {
	st, ok := r.Servers[name]
	return st, ok
}

// Clone 返回浅拷贝副本（新 map，共享条目指针——配合"条目先拷贝再改"约定）。
func (r *Registry) Clone() *Registry {
	next := &Registry{Version: r.Version, Servers: make(map[string]*ServerTools, len(r.Servers)), rootDir: r.rootDir}
	for name, st := range r.Servers {
		next.Servers[name] = st
	}
	return next
}
