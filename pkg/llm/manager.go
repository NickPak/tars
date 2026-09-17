package llm

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
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

type Manager struct {
	mu     sync.RWMutex
	cfg    *Config
	models map[string]model.ToolCallingChatModel
}

func NewManager(cfg *Config) *Manager {
	if cfg == nil {
		cfg = &Config{}
	}
	r := &Manager{
		cfg:    cfg,
		models: map[string]model.ToolCallingChatModel{},
	}
	return r
}

func (r *Manager) Startup() error {
	// 预构建激活模型，尽早暴露配置问题（错误延迟到 Active() 暴露）
	_, _, err := r.Active()
	return err
}

func (r *Manager) Shutdown() error {
	return nil
}

func (r *Manager) UpdateConfig(cfg *Config) error {
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
		r.models[m.EntryID] = active
	}
	return nil
}

func (r *Manager) Config() *Config {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.cfg
}

// ContextWindow 返回当前激活模型的上下文窗口（token 数）；未配置返回 0。
// 纯配置读——绝不允许走 Active()（后者触发模型客户端构建，为读一个 int
// 建 eino client 是数量级错误的粒度，且零模型时返回 error 徒增分支）。
func (r *Manager) ContextWindow() int {
	cfg := r.Config()
	if cfg == nil {
		return 0
	}
	if m := cfg.ActiveModel(); m != nil {
		return m.ContextWindow
	}
	return 0
}

func (r *Manager) Active() (model.ToolCallingChatModel, *ModelConfig, error) {
	cfg := r.Config()
	m := cfg.ActiveModel()
	if m == nil {
		return nil, nil, errors.New("尚未配置任何模型，请在设置中添加")
	}
	cm, err := r.chatModel(m.EntryID)
	return cm, m, err
}

func (r *Manager) ChatModel(entryID string) (model.ToolCallingChatModel, error) {
	return r.chatModel(entryID)
}

func (r *Manager) chatModel(entryID string) (model.ToolCallingChatModel, error) {
	r.mu.RLock()
	if cm, ok := r.models[entryID]; ok {
		r.mu.RUnlock()
		return cm, nil
	}
	cfg := r.cfg
	r.mu.RUnlock()

	m := cfg.FindModel(entryID)
	if m == nil {
		return nil, fmt.Errorf("模型条目 %q 不存在", entryID)
	}
	cm, err := buildOne(context.Background(), cfg, m)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.models[entryID] = cm
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
		return nil, fmt.Errorf("模型条目 %q 引用了不存在的供应商 %q", m.EntryID, m.Provider)
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

// f32p 把请求默认值 *float64 转为 eino 各组件的 *float32；nil 不下发。
func f32p(v *float64) *float32 {
	if v == nil {
		return nil
	}
	f := float32(*v)
	return &f
}

// f32pClamped 同 f32p，但收敛到供应商文档的更窄区间（如 claude/qianfan
// 的 [0,1]）——全局 Validate 放行 [0,2]，超界值在构建期收敛而非报错。
func f32pClamped(v *float64, hi float32) *float32 {
	f := f32p(v)
	if f != nil && *f > hi {
		*f = hi
	}
	return f
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
		Client:      genaiClient,
		Model:       m.ModelId,
		Temperature: f32p(m.Temperature),
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
	// BaseURL 只到服务根（如 .../v1）：客户端会自行拼接 /chat/completions。
	// 用户误填完整端点时归一化，否则路径重复（.../chat/completions/chat/completions → 404）。
	baseURL := strings.TrimSuffix(strings.TrimRight(p.BaseUrl, "/"), "/chat/completions")
	if baseURL != p.BaseUrl {
		slog.Warn("供应商 BaseUrl 含端点后缀，已归一化", "provider", p.ID, "baseUrl", baseURL)
	}
	cfg := &openai.ChatModelConfig{
		APIKey:      p.ApiKey,
		BaseURL:     baseURL,
		Model:       m.ModelId,
		Temperature: f32p(m.Temperature),
	}
	if m.MaxTokens > 0 {
		cfg.MaxCompletionTokens = &m.MaxTokens
	}
	// 推理强度经能力门控：仅当条目声明支持推理才下发（往不支持的
	// 端点发 reasoning_effort 会直接 400）。ReasoningSummary 属
	// Responses API 范畴，chat completions 无对应字段，暂不下发。
	if m.ReasoningEnabled() && m.ReasoningEffort != "" {
		cfg.ReasoningEffort = openai.ReasoningEffortLevel(m.ReasoningEffort)
	}
	return openai.NewChatModel(ctx, cfg)
}

func buildClaude(ctx context.Context, p *ProviderConfig, m *ModelConfig) (model.ToolCallingChatModel, error) {
	if p.ApiKey == "" {
		return nil, fmt.Errorf("供应商 %q 未配置 API Key", p.ID)
	}
	if m.MaxTokens <= 0 {
		return nil, fmt.Errorf("Claude 模型 %q 必须配置最大输出 tokens（maxTokens，Anthropic API 必填）", m.EntryID)
	}
	cfg := &claude.Config{
		APIKey:      p.ApiKey,
		Model:       m.ModelId,
		MaxTokens:   m.MaxTokens,
		Temperature: f32pClamped(m.Temperature, 1.0), // Anthropic 区间 [0,1]
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
	if m.Temperature != nil {
		cfg.Temperature = float32(*m.Temperature) // deepseek 为值类型字段
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
		Temperature:    f32p(m.Temperature),
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
		APIKey:      p.ApiKey,
		Model:       m.ModelId, // 注意：ark 的 Model 是推理接入点 endpoint ID（ep-xxx）
		Temperature: f32p(m.Temperature),
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
	if m.Temperature != nil {
		cfg.Options = &ollama.Options{Temperature: float32(*m.Temperature)}
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
	cfg := &qianfan.ChatModelConfig{
		Model:       m.ModelId,
		Temperature: f32pClamped(m.Temperature, 1.0), // 千帆区间 (0,1]，组件默认 0.95
	}
	if m.MaxTokens > 0 {
		cfg.MaxCompletionTokens = &m.MaxTokens
	}
	return qianfan.NewChatModel(ctx, cfg)
}
