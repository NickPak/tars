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
