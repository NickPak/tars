package main

import (
	"errors"
	"log/slog"

	"tars/internal/config"
	"tars/internal/skills"
	"tars/pkg/llm"
	"tars/pkg/trace"
)

// GetAppConfig 返回当前配置（密钥原样返回，前端用眼睛按钮控制显示）。
func (s *AgentService) GetAppConfig() (*config.AppConfig, error) {
	cfg := config.Get()
	if cfg == nil {
		return nil, errors.New("config not initialized")
	}
	return cfg, nil
}

// SaveAppConfig 校验并保存配置：写回 config.yaml（保留注释与 apiKey 引用），
// 并热更新内存配置与模型注册表（model/agent/trace 立即生效，
// workDir 需重启生效——工作目录涉及存量会话数据搬迁）。
func (s *AgentService) SaveAppConfig(v *config.AppConfig) error {
	if v == nil {
		return errors.New("config is nil")
	}

	// 校验并修正（默认值填充 + LLM 结构校验，逻辑在配置结构自身）
	if err := v.Validate(); err != nil {
		return err
	}

	// 先热更新注册表：UpdateConfig 会预构建激活模型，配置无效则
	// 整体不落盘、不生效，保持现状。
	if err := llm.GetRegistry().UpdateConfig(v.LLM); err != nil {
		return errors.New("LLM 配置无效：" + err.Error())
	}

	if err := config.SaveAppConfigFile(v); err != nil {
		return errors.New("写入配置文件失败：" + err.Error())
	}

	config.Set(v)
	// 配置可能修复了密钥/端点：清空健康记录，给新配置一次全新尝试
	llm.GetRegistry().ResetHealth()
	// 追踪配置（开关/端点）可能已变：重建全局 tracer
	trace.Rebuild(v.Trace)
	// 技能索引档位阈值可能已变：更新并重建索引（下一次对话立即生效）
	skills.GetManager().UpdateConfig(v.Skills)
	if err := skills.GetManager().GenerateIndex(); err != nil {
		slog.Warn("Failed to regenerate skills index after config save", "error", err)
	}
	return nil
}

// persistConfigFile 把当前内存配置写回 config.yaml。
// 所有供应商的密钥先剥离（置空），由 SaveAppConfigFile 的保留逻辑
// 从文件原文回填——避免把内存中已展开的密钥写进配置文件。
func (s *AgentService) persistConfigFile() error {
	cfg := config.Get()
	if cfg == nil {
		return errors.New("config not initialized")
	}
	fileCfg := *cfg
	if cfg.LLM != nil {
		llmCopy := *cfg.LLM
		providers := make(map[string]*llm.ProviderConfig, len(llmCopy.Providers))
		for id, p := range llmCopy.Providers {
			cp := *p
			cp.ApiKey, cp.AccessKey, cp.SecretKey = "", "", ""
			providers[id] = &cp
		}
		llmCopy.Providers = providers
		fileCfg.LLM = &llmCopy
	}
	return config.SaveAppConfigFile(&fileCfg)
}
