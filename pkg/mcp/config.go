// Package mcp 是 MCP（Model Context Protocol）能力供给渠道：
// 服务器配置、探测缓存（工具清单注册表）、懒启动连接池与工具检索。
// 工具被实例化（包装 tools.Definition、注册进会话 Registry）后即为普通
// 工具，审批/并发/执行复用 tools 包机制——tools 是 sink 不是 source，
// 本包单向依赖 pkg/tools，不被其反向依赖。
package mcp

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// Risk 级别：MCP 工具默认 RiskMedium（走审批流），可按服务器覆盖。
type Risk string

const (
	RiskLow    Risk = "low"    // 只读查询，不触发审批
	RiskMedium Risk = "medium" // 默认：执行前需用户审批
	RiskHigh   Risk = "high"   // 同 medium，GUI 中醒目标注
)

// SourceType 推荐词汇（信息源类型标注，用于系统消息索引 "name [type]: desc"）。
// 非强制枚举——自由文本，Validate 仅做空白修剪。
const (
	SourceSearch = "search" // 检索类（web 搜索、文档库）
	SourceRead   = "read"   // 读取类（文件、数据库读取）
	SourceParse  = "parse"  // 解析类（文档/格式解析）
	SourceQuery  = "query"  // 查询类（API 数据查询）
)

// ServerConfig 是一个 MCP 服务器（本地 stdio 进程）的配置。
// 能力默认关闭（Enabled 零值 false），须显式启用（失败安全默认值）。
type ServerConfig struct {
	// Command 是服务器可执行文件（或 npx/uvx 等启动器）。
	Command string `yaml:"command" json:"command"`
	// Args 是启动参数。
	Args []string `yaml:"args,omitempty" json:"args,omitempty"`
	// Env 是附加环境变量（最小权限凭证；支持 ${VAR} 引用，进程拉起时展开——
	// 磁盘与内存配置中引用原样保留，写回不泄露密钥）。
	Env map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
	// Description 是一句话能力描述（服务器级索引用，模型可见，用英文）。
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	// SourceType 是信息源类型标注（search/read/parse/query，自由文本）。
	SourceType string `yaml:"sourceType,omitempty" json:"sourceType,omitempty"`
	// Enabled 为 false 时服务器对 Agent 不可见且不启动进程（默认关闭）。
	Enabled bool `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	// Risk 是该服务器全部工具的默认风险级别（空值归一为 medium）。
	Risk Risk `yaml:"risk,omitempty" json:"risk,omitempty"`
}

// serversFile 是 MCP 服务器配置的文件名（磁盘格式即内存格式）。
const serversFile = "servers.yaml"

// Config 是 MCP 服务器配置的磁盘注册表（<workDir>/mcp/servers.yaml 的
// 内存映射）——服务器配置不属于用户应用配置（config.yaml），与技能一样
// 由 Manager 在工作目录下自管读写，即改即存。
// 与 skills.Registry 同契约：Load/Store 后视为不可变，读路径无锁，
// 变更一律整体替换（COW：Clone → 改 → Save → Store）。
// 注意：磁盘值不做环境变量展开，${VAR} 引用原样保留（写回不泄露密钥），
// 展开发生在进程拉起时（pool/client 的 spawn 路径）。
type Config struct {
	// Servers 以服务器名为键（名字会编入工具名 mcp__<server>__<tool>，
	// 须为小写字母/数字/连字符，见 ValidateName）。
	Servers map[string]*ServerConfig `yaml:"servers,omitempty" json:"servers,omitempty"`
	rootDir string
}

// NewConfig 创建一个 Config（rootDir 是 mcp 目录）。
func NewConfig(rootDir string) *Config {
	return &Config{Servers: make(map[string]*ServerConfig), rootDir: rootDir}
}

// Load 从磁盘加载服务器配置并校验。文件缺失视为首次运行（空配置，非错误）。
func (c *Config) Load() error {
	data, err := os.ReadFile(filepath.Join(c.rootDir, serversFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("mcp: load servers: %w", err)
	}
	if err := yaml.Unmarshal(data, c); err != nil {
		return fmt.Errorf("mcp: parse servers: %w", err)
	}
	if c.Servers == nil {
		c.Servers = make(map[string]*ServerConfig)
	}
	return c.Validate()
}

// Save 校验并原子写盘（tmp + rename）。
func (c *Config) Save() error {
	if err := c.Validate(); err != nil {
		return err
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("mcp: marshal servers: %w", err)
	}
	path := filepath.Join(c.rootDir, serversFile)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("mcp: write servers tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("mcp: rename servers: %w", err)
	}
	return nil
}

// Clone 返回深拷贝副本（新 map + 条目逐一拷贝，Args/Env 同步复制）——
// 副本可安全修改（含 Validate 的原位归一化），不与任何读路径共享。
func (c *Config) Clone() *Config {
	next := &Config{Servers: make(map[string]*ServerConfig, len(c.Servers)), rootDir: c.rootDir}
	for name, srv := range c.Servers {
		cp := *srv
		cp.Args = slices.Clone(srv.Args)
		cp.Env = maps.Clone(srv.Env)
		next.Servers[name] = &cp
	}
	return next
}

var namePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// ValidateName 校验服务器名：编入工具名的标识符，须为小写字母/数字/连字符。
func ValidateName(name string) error {
	if name == "" {
		return fmt.Errorf("mcp: server name is required")
	}
	if !namePattern.MatchString(name) {
		return fmt.Errorf("mcp: invalid server name %q: lowercase letters, digits and hyphens only", name)
	}
	return nil
}

// Validate 校验全部服务器配置：非法名字/空命令/未知风险级别报错；
// 风险级别空值归一为 medium，描述与类型做空白修剪。
func (c *Config) Validate() error {
	if c == nil {
		return nil
	}
	for name, srv := range c.Servers {
		if err := ValidateName(name); err != nil {
			return err
		}
		if srv == nil {
			return fmt.Errorf("mcp: server %q: config is empty", name)
		}
		srv.Command = strings.TrimSpace(srv.Command)
		if srv.Command == "" {
			return fmt.Errorf("mcp: server %q: command is required", name)
		}
		srv.Description = strings.TrimSpace(srv.Description)
		srv.SourceType = strings.ToLower(strings.TrimSpace(srv.SourceType))
		if srv.Risk == "" {
			srv.Risk = RiskMedium
		}
		switch srv.Risk {
		case RiskLow, RiskMedium, RiskHigh:
		default:
			return fmt.Errorf("mcp: server %q: unknown risk level %q", name, srv.Risk)
		}
	}
	return nil
}
