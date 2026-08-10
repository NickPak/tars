package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tars/pkg/llm"
)

const sampleYAML = `# 顶部注释
llm:
  active: gemini/gemini-3.1-flash-lite
  providers:
    - id: gemini
      type: gemini
      # 用环境变量注入密钥
      apiKey: ${TARS_API_KEY}
      timeout: 60s
  models:
    - id: gemini/gemini-3.1-flash-lite
      provider: gemini
      modelId: gemini-3.1-flash-lite
      inputPricePerMillion: 0.075

# agent 段注释
agent:
  maxIterations: 100

trace:
  otlpGrpcEndpoint: "localhost:4317"
`

func TestSaveAppConfigFileMerge(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(sampleYAML), 0644); err != nil {
		t.Fatal(err)
	}

	budget := int32(-1)
	cfg := &AppConfig{
		LLM: &llm.Config{
			Active: "gemini/gemini-3.6-flash",
			Providers: []llm.ProviderConfig{
				// ApiKey 留空 → 必须保留文件中的 ${TARS_API_KEY} 引用
				{ID: "gemini", Type: "gemini", Timeout: "2m0s"},
				{ID: "deepseek", Type: "openai", ApiKey: "sk-new", BaseUrl: "https://api.deepseek.com/v1"},
			},
			Models: []llm.ModelConfig{
				{ID: "gemini/gemini-3.6-flash", Provider: "gemini", ModelId: "gemini-3.6-flash",
					ContextWindow: 1048576, InputPricePerMillion: 0.075, ThinkingBudget: &budget},
				{ID: "deepseek/deepseek-chat", Provider: "deepseek", ModelId: "deepseek-chat"},
			},
		},
		Agent: &AgentConfig{MaxIterations: 50, CompressionThreshold: 0.8},
		Trace: &TraceConfig{OTLPHTTPEndpoint: "localhost:4318"}, // gRPC 留空 → 删除
	}
	if err := SaveAppConfigFile(path, cfg); err != nil {
		t.Fatalf("SaveAppConfigFile: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := string(data)

	// llm 段之外的注释与结构保留
	for _, want := range []string{"# 顶部注释", "# agent 段注释", "maxIterations: 50", "compressionThreshold: 0.8"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q preserved:\n%s", want, out)
		}
	}
	// apiKey 引用保留（按供应商 ID 从文件回填）
	if !strings.Contains(out, "apiKey: ${TARS_API_KEY}") {
		t.Errorf("apiKey env ref lost:\n%s", out)
	}
	// 新结构写入
	for _, want := range []string{"active: gemini/gemini-3.6-flash", "type: openai",
		"baseUrl: https://api.deepseek.com/v1", "thinkingBudget: -1", "otlpHttpEndpoint: localhost:4318"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in:\n%s", want, out)
		}
	}
	// 0 值删除
	if strings.Contains(out, "otlpGrpcEndpoint") {
		t.Errorf("otlpGrpcEndpoint should be deleted:\n%s", out)
	}

	// 写回结果仍可被 LoadAppConfig 解析（apiKey 环境变量展开）
	t.Setenv("TARS_API_KEY", "secret")
	loaded, err := LoadAppConfig(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if loaded.LLM.Active != "gemini/gemini-3.6-flash" {
		t.Errorf("active mismatch: %q", loaded.LLM.Active)
	}
	g := loaded.LLM.FindProvider("gemini")
	if g == nil || g.ApiKey != "secret" || g.Timeout != "2m0s" {
		t.Errorf("gemini provider mismatch: %+v", g)
	}
	m := loaded.LLM.FindModel("gemini/gemini-3.6-flash")
	if m == nil || m.ThinkingBudget == nil || *m.ThinkingBudget != -1 || m.ContextWindow != 1048576 {
		t.Errorf("model entry mismatch: %+v", m)
	}
	if loaded.Agent.MaxIterations != 50 || loaded.Trace.OTLPGrpcEndpoint != "" {
		t.Errorf("agent/trace mismatch: %+v %+v", loaded.Agent, loaded.Trace)
	}
}
