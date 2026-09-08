// Package memory 是跨会话记忆能力包（plan/agent-memory-design-plan.md）。
// 三层架构：第 1 层指令记忆（AGENTS.md，人写）→ 第 2 层会话记忆（压缩
// 归档，已有）→ 第 3 层事实记忆（remember 工具 + 索引注入）。
// 分层模式与 skill/mcp 同构：Manager（进程级磁盘权威 + 工厂）→
// Runtime（会话级视图：记忆块渲染 + remember/recall 落盘端）。
package memory

import (
	"gopkg.in/yaml.v3"
)

const (
	// AgentsFile 是项目指令记忆的约定文件名（行业公约，存在于工作区
	// 根即生效——格式上不发明新东西，第三方仓库可携带）。
	AgentsFile = "AGENTS.md"

	// DefaultMaxBytes 是项目指令记忆的注入上限；超出截断并附
	// read_file 指针（防超大文件撑爆视图）。
	DefaultMaxBytes = 8 * 1024

	// DefaultMaxIndexBytes 是事实记忆索引（MEMORY.md）的注入上限，
	// 超出截断并附 recall 指针（共识 6：超体量退化为索引截断 + 按需检索）。
	DefaultMaxIndexBytes = 8 * 1024
)

// Scope 事实记忆的归属范围。
type Scope string

const (
	// ScopeProject 项目级（<projectDir>/memory/）：项目相关事实的默认归属。
	ScopeProject Scope = "project"
	// ScopeGlobal 全局用户级（<workDir>/memory/）：跨项目的用户偏好。
	ScopeGlobal Scope = "global"
)

// RememberInput 是 remember 工具的写入输入（经 Provider.Remember）。
type RememberInput struct {
	Type      FactType
	Subject   string
	Body      string
	Scope     Scope
	Overwrite bool
}

// CandidateItem 是压缩联动产出的候选原料（session 压缩管线 →
// Runtime.Suggest）。只用 UserAsks 一路：Facts（路径/URL/ID 等逐字值）
// 属于"仓库可推导"内容，与 remember 反面清单冲突，不进候选区。
type CandidateItem struct {
	Type    FactType
	Text    string
	Pointer string // archive://... 溯源指针
}

// Provider 是 remember / recall 工具所需的宿主注入能力（实现见
// runtime.go；工具包只依赖本接口）。
type Provider interface {
	// Remember 写入一条事实；created 表示本次是新建。白名单语义
	// （subject 冲突必须显式 overwrite、敏感模式拒绝）在存储层执行。
	Remember(in *RememberInput) (created bool, err error)
	// Recall 按关键词检索事实全文（项目级 + 全局，过期项排除）。
	Recall(query string, limit int) ([]*Fact, error)
}

// Config 是记忆功能配置段（AppConfig.memory；8.5）。
type Config struct {
	// Enabled 为 false 时记忆块不注入、remember/recall 报错。
	// 缺省 true（见 UnmarshalYAML：旧配置无此键不能静默关闭功能）。
	Enabled       bool `yaml:"enabled" json:"enabled"`
	MaxIndexBytes int  `yaml:"maxIndexBytes,omitempty" json:"maxIndexBytes,omitempty"`
}

func NewConfig() *Config {
	return &Config{Enabled: true, MaxIndexBytes: DefaultMaxIndexBytes}
}

// UnmarshalYAML 反序列化时缺省 enabled=true。
func (c *Config) UnmarshalYAML(value *yaml.Node) error {
	type plain Config
	c.Enabled = true
	return value.Decode((*plain)(c))
}

// Validate 修正非法字段为默认值。
func (c *Config) Validate() {
	if c.MaxIndexBytes <= 0 {
		c.MaxIndexBytes = DefaultMaxIndexBytes
	}
}
