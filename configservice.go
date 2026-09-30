package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"

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

// BroadcastTheme 主题切换广播（theme:changed）到所有窗口，并落一份
// 主题小文件（Go 侧创建窗口时按它给底色，避免浅色主题开窗黑闪）。
// 主题的运行时持久化主通道是前端 localStorage（同 profile 各窗口共享），
// 不切 config.yaml。
func (s *ConfigService) BroadcastTheme(theme string) error {
	if theme != "dark" && theme != "light" {
		return fmt.Errorf("unknown theme: %s", theme)
	}
	_ = os.MkdirAll(config.DefaultDataDir(), 0755)
	_ = os.WriteFile(themeFile(), []byte(theme), 0644)
	if app := application.Get(); app != nil {
		app.Event.Emit("theme:changed", theme)
	}
	return nil
}

// BroadcastLocale 界面语言切换广播（locale:changed）到所有窗口。
// 与主题同理：持久化在前端 localStorage，这里只做实时同步转发。
// pref 为空串表示"跟随系统"。
func (s *ConfigService) BroadcastLocale(pref string) error {
	if pref != "" && pref != "zh" && pref != "en" {
		return fmt.Errorf("unknown locale: %s", pref)
	}
	if app := application.Get(); app != nil {
		app.Event.Emit("locale:changed", pref)
	}
	return nil
}

// themeFile 主题持久化小文件路径。
func themeFile() string {
	return filepath.Join(config.DefaultDataDir(), "theme")
}

// windowBackground 按当前主题给窗口底色（创建窗口时用；默认深色）。
func windowBackground() application.RGBA {
	b, err := os.ReadFile(themeFile())
	if err == nil && strings.TrimSpace(string(b)) == "light" {
		return application.NewRGB(255, 255, 255)
	}
	return application.NewRGB(19, 19, 20)
}
