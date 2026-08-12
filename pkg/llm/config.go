package llm

import (
	"fmt"
	"strings"
)

// 支持的供应商类型（各对应 eino-ext 原生组件，保留模型私有特性）。
const (
	ProviderGemini   = "gemini" // Google Gemini（原生 genai SDK）
	ProviderOpenAI   = "openai" // OpenAI 及所有兼容端点（Moonshot/OpenRouter/本地 vLLM 等）
	ProviderClaude   = "claude" // Anthropic Claude
	ProviderDeepSeek = "deepseek"
	ProviderQwen     = "qwen"    // 阿里百炼 DashScope
	ProviderArk      = "ark"     // 火山引擎方舟（豆包等）
	ProviderOllama   = "ollama"  // 本地 Ollama 服务（无需 API Key）
	ProviderQianfan  = "qianfan" // 百度千帆（AK/SK 鉴权）
)

// ProviderTypes 返回全部受支持的供应商类型（UI 枚举用）。
func ProviderTypes() []string {
	return []string{
		ProviderGemini, ProviderOpenAI, ProviderClaude, ProviderDeepSeek,
		ProviderQwen, ProviderArk, ProviderOllama, ProviderQianfan,
	}
}

// IsValidProviderType 校验供应商类型。
func IsValidProviderType(t string) bool {
	switch t {
	case ProviderGemini, ProviderOpenAI, ProviderClaude, ProviderDeepSeek,
		ProviderQwen, ProviderArk, ProviderOllama, ProviderQianfan:
		return true
	default:
		return false
	}
}

// ProviderConfig 是一个模型供应商的接入配置。
// ID 不进 YAML——文件中 map key 即 ID，Validate 时归一化回填。
type ProviderConfig struct {
	ID     string `yaml:"-" json:"id"`      // 唯一键（= providers map 的 key），如 "gemini"
	Type   string `yaml:"type" json:"type"` // 见 ProviderXxx 常量
	ApiKey string `yaml:"apiKey,omitempty" json:"apiKey,omitempty"`
	// BaseUrl 可覆盖官方端点（openai/qwen 类型必填；ollama 默认 http://localhost:11434）
	BaseUrl string `yaml:"baseUrl,omitempty" json:"baseUrl,omitempty"`

	// ---- 供应商私有字段 ----

	// AccessKey/SecretKey 千帆（qianfan）类型的 AK/SK 鉴权
	//（该 SDK 走全局单例配置，构建时注入）。
	AccessKey string `yaml:"accessKey,omitempty" json:"accessKey,omitempty"`
	SecretKey string `yaml:"secretKey,omitempty" json:"secretKey,omitempty"`
	// Region 火山引擎区域（ark 类型），默认 cn-beijing。
	Region string `yaml:"region,omitempty" json:"region,omitempty"`
	// CacheTTL Claude 自动前缀缓存（claude 类型）："5m"/"1h"，
	// 非空时在 system、工具定义与每轮最后一条 user 消息上自动打缓存断点。
	CacheTTL string `yaml:"cacheTTL,omitempty" json:"cacheTTL,omitempty"`
	// ReasoningPolicy 历史消息中 reasoning（思考链）的回放策略：
	// ""/"replay" 回放 / "strip" 剥离 / "keep" 原样透传。
	// 空则使用供应商类型的内置默认（见 defaultReasoningPolicies）——
	// 这是协议要求而非偏好：DeepSeek 工具回合禁止回传 reasoning，
	// Gemini function call 回合必须回放 thinking。
	ReasoningPolicy string `yaml:"reasoningPolicy,omitempty" json:"reasoningPolicy,omitempty"`
}

// Reasoning 回放策略取值。
const (
	ReasoningReplay = "replay" // 回放历史 reasoning（Gemini 需要：维持 thinking 签名链）
	ReasoningStrip  = "strip"  // 剥离历史 reasoning（DeepSeek 需要：工具回合禁止回传）
	ReasoningKeep   = "keep"   // 原样透传（OpenAI/Claude 等容忍额外字段的供应商）
)

// defaultReasoningPolicies 各供应商类型的内置 reasoning 回放策略。
// 注意：这是协议正确性约束，不是用户偏好——选错会直接报 400。
var defaultReasoningPolicies = map[string]string{
	ProviderGemini:   ReasoningReplay,
	ProviderDeepSeek: ReasoningStrip,
	ProviderQwen:     ReasoningStrip, // DashScope 工具回合同样要求剥离
	ProviderArk:      ReasoningStrip,
	ProviderOpenAI:   ReasoningKeep,
	ProviderClaude:   ReasoningKeep, // Claude 原生 thinking blocks 另有签名机制，由组件处理
	ProviderOllama:   ReasoningKeep,
	ProviderQianfan:  ReasoningKeep,
}

// ReasoningReplayMode 返回该供应商的 reasoning 回放策略
// （配置覆盖优先，缺省用类型内置默认）。
func (p *ProviderConfig) ReasoningReplayMode() string {
	if p.ReasoningPolicy != "" {
		return p.ReasoningPolicy
	}
	if d, ok := defaultReasoningPolicies[p.Type]; ok {
		return d
	}
	return ReasoningKeep
}

// ModelConfig 是一个可用模型条目。
// ID 不进 YAML——文件中 map key 即 ID，Validate 时归一化回填。
type ModelConfig struct {
	ID       string `yaml:"-" json:"id"`              // 唯一键（= models map 的 key），如 "gemini/gemini-3.1-flash-lite"
	Provider string `yaml:"provider" json:"provider"` // 引用 ProviderConfig.ID
	// ModelId 发送给 API 的模型名。注意 ark 类型填的是推理接入点 endpoint ID（ep-xxx）。
	ModelId string `yaml:"modelId" json:"modelId"`
	// 以下计量字段用于状态栏展示与费用估算
	ContextWindow         int     `yaml:"contextWindow,omitempty" json:"contextWindow,omitempty"`
	InputPricePerMillion  float64 `yaml:"inputPricePerMillion,omitempty" json:"inputPricePerMillion,omitempty"`
	OutputPricePerMillion float64 `yaml:"outputPricePerMillion,omitempty" json:"outputPricePerMillion,omitempty"`

	// ---- 模型私有参数（按供应商类型映射到原生字段，不支持的类型忽略） ----

	// MaxTokens 最大输出 tokens：claude 必填；deepseek 默认 4096 上限 8192；其余可选。
	MaxTokens int `yaml:"maxTokens,omitempty" json:"maxTokens,omitempty"`
	// ThinkingBudget 思考预算（仅 gemini）：nil 默认 / -1 动态 / 0 关闭 / >0 固定预算
	ThinkingBudget *int32 `yaml:"thinkingBudget,omitempty" json:"thinkingBudget,omitempty"`
	// EnableThinking 思考模式总开关（仅 deepseek/qwen/ark/ollama）：
	// nil 跟随服务端默认；true/false 显式开/关
	//（映射：deepseek ThinkingConfig.type、qwen enable_thinking、
	// ark Thinking.type、ollama ThinkValue）
	EnableThinking *bool `yaml:"enableThinking,omitempty" json:"enableThinking,omitempty"`
}

// Config 是 LLM 配置：供应商 + 模型条目注册表 + 当前激活条目。
//
// Providers/Models 以 map 存储，map key 即条目 ID（YAML/JSON 中同样以
// key 表达，条目结构内的 ID 字段不参与 YAML 序列化，避免双写）。
// Validate 会把条目内的 ID 归一化为 map key（条件写入，不触碰已一致的
// 共享条目）。Config 在发布后不可变（整体替换语义），map 查找无并发问题。
type Config struct {
	Active    string                     `yaml:"active,omitempty" json:"active,omitempty"` // 当前使用的 ModelConfig.ID
	Providers map[string]*ProviderConfig `yaml:"providers,omitempty" json:"providers,omitempty"`
	Models    map[string]*ModelConfig    `yaml:"models,omitempty" json:"models,omitempty"`
}

// FindProvider 按 ID 查找供应商，未找到返回 nil。
func (c *Config) FindProvider(id string) *ProviderConfig {
	return c.Providers[id]
}

// FindModel 按 ID 查找模型条目，未找到返回 nil。
func (c *Config) FindModel(id string) *ModelConfig {
	return c.Models[id]
}

// ActiveModel 返回当前激活的模型条目；active 未设置或失效时回退到
// key 排序后的第一个条目（map 无序，回退必须确定性）。
// 没有任何模型条目时返回 nil。
func (c *Config) ActiveModel() *ModelConfig {
	if m := c.FindModel(c.Active); m != nil {
		return m
	}
	first := ""
	for id := range c.Models {
		if first == "" || id < first {
			first = id
		}
	}
	if first == "" {
		return nil
	}
	return c.Models[first]
}

// Validate 校验配置的引用完整性，并把条目的 ID 归一化为 map key。
// 允许零模型条目（启动期容忍，首次对话时 Active() 才报错，
// 便于用户通过设置界面修复配置）。
func (c *Config) Validate() error {
	for id, p := range c.Providers {
		if id == "" {
			return fmt.Errorf("供应商 ID 不能为空")
		}
		if p == nil {
			return fmt.Errorf("供应商 %q 配置为空", id)
		}
		if p.ID != id {
			p.ID = id // 归一化：map key 是身份的唯一来源
		}
		if !IsValidProviderType(p.Type) {
			return fmt.Errorf("供应商 %q 的类型 %q 不支持（可选：%s）",
				id, p.Type, strings.Join(ProviderTypes(), " / "))
		}
	}
	for id, m := range c.Models {
		if id == "" {
			return fmt.Errorf("模型条目 ID 不能为空")
		}
		if m == nil {
			return fmt.Errorf("模型条目 %q 配置为空", id)
		}
		if m.ID != id {
			m.ID = id
		}
		if m.ModelId == "" {
			return fmt.Errorf("模型条目 %q 的模型 ID 不能为空", id)
		}
		if c.FindProvider(m.Provider) == nil {
			return fmt.Errorf("模型条目 %q 引用了不存在的供应商 %q", id, m.Provider)
		}
	}
	if c.Active != "" && c.FindModel(c.Active) == nil {
		return fmt.Errorf("当前模型 %q 不在模型列表中", c.Active)
	}
	return nil
}
