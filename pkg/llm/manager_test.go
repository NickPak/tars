package llm

import (
	"context"
	"testing"
)

func TestF32p(t *testing.T) {
	if got := f32p(nil); got != nil {
		t.Errorf("f32p(nil) = %v, want nil", got)
	}
	v := 0.7
	got := f32p(&v)
	if got == nil || *got != float32(0.7) {
		t.Errorf("f32p(0.7) = %v", got)
	}
}

// 超界值在构建期收敛到供应商文档区间，而非报错。
func TestF32pClamped(t *testing.T) {
	if got := f32pClamped(nil, 1.0); got != nil {
		t.Errorf("f32pClamped(nil) = %v, want nil", got)
	}
	over := 1.8
	got := f32pClamped(&over, 1.0)
	if got == nil || *got != 1.0 {
		t.Errorf("f32pClamped(1.8, 1.0) = %v, want 1.0", got)
	}
	under := 0.5
	got = f32pClamped(&under, 1.0)
	if got == nil || *got != 0.5 {
		t.Errorf("f32pClamped(0.5, 1.0) = %v, want 0.5", got)
	}
}

// openai 构建冒烟：temperature/reasoning 参数路径不报错（构建不拨号，
// 仅需 BaseUrl；能力未声明时 effort 不下发的分支同样走通）。
func TestBuildOpenAIWithRequestDefaults(t *testing.T) {
	temp := 0.3
	on := true
	p := &ProviderConfig{ID: "p", Type: ProviderOpenAI, ApiKey: "sk-test", BaseUrl: "https://example.com/v1"}
	m := &ModelConfig{
		EntryID:           "p/m",
		Provider:          "p",
		ModelId:           "m",
		Temperature:       &temp,
		SupportsReasoning: &on,
		ReasoningEffort:   "high",
	}
	if _, err := buildOpenAI(context.Background(), p, m); err != nil {
		t.Fatalf("buildOpenAI with temperature+effort: %v", err)
	}

	// 未声明推理能力：effort 被门控忽略，构建仍成功
	m.SupportsReasoning = nil
	if _, err := buildOpenAI(context.Background(), p, m); err != nil {
		t.Fatalf("buildOpenAI with gated effort: %v", err)
	}
}
