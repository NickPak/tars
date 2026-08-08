package main

// ModelInfo 描述当前配置的模型（不含 apiKey 等敏感字段），
// 供前端 TopicBar/状态栏展示。
type ModelInfo struct {
	ModelID string `json:"modelId"`
	// ContextWindow 模型上下文窗口（tokens）。
	ContextWindow int `json:"contextWindow"`
}

// GetModelInfo 返回当前 LLM 配置的可公开信息。
// 调用方无需会话上下文 —— 模型是应用级配置。
func (s *AgentService) GetModelInfo() (*ModelInfo, error) {
	return &ModelInfo{
		ModelID:       s.appConfig.LLM.ModelId,
		ContextWindow: s.appConfig.LLM.ContextWindow,
	}, nil
}
