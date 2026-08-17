package llm

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/cloudwego/eino-ext/components/model/ark"
	"github.com/cloudwego/eino-ext/components/model/claude"
	"github.com/cloudwego/eino-ext/components/model/deepseek"
	"github.com/cloudwego/eino-ext/components/model/gemini"
	"github.com/cloudwego/eino-ext/components/model/ollama"
	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino-ext/components/model/qianfan"
	"github.com/cloudwego/eino-ext/components/model/qwen"
	"github.com/cloudwego/eino/components/model"
	arkmodel "github.com/volcengine/volcengine-go-sdk/service/arkruntime/model"
	"google.golang.org/genai"
)

type Registry struct {
	mu     sync.RWMutex
	cfg    *Config
	models map[string]model.ToolCallingChatModel

	healthy map[string]bool
}

func NewRegistry(cfg *Config) *Registry {
	if cfg == nil {
		cfg = &Config{}
	}
	r := &Registry{
		cfg:     cfg,
		models:  map[string]model.ToolCallingChatModel{},
		healthy: map[string]bool{},
	}
	if err := cfg.Validate(); err == nil {
		// 预构建激活模型，尽早暴露配置问题（错误延迟到 Active() 暴露）
		_, _, _ = r.Active()
	}
	return r
}

func (r *Registry) SetHealthy(modelID string, ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.healthy[modelID] = ok
}

func (r *Registry) IsHealthy(modelID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ok, recorded := r.healthy[modelID]
	return !recorded || ok
}

func (r *Registry) ResetHealth() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.healthy = map[string]bool{}
}

func (r *Registry) UpdateConfig(cfg *Config) error {
	if cfg == nil {
		return errors.New("llm config is nil")
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	// 预构建激活模型，配置错误在保存时即暴露
	active, err := buildChatModel(context.Background(), cfg)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cfg = cfg
	r.models = map[string]model.ToolCallingChatModel{}
	if m := cfg.ActiveModel(); m != nil && active != nil {
		r.models[m.ID] = active
	}
	return nil
}

func (r *Registry) Config() *Config {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.cfg
}

func (r *Registry) Active() (model.ToolCallingChatModel, *ModelConfig, error) {
	cfg := r.Config()
	m := cfg.ActiveModel()
	if m == nil {
		return nil, nil, errors.New("尚未配置任何模型，请在设置中添加")
	}
	cm, err := r.chatModel(m.ID)
	return cm, m, err
}

func (r *Registry) ChatModel(modelID string) (model.ToolCallingChatModel, error) {
	return r.chatModel(modelID)
}

func (r *Registry) chatModel(modelID string) (model.ToolCallingChatModel, error) {
	r.mu.RLock()
	if cm, ok := r.models[modelID]; ok {
		r.mu.RUnlock()
		return cm, nil
	}
	cfg := r.cfg
	r.mu.RUnlock()

	m := cfg.FindModel(modelID)
	if m == nil {
		return nil, fmt.Errorf("模型条目 %q 不存在", modelID)
	}
	cm, err := buildOne(context.Background(), cfg, m)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.models[modelID] = cm
	r.mu.Unlock()
	return cm, nil
}

func buildChatModel(ctx context.Context, cfg *Config) (model.ToolCallingChatModel, error) {
	m := cfg.ActiveModel()
	if m == nil {
		return nil, nil // 零模型条目：合法状态，延迟到使用时报错
	}
	return buildOne(ctx, cfg, m)
}

func buildOne(ctx context.Context, cfg *Config, m *ModelConfig) (model.ToolCallingChatModel, error) {
	p := cfg.FindProvider(m.Provider)
	if p == nil {
		return nil, fmt.Errorf("模型条目 %q 引用了不存在的供应商 %q", m.ID, m.Provider)
	}
	switch p.Type {
	case ProviderGemini:
		return buildGemini(ctx, p, m)
	case ProviderOpenAI:
		return buildOpenAI(ctx, p, m)
	case ProviderClaude:
		return buildClaude(ctx, p, m)
	case ProviderDeepSeek:
		return buildDeepSeek(ctx, p, m)
	case ProviderQwen:
		return buildQwen(ctx, p, m)
	case ProviderArk:
		return buildArk(ctx, p, m)
	case ProviderOllama:
		return buildOllama(ctx, p, m)
	case ProviderQianfan:
		return buildQianfan(ctx, p, m)
	default:
		return nil, fmt.Errorf("供应商 %q 的类型 %q 不支持", p.ID, p.Type)
	}
}

func buildGemini(ctx context.Context, p *ProviderConfig, m *ModelConfig) (model.ToolCallingChatModel, error) {
	if p.ApiKey == "" {
		return nil, fmt.Errorf("供应商 %q 未配置 API Key", p.ID)
	}
	genaiClient, err := genai.NewClient(ctx, &genai.ClientConfig{APIKey: p.ApiKey})
	if err != nil {
		return nil, fmt.Errorf("供应商 %q 初始化失败：%w", p.ID, err)
	}
	return gemini.NewChatModel(ctx, &gemini.Config{
		Client: genaiClient,
		Model:  m.ModelId,
		ThinkingConfig: &genai.ThinkingConfig{
			IncludeThoughts: true,
			ThinkingBudget:  m.ThinkingBudget,
		},
	})
}

func buildOpenAI(ctx context.Context, p *ProviderConfig, m *ModelConfig) (model.ToolCallingChatModel, error) {
	if p.BaseUrl == "" {
		return nil, fmt.Errorf("供应商 %q 未配置 Base URL（openai 类型必填）", p.ID)
	}
	cfg := &openai.ChatModelConfig{
		APIKey:  p.ApiKey,
		BaseURL: p.BaseUrl,
		Model:   m.ModelId,
	}
	if m.MaxTokens > 0 {
		cfg.MaxCompletionTokens = &m.MaxTokens
	}
	return openai.NewChatModel(ctx, cfg)
}

func buildClaude(ctx context.Context, p *ProviderConfig, m *ModelConfig) (model.ToolCallingChatModel, error) {
	if p.ApiKey == "" {
		return nil, fmt.Errorf("供应商 %q 未配置 API Key", p.ID)
	}
	if m.MaxTokens <= 0 {
		return nil, fmt.Errorf("Claude 模型 %q 必须配置最大输出 tokens（maxTokens，Anthropic API 必填）", m.ID)
	}
	cfg := &claude.Config{
		APIKey:    p.ApiKey,
		Model:     m.ModelId,
		MaxTokens: m.MaxTokens,
	}
	if p.BaseUrl != "" {
		cfg.BaseURL = &p.BaseUrl
	}
	// 自动前缀缓存：在 system、工具定义与每轮最后一条 user 消息上打缓存断点。
	// 与我们的"静态前缀"提示词结构正好契合，能显著降低长会话成本。
	switch p.CacheTTL {
	case "5m":
		cfg.AutoCacheControl = &claude.CacheControl{TTL: claude.CacheTTL5m}
	case "1h":
		cfg.AutoCacheControl = &claude.CacheControl{TTL: claude.CacheTTL1h}
	}
	return claude.NewChatModel(ctx, cfg)
}

func buildDeepSeek(ctx context.Context, p *ProviderConfig, m *ModelConfig) (model.ToolCallingChatModel, error) {
	if p.ApiKey == "" {
		return nil, fmt.Errorf("供应商 %q 未配置 API Key", p.ID)
	}
	cfg := &deepseek.ChatModelConfig{
		APIKey: p.ApiKey,
		Model:  m.ModelId,
	}
	if p.BaseUrl != "" {
		cfg.BaseURL = p.BaseUrl
	}
	if m.MaxTokens > 0 {
		cfg.MaxTokens = m.MaxTokens
	}
	if m.EnableThinking != nil {
		typ := "disabled"
		if *m.EnableThinking {
			typ = "enabled"
		}
		cfg.ThinkingConfig = &deepseek.ThinkingConfig{Type: typ}
	}
	return deepseek.NewChatModel(ctx, cfg)
}

func buildQwen(ctx context.Context, p *ProviderConfig, m *ModelConfig) (model.ToolCallingChatModel, error) {
	if p.ApiKey == "" {
		return nil, fmt.Errorf("供应商 %q 未配置 API Key", p.ID)
	}
	if p.BaseUrl == "" {
		return nil, fmt.Errorf("供应商 %q 未配置 Base URL（qwen 类型必填，如 https://dashscope.aliyuncs.com/compatible-mode/v1）", p.ID)
	}
	cfg := &qwen.ChatModelConfig{
		APIKey:         p.ApiKey,
		BaseURL:        p.BaseUrl,
		Model:          m.ModelId,
		EnableThinking: m.EnableThinking,
	}
	if m.MaxTokens > 0 {
		cfg.MaxTokens = &m.MaxTokens
	}
	return qwen.NewChatModel(ctx, cfg)
}

func buildArk(ctx context.Context, p *ProviderConfig, m *ModelConfig) (model.ToolCallingChatModel, error) {
	if p.ApiKey == "" {
		return nil, fmt.Errorf("供应商 %q 未配置 API Key", p.ID)
	}
	cfg := &ark.ChatModelConfig{
		APIKey: p.ApiKey,
		Model:  m.ModelId, // 注意：ark 的 Model 是推理接入点 endpoint ID（ep-xxx）
	}
	if p.BaseUrl != "" {
		cfg.BaseURL = p.BaseUrl
	}
	if p.Region != "" {
		cfg.Region = p.Region
	}
	if m.MaxTokens > 0 {
		cfg.MaxTokens = &m.MaxTokens
	}
	if m.EnableThinking != nil {
		typ := arkmodel.ThinkingTypeDisabled
		if *m.EnableThinking {
			typ = arkmodel.ThinkingTypeEnabled
		}
		cfg.Thinking = &arkmodel.Thinking{Type: typ}
	}
	return ark.NewChatModel(ctx, cfg)
}

func buildOllama(ctx context.Context, p *ProviderConfig, m *ModelConfig) (model.ToolCallingChatModel, error) {
	cfg := &ollama.ChatModelConfig{
		BaseURL: p.BaseUrl, // 空则组件使用默认 http://localhost:11434
		Model:   m.ModelId,
	}
	if m.EnableThinking != nil {
		cfg.Thinking = &ollama.ThinkValue{Value: *m.EnableThinking}
	}
	return ollama.NewChatModel(ctx, cfg)
}

func buildQianfan(ctx context.Context, p *ProviderConfig, m *ModelConfig) (model.ToolCallingChatModel, error) {
	// 千帆 SDK 走全局单例配置，构建前注入 AK/SK
	if p.AccessKey == "" || p.SecretKey == "" {
		return nil, fmt.Errorf("供应商 %q 未配置 Access Key / Secret Key（qianfan 类型必填）", p.ID)
	}
	qianfan.GetQianfanSingletonConfig().AccessKey = p.AccessKey
	qianfan.GetQianfanSingletonConfig().SecretKey = p.SecretKey
	cfg := &qianfan.ChatModelConfig{Model: m.ModelId}
	if m.MaxTokens > 0 {
		cfg.MaxCompletionTokens = &m.MaxTokens
	}
	return qianfan.NewChatModel(ctx, cfg)
}
