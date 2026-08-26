package main

import (
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"tars/internal/config"
	"tars/pkg/llm"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// ModelInfo describes one configured model entry.
type ModelInfo struct {
	EntryID       string `json:"entryId"`       // 配置条目 ID（models[].entryId）
	Provider      string `json:"provider"`      // 供应商 ID
	ProviderType  string `json:"providerType"`  // 供应商类型（gemini/openai）
	ModelID       string `json:"modelId"`       // 发送给 API 的真实模型名
	ContextWindow int    `json:"contextWindow"` // 上下文窗口大小（tokens），0 = 未知
	Active        bool   `json:"active"`        // 是否为当前使用中的模型
}

// ModelChangedEvent is the payload of the "model:changed" event.
type ModelChangedEvent struct {
	Model ModelInfo `json:"model"`
}

func modelInfoOf(m *llm.ModelConfig, cfg *llm.Config) ModelInfo {
	info := ModelInfo{
		EntryID:       m.EntryID,
		Provider:      m.Provider,
		ModelID:       m.ModelId,
		ContextWindow: m.ContextWindow,
	}
	if p := cfg.FindProvider(m.Provider); p != nil {
		info.ProviderType = p.Type
	}
	if active := cfg.ActiveModel(); active != nil && active.EntryID == m.EntryID {
		info.Active = true
	}
	return info
}

// GetModelInfo returns the currently active model (TopicBar/状态栏展示用）。
// 未配置任何模型时返回空对象（前端退化为不显示）。
func (s *AgentService) GetModelInfo() (*ModelInfo, error) {
	cfg := s.app.GetLLMMgr().Config()
	m := cfg.ActiveModel()
	if m == nil {
		return &ModelInfo{}, nil
	}
	info := modelInfoOf(m, cfg)
	return &info, nil
}

// ListModels returns all configured model entries（TopicBar 切换下拉用）。
// 按条目 ID 排序返回（map 无序，下拉列表需要确定性顺序）。
func (s *AgentService) ListModels() ([]ModelInfo, error) {
	cfg := s.app.GetLLMMgr().Config()
	ids := make([]string, 0, len(cfg.Models))
	for id := range cfg.Models {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	infos := make([]ModelInfo, 0, len(ids))
	for _, id := range ids {
		infos = append(infos, modelInfoOf(cfg.Models[id], cfg))
	}
	return infos, nil
}

// SetActiveModel switches the active model: 预构建目标模型（失败则不切换），
// 热更新注册表并落盘（active 键），最后广播 model:changed 事件。
func (s *AgentService) SetActiveModel(id string) error {
	cfg := s.app.GetLLMMgr().Config()
	if cfg.FindModel(id) == nil {
		return fmt.Errorf("模型条目 %q 不存在", id)
	}
	if active := cfg.ActiveModel(); active != nil && active.EntryID == id {
		return nil // 已是当前模型
	}

	newLLM := *cfg
	newLLM.Active = id
	// UpdateConfig 会预构建激活模型，配置错误在此暴露，不影响现状
	if err := s.app.GetLLMMgr().UpdateConfig(&newLLM); err != nil {
		return err
	}

	if cur := config.Get(); cur != nil {
		newCfg := *cur
		newCfg.LLM = &newLLM
		config.Set(&newCfg)
	}

	if err := s.persistConfigFile(); err != nil {
		// 内存已切换，落盘失败仅警告（重启后回退到文件中的 active）
		slog.Warn("Failed to persist active model", "id", id, "error", err)
	}

	application.Get().Event.Emit("model:changed", &ModelChangedEvent{
		Model: modelInfoOf(newLLM.FindModel(id), &newLLM),
	})
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
