package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tars/pkg/llm"
)

// 复现"清空供应商与模型后保存"：SaveAppConfigFile 对空 llm 段必须完成写入，
// 且旧文件中的 providers/models 条目被整段清掉（llm 段是整段替换）。
func TestSaveAppConfigFileWithEmptyLLM(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	old := AppConfigPath
	AppConfigPath = path
	defer func() { AppConfigPath = old }()

	// 存量文件：有供应商、模型与 active（模拟用户清空前后的文件状态）
	existing := `llm:
  active: gemini/gemini-3.1-flash-lite
  providers:
    gemini:
      id: gemini
      type: gemini
      apiKey: ${GEMINI_KEY}
  models:
    gemini/gemini-3.1-flash-lite:
      provider: gemini
      modelId: gemini-3.1-flash-lite
agent:
  maxIterations: 100
`
	if err := os.WriteFile(path, []byte(existing), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := &AppConfig{
		LLM: &llm.Config{
			Active:    "",
			Providers: map[string]*llm.ProviderConfig{},
			Models:    map[string]*llm.ModelConfig{},
		},
	}
	cfg.Validate() // 生产链路：SaveAppConfig 先 Validate

	done := make(chan error, 1)
	go func() { done <- SaveAppConfigFile(cfg) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("SaveAppConfigFile: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SaveAppConfigFile hung with empty llm")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	// 旧条目必须被清掉；llm 段保留为空映射
	for _, s := range []string{"gemini-3.1-flash-lite", "GEMINI_KEY"} {
		if strings.Contains(text, s) {
			t.Fatalf("old entry %q should be wiped, file:\n%s", s, text)
		}
	}
	// agent 段（键级合并）应保留
	if !strings.Contains(text, "maxIterations") {
		t.Fatalf("agent section should survive, file:\n%s", text)
	}
}
