package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"tars/internal/config"
	"tars/pkg/llm"
)

// ProviderView 是供应商配置对前端的视图。
// 安全约定：ApiKey 读取时恒为空串（不落网传输已保存的密钥），
// 仅返回 ApiKeySet 标记"是否已配置"；保存时 ApiKey 非空才覆盖。
type ProviderView struct {
	ID        string `json:"id"`
	Type      string `json:"type"` // gemini | openai | claude | deepseek | qwen | ark | ollama | qianfan
	ApiKey    string `json:"apiKey"`
	ApiKeySet bool   `json:"apiKeySet"`
	BaseUrl   string `json:"baseUrl"`
	// Timeout 人类可读的时长字符串（如 "60s"、"2m"），空 = 不设超时
	Timeout string `json:"timeout"`
	// ---- 供应商私有字段 ----
	AccessKey string `json:"accessKey"` // qianfan AK（读取时脱敏为空）
	SecretKey string `json:"secretKey"` // qianfan SK（读取时脱敏为空）
	KeySet    bool   `json:"keySet"`    // AK/SK 是否均已配置
	Region    string `json:"region"`    // ark 区域，默认 cn-beijing
	CacheTTL  string `json:"cacheTTL"`  // claude 自动前缀缓存："5m"/"1h"/"" 关闭
	// ReasoningPolicy 历史 reasoning 回放策略："" 内置默认 / replay / strip / keep。
	// 留空用供应商类型的内置默认（DeepSeek/Qwen/ARK=strip，Gemini=replay，其余=keep）
	ReasoningPolicy string `json:"reasoningPolicy"`
}

// ModelView 是模型条目对前端的视图。
type ModelView struct {
	ID                    string  `json:"id"`
	Provider              string  `json:"provider"`
	ModelID               string  `json:"modelId"`
	ContextWindow         int     `json:"contextWindow"`
	InputPricePerMillion  float64 `json:"inputPricePerMillion"`
	OutputPricePerMillion float64 `json:"outputPricePerMillion"`
	// MaxTokens 最大输出 tokens（claude 必填；deepseek 默认 4096 上限 8192；其余可选）
	MaxTokens int `json:"maxTokens"`
	// ThinkingBudget 字符串形式整数："" 默认 / "-1" 动态 / "0" 关闭 / ">0" 固定预算
	//（仅 gemini 类型供应商的模型使用）
	ThinkingBudget string `json:"thinkingBudget"`
	// EnableThinking 三态："" 默认 / "on" 开启 / "off" 关闭
	//（仅 deepseek/qwen/ark/ollama 类型供应商的模型使用）
	EnableThinking string `json:"enableThinking"`
}

// LLMConfigView 是 LLM 配置对前端的视图。
type LLMConfigView struct {
	Active    string         `json:"active"`
	Providers []ProviderView `json:"providers"`
	Models    []ModelView    `json:"models"`
}

// AgentConfigView 是 Agent 运行时配置对前端的视图。
type AgentConfigView struct {
	MaxIterations        int     `json:"maxIterations"`
	CompressionThreshold float64 `json:"compressionThreshold"`
}

// TraceConfigView 是追踪配置对前端的视图。
type TraceConfigView struct {
	// Enabled 追踪总开关：false 时即使配置了端点也不产生任何 span
	Enabled          bool   `json:"enabled"`
	OTLPHTTPEndpoint string `json:"otlpHttpEndpoint"`
	OTLPGrpcEndpoint string `json:"otlpGrpcEndpoint"`
}

// AppConfigView 是设置界面读写的完整配置视图。
type AppConfigView struct {
	LLM     LLMConfigView   `json:"llm"`
	WorkDir string          `json:"workDir"`
	Agent   AgentConfigView `json:"agent"`
	Trace   TraceConfigView `json:"trace"`
}

// GetAppConfig 返回当前配置（apiKey 脱敏）。
func (s *AgentService) GetAppConfig() (*AppConfigView, error) {
	cfg := s.currentConfig()
	if cfg == nil {
		return nil, errors.New("config not initialized")
	}

	v := &AppConfigView{WorkDir: cfg.WorkDir}
	if cfg.LLM != nil {
		v.LLM.Active = cfg.LLM.Active
		// active 未设置时回退第一个条目（与 ActiveModel 解析语义一致）
		if v.LLM.Active == "" && len(cfg.LLM.Models) > 0 {
			v.LLM.Active = cfg.LLM.Models[0].ID
		}
		for _, p := range cfg.LLM.Providers {
			v.LLM.Providers = append(v.LLM.Providers, ProviderView{
				ID:        p.ID,
				Type:      p.Type,
				ApiKeySet: p.ApiKey != "",
				BaseUrl:   p.BaseUrl,
				Timeout:   p.Timeout,
				KeySet:    p.AccessKey != "" && p.SecretKey != "",
				Region:    p.Region,
				CacheTTL:  p.CacheTTL,
				ReasoningPolicy: p.ReasoningPolicy,
			})
		}
		for _, m := range cfg.LLM.Models {
			mv := ModelView{
				ID:                    m.ID,
				Provider:              m.Provider,
				ModelID:               m.ModelId,
				ContextWindow:         m.ContextWindow,
				InputPricePerMillion:  m.InputPricePerMillion,
				OutputPricePerMillion: m.OutputPricePerMillion,
				MaxTokens:             m.MaxTokens,
			}
			if m.ThinkingBudget != nil {
				mv.ThinkingBudget = strconv.FormatInt(int64(*m.ThinkingBudget), 10)
			}
			if m.EnableThinking != nil {
				if *m.EnableThinking {
					mv.EnableThinking = "on"
				} else {
					mv.EnableThinking = "off"
				}
			}
			v.LLM.Models = append(v.LLM.Models, mv)
		}
	}
	v.Agent = AgentConfigView{
		MaxIterations:        cfg.MaxIterationsOrDefault(),
		CompressionThreshold: cfg.CompressionThresholdOrDefault(),
	}
	if cfg.Trace != nil {
		v.Trace.Enabled = cfg.Trace.Enabled
	}
	v.Trace.OTLPHTTPEndpoint, v.Trace.OTLPGrpcEndpoint = cfg.OTLPEndpoints()
	return v, nil
}

// SaveAppConfig 校验并保存配置：写回 config.yaml（保留注释与 apiKey 引用），
// 并热更新内存配置与模型注册表（model/agent/trace 立即生效，
// workDir 需重启生效——工作目录涉及存量会话数据搬迁）。
func (s *AgentService) SaveAppConfig(v *AppConfigView) error {
	if v == nil {
		return errors.New("config is nil")
	}

	newLLM, err := llmViewToConfig(&v.LLM)
	if err != nil {
		return err
	}
	if v.Agent.MaxIterations <= 0 {
		return errors.New("最大迭代次数必须大于 0")
	}
	if v.Agent.CompressionThreshold <= 0 || v.Agent.CompressionThreshold > 1 {
		return errors.New("压缩阈值必须在 (0, 1] 之间")
	}

	// 生效配置需要真实的密钥：视图为空时按供应商 ID 沿用内存中的展开值
	//（写文件时的"空则保留原值"由 SaveAppConfigFile 按文件内容处理）。
	if cur := s.currentConfig(); cur != nil && cur.LLM != nil {
		for i := range newLLM.Providers {
			old := cur.LLM.FindProvider(newLLM.Providers[i].ID)
			if old == nil {
				continue
			}
			if newLLM.Providers[i].ApiKey == "" {
				newLLM.Providers[i].ApiKey = old.ApiKey
			}
			if newLLM.Providers[i].AccessKey == "" {
				newLLM.Providers[i].AccessKey = old.AccessKey
			}
			if newLLM.Providers[i].SecretKey == "" {
				newLLM.Providers[i].SecretKey = old.SecretKey
			}
		}
	}

	newCfg := &config.AppConfig{
		LLM:     newLLM,
		WorkDir: strings.TrimSpace(v.WorkDir),
		Agent: &config.AgentConfig{
			MaxIterations:        v.Agent.MaxIterations,
			CompressionThreshold: v.Agent.CompressionThreshold,
		},
		Trace: &config.TraceConfig{
			Enabled:          v.Trace.Enabled,
			OTLPHTTPEndpoint: strings.TrimSpace(v.Trace.OTLPHTTPEndpoint),
			OTLPGrpcEndpoint: strings.TrimSpace(v.Trace.OTLPGrpcEndpoint),
		},
	}

	// 先热更新注册表：UpdateConfig 会预构建激活模型，配置无效则
	// 整体不落盘、不生效，保持现状。
	if err := s.llmReg.UpdateConfig(newLLM); err != nil {
		return fmt.Errorf("LLM 配置无效：%w", err)
	}

	if err := config.SaveAppConfigFile(appConfigPath, newCfg); err != nil {
		return fmt.Errorf("写入配置文件失败：%w", err)
	}

	s.cfgMu.Lock()
	s.appConfig = newCfg
	s.otlpHTTPEndpoint, s.otlpGrpcEndpoint = newCfg.Trace.OTLPHTTPEndpoint, newCfg.Trace.OTLPGrpcEndpoint
	s.cfgMu.Unlock()
	// 追踪配置（开关/端点）可能已变：重建所有会话的 tracer
	s.rebuildTracers()
	return nil
}

// llmViewToConfig 把视图转换为配置结构（校验在 Config.Validate 中统一做）。
func llmViewToConfig(v *LLMConfigView) (*llm.Config, error) {
	cfg := &llm.Config{Active: strings.TrimSpace(v.Active)}
	for _, p := range v.Providers {
		if p.CacheTTL != "" && p.CacheTTL != "5m" && p.CacheTTL != "1h" {
			return nil, fmt.Errorf("供应商 %q 的缓存 TTL 只能是 5m / 1h / 留空", p.ID)
		}
		switch p.ReasoningPolicy {
		case "", llm.ReasoningReplay, llm.ReasoningStrip, llm.ReasoningKeep:
		default:
			return nil, fmt.Errorf("供应商 %q 的 reasoning 回放策略只能是 默认/replay/strip/keep", p.ID)
		}
		cfg.Providers = append(cfg.Providers, llm.ProviderConfig{
			ID:        strings.TrimSpace(p.ID),
			Type:      strings.TrimSpace(p.Type),
			ApiKey:    strings.TrimSpace(p.ApiKey),
			BaseUrl:   strings.TrimSpace(p.BaseUrl),
			Timeout:   strings.TrimSpace(p.Timeout),
			AccessKey: strings.TrimSpace(p.AccessKey),
			SecretKey: strings.TrimSpace(p.SecretKey),
			Region:    strings.TrimSpace(p.Region),
			CacheTTL:  strings.TrimSpace(p.CacheTTL),
			ReasoningPolicy: strings.TrimSpace(p.ReasoningPolicy),
		})
	}
	for _, m := range v.Models {
		var budget *int32
		if tb := strings.TrimSpace(m.ThinkingBudget); tb != "" {
			n, err := strconv.ParseInt(tb, 10, 32)
			if err != nil {
				return nil, fmt.Errorf("模型条目 %q 的思考预算必须是整数（-1 动态 / 0 关闭 / >0 固定）", m.ID)
			}
			b := int32(n)
			budget = &b
		}
		var enableThinking *bool
		switch strings.TrimSpace(m.EnableThinking) {
		case "on":
			t := true
			enableThinking = &t
		case "off":
			f := false
			enableThinking = &f
		case "":
		default:
			return nil, fmt.Errorf("模型条目 %q 的思考模式只能是 默认/开启/关闭", m.ID)
		}
		cfg.Models = append(cfg.Models, llm.ModelConfig{
			ID:                    strings.TrimSpace(m.ID),
			Provider:              strings.TrimSpace(m.Provider),
			ModelId:               strings.TrimSpace(m.ModelID),
			ContextWindow:         m.ContextWindow,
			InputPricePerMillion:  m.InputPricePerMillion,
			OutputPricePerMillion: m.OutputPricePerMillion,
			MaxTokens:             m.MaxTokens,
			ThinkingBudget:        budget,
			EnableThinking:        enableThinking,
		})
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// persistConfigFile 把当前内存配置写回 config.yaml。
// 所有供应商的密钥先剥离（置空），由 SaveAppConfigFile 的保留逻辑
// 从文件原文回填——避免把内存中已展开的密钥写进配置文件。
func (s *AgentService) persistConfigFile() error {
	cfg := s.currentConfig()
	if cfg == nil {
		return errors.New("config not initialized")
	}
	fileCfg := *cfg
	if cfg.LLM != nil {
		llmCopy := *cfg.LLM
		providers := make([]llm.ProviderConfig, len(llmCopy.Providers))
		for i, p := range llmCopy.Providers {
			p.ApiKey, p.AccessKey, p.SecretKey = "", "", ""
			providers[i] = p
		}
		llmCopy.Providers = providers
		fileCfg.LLM = &llmCopy
	}
	return config.SaveAppConfigFile(appConfigPath, &fileCfg)
}
