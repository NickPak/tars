package main

import (
	"fmt"
)

// SessionStats 是会话级聚合统计，供底部状态栏展示。
// 轮次级指标（本次命中率/本次费用/tokens/耗时）随每条 assistant 消息的
// Usage/ElapsedMs 持久化，由前端在消息底部直接渲染，不在此聚合。
type SessionStats struct {
	// ModelID 当前使用的模型名。
	ModelID string `json:"modelId"`
	// ModelHealthy 最近一次 LLM 调用是否成功（状态栏绿/红灯）。
	ModelHealthy bool `json:"modelHealthy"`
	// Rounds 会话轮次（user 消息数）。
	Rounds int `json:"rounds"`
	// TotalTokens 会话累计 token（所有 assistant 回复合计）。
	TotalTokens int `json:"totalTokens"`
	// TotalCredits 会话累计 Credits（1 credit = 1000 tokens，直观量级指标）。
	TotalCredits float64 `json:"totalCredits"`
	// TotalCostYuan 会话累计费用（元），按配置的价格表估算。
	TotalCostYuan float64 `json:"totalCostYuan"`
	// AvgCacheHitRate 会话平均缓存命中率 = Σcached / Σprompt（0-1）。
	AvgCacheHitRate float64 `json:"avgCacheHitRate"`
	// ContextUsage 上下文使用占比 = 最近一次请求的 promptTokens / ContextWindow（0-1）。
	ContextUsage float64 `json:"contextUsage"`
	// ContextWindow 模型上下文窗口（tokens），供前端展示 "x/y" 形式。
	ContextWindow int `json:"contextWindow"`
	// CompressionThreshold 压缩阈值配置（0-1）。
	CompressionThreshold float64 `json:"compressionThreshold"`
	// 价格表透传给前端，用于计算每条消息的"本次费用"（价格变更时历史
	// 消息的展示费用随当前价格浮动，属预期行为 —— 费用是估算值而非账单）。
	InputPricePerMillion  float64 `json:"inputPricePerMillion"`
	OutputPricePerMillion float64 `json:"outputPricePerMillion"`
}

// setModelHealthy 记录最近一次 LLM 调用的健康状态。
func (s *AgentService) setModelHealthy(ok bool) {
	s.modelHealthy.Store(ok)
}

// GetSessionStats 返回指定会话的聚合统计。空会话返回带模型/价格信息的零值。
func (s *AgentService) GetSessionStats(conversationID string) (*SessionStats, error) {
	if !s.hasConversation(conversationID) {
		return nil, fmt.Errorf("conversation not found: %s", conversationID)
	}

	stats := &SessionStats{
		ModelID:               s.appConfig.LLM.ModelId,
		ModelHealthy:          s.modelHealthy.Load(),
		ContextWindow:         s.appConfig.LLM.ContextWindow,
		CompressionThreshold:  s.appConfig.CompressionThresholdOrDefault(),
		InputPricePerMillion:  s.appConfig.LLM.InputPricePerMillion,
		OutputPricePerMillion: s.appConfig.LLM.OutputPricePerMillion,
	}

	s.mu.RLock()
	conv, ok := s.convs[conversationID]
	if !ok {
		s.mu.RUnlock()
		return stats, nil
	}
	// 拷贝消息切片头，缩短持锁时间（统计只读 Usage/CreatedAt 等标量字段）
	msgs := append([]Message{}, conv.Messages...)
	s.mu.RUnlock()

	var totalPrompt, totalCached int
	var lastPromptTokens int
	for _, m := range msgs {
		switch m.Role {
		case RoleUser:
			stats.Rounds++
		case RoleAssistant:
			if m.Usage == nil {
				continue
			}
			u := m.Usage
			stats.TotalTokens += u.TotalTokens
			totalPrompt += u.PromptTokens
			totalCached += u.CachedTokens
			if u.PromptTokens > 0 {
				lastPromptTokens = u.PromptTokens // 最后一条有 usage 的 assistant 即最新上下文规模
			}
			stats.TotalCostYuan += float64(u.PromptTokens)*stats.InputPricePerMillion/1e6 +
				float64(u.CompletionTokens)*stats.OutputPricePerMillion/1e6
		}
	}

	stats.TotalCredits = float64(stats.TotalTokens) / 1000.0
	if totalPrompt > 0 {
		stats.AvgCacheHitRate = float64(totalCached) / float64(totalPrompt)
	}
	if stats.ContextWindow > 0 {
		stats.ContextUsage = float64(lastPromptTokens) / float64(stats.ContextWindow)
	}

	return stats, nil
}
