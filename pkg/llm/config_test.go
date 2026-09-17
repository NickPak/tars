package llm

import (
	"strings"
	"testing"
	"time"
)

// 清空全部供应商与模型必须允许（设置页清空后保存的场景）：
// Validate 放行并把残留的 Active 归零；首次对话时 Active() 才报错。
func TestValidateEmptyModelsAllowed(t *testing.T) {
	for name, models := range map[string]map[string]*ModelConfig{
		"empty map": {},
		"nil map":   nil,
	} {
		t.Run(name, func(t *testing.T) {
			cfg := &Config{
				Active:    "gemini/gemini-3.1-flash-lite", // 清空前的残留值
				Providers: map[string]*ProviderConfig{},
				Models:    models,
			}
			if err := cfg.Validate(); err != nil {
				t.Fatalf("empty models should be allowed: %v", err)
			}
			if cfg.Active != "" {
				t.Fatalf("Active should be reset, got %q", cfg.Active)
			}
			if m := cfg.ActiveModel(); m != nil {
				t.Fatal("ActiveModel should be nil with zero models")
			}
		})
	}
}

// 模型列表非空时，Active 悬挂（指向不存在的条目）仍然是错误。
func TestValidateDanglingActiveRejected(t *testing.T) {
	cfg := &Config{
		Active: "p/ghost",
		Providers: map[string]*ProviderConfig{
			"p": {ID: "p", Type: "openai"},
		},
		Models: map[string]*ModelConfig{
			"p/m": {EntryID: "p/m", Provider: "p", ModelId: "m"},
		},
	}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "不在模型列表中") {
		t.Fatalf("dangling active should be rejected, got: %v", err)
	}
}

func TestValidateActiveOK(t *testing.T) {
	cfg := &Config{
		Active: "p/m",
		Providers: map[string]*ProviderConfig{
			"p": {ID: "p", Type: "openai"},
		},
		Models: map[string]*ModelConfig{
			"p/m": {EntryID: "p/m", Provider: "p", ModelId: "m"},
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	if m := cfg.ActiveModel(); m == nil || m.EntryID != "p/m" {
		t.Fatalf("ActiveModel = %+v", m)
	}
}

// 能力声明归一化：老配置（nil）回填默认值——工具默认开（现状行为不变），
// 图片/推理默认关（显式声明才开启）；显式设置的值不被覆盖。
func TestValidateCapabilityDefaults(t *testing.T) {
	off, on := false, true
	cfg := &Config{
		Providers: map[string]*ProviderConfig{
			"p": {ID: "p", Type: "openai"},
		},
		Models: map[string]*ModelConfig{
			"p/legacy": {Provider: "p", ModelId: "legacy"}, // 全部 nil：老配置
			"p/vision": {Provider: "p", ModelId: "vision",
				SupportsImages: &on, SupportsTools: &off}, // 显式设置
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	legacy := cfg.Models["p/legacy"]
	if !legacy.ToolsEnabled() {
		t.Error("legacy model should default to tools enabled")
	}
	if legacy.ImagesEnabled() || legacy.ReasoningEnabled() {
		t.Error("legacy model should default to images/reasoning disabled")
	}
	if legacy.SupportsTools == nil || legacy.SupportsImages == nil || legacy.SupportsReasoning == nil {
		t.Error("Validate should normalize nil capability fields")
	}

	vision := cfg.Models["p/vision"]
	if !vision.ImagesEnabled() {
		t.Error("explicit SupportsImages=true should be preserved")
	}
	if vision.ToolsEnabled() {
		t.Error("explicit SupportsTools=false should be preserved")
	}
}

// Reasoning 配置：小写归一；合法档位放行；非法档位与越界 temperature 拒绝。
func TestValidateReasoningAndTemperature(t *testing.T) {
	newCfg := func(m *ModelConfig) *Config {
		return &Config{
			Providers: map[string]*ProviderConfig{"p": {ID: "p", Type: "openai"}},
			Models:    map[string]*ModelConfig{"p/m": m},
		}
	}

	// 大小写归一
	m := &ModelConfig{Provider: "p", ModelId: "m", ReasoningEffort: " High ", ReasoningSummary: "AUTO"}
	if err := newCfg(m).Validate(); err != nil {
		t.Fatalf("valid reasoning config rejected: %v", err)
	}
	if m.ReasoningEffort != "high" || m.ReasoningSummary != "auto" {
		t.Errorf("reasoning values not normalized: %q %q", m.ReasoningEffort, m.ReasoningSummary)
	}

	// 非法档位
	if err := newCfg(&ModelConfig{Provider: "p", ModelId: "m", ReasoningEffort: "hign"}).Validate(); err == nil {
		t.Error("typo effort should be rejected")
	}
	if err := newCfg(&ModelConfig{Provider: "p", ModelId: "m", ReasoningSummary: "verbose"}).Validate(); err == nil {
		t.Error("unknown summary should be rejected")
	}

	// temperature 边界
	temp := 0.7
	if err := newCfg(&ModelConfig{Provider: "p", ModelId: "m", Temperature: &temp}).Validate(); err != nil {
		t.Errorf("valid temperature rejected: %v", err)
	}
	bad := 2.5
	if err := newCfg(&ModelConfig{Provider: "p", ModelId: "m", Temperature: &bad}).Validate(); err == nil {
		t.Error("temperature > 2 should be rejected")
	}
}

// UpdateConfig 不得在持有 r.mu 时对同一互斥锁二次加锁（ResetHealth 死锁回归）：
// 清空模型的保存链路（SaveAppConfig → UpdateConfig）必须能完成。
func TestUpdateConfigNoDeadlock(t *testing.T) {
	r := NewManager(&Config{
		Active: "p/m",
		Providers: map[string]*ProviderConfig{
			"p": {ID: "p", Type: "openai"},
		},
		Models: map[string]*ModelConfig{
			"p/m": {EntryID: "p/m", Provider: "p", ModelId: "m"},
		},
	})

	empty := &Config{
		Providers: map[string]*ProviderConfig{},
		Models:    map[string]*ModelConfig{},
	}
	done := make(chan error, 1)
	go func() { done <- r.UpdateConfig(empty) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("UpdateConfig with empty models: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("UpdateConfig deadlocked (ResetHealth re-locks r.mu)")
	}

	// 清空后 Active() 报"尚未配置任何模型"，设置页可修复
	if _, _, err := r.Active(); err == nil {
		t.Fatal("Active should fail with zero models")
	}
	// 健康记录已清空
	if len(r.healthy) != 0 {
		t.Fatalf("healthy records should be reset, got %v", r.healthy)
	}
}
