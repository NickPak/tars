package main

import (
	"errors"
	"tars/internal/boot"
	"tars/internal/config"
)

// ConfigService —— 应用配置的读取与保存（设置页通用页签）。
type ConfigService struct{}

// GetAppConfig 返回当前配置（密钥原样返回，前端用眼睛按钮控制显示）。
func (s *ConfigService) GetAppConfig() (*config.AppConfig, error) {
	cfg := config.Get()
	if cfg == nil {
		return nil, errors.New("config not initialized")
	}
	return cfg, nil
}

// SaveAppConfig 校验并保存配置：写回 config.yaml（保留注释与 apiKey 引用），
// 并热更新内存配置与模型注册表（model/agent/trace 立即生效，
// workDir 需重启生效——工作目录涉及存量会话数据搬迁）。
func (s *ConfigService) SaveAppConfig(v *config.AppConfig) error {
	return boot.GetApp().SaveAppConfig(v)
}
