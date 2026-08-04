package config

import (
	"tars/pkg/llm"
	"os"

	"gopkg.in/yaml.v3"
)

// TraceConfig controls OpenTelemetry span export.
// Local per-conversation trace.jsonl is always written; OTLP exporters
// are optional mirrors (both can be enabled at the same time).
type TraceConfig struct {
	// OTLPHTTPEndpoint mirrors spans to an OTLP/HTTP collector
	// (e.g. "localhost:4318" for Jaeger). Empty disables it.
	OTLPHTTPEndpoint string `yaml:"otlpHttpEndpoint,omitempty" json:"otlpHttpEndpoint,omitempty"`
	// OTLPGrpcEndpoint mirrors spans to an OTLP/gRPC collector
	// (e.g. "localhost:4317" for Arize Phoenix). Empty disables it.
	OTLPGrpcEndpoint string `yaml:"otlpGrpcEndpoint,omitempty" json:"otlpGrpcEndpoint,omitempty"`
}

type AppConfig struct {
	LLM     *llm.Options `yaml:"llm,omitempty" json:"llm,omitempty"`
	WorkDir string       `yaml:"workDir,omitempty" json:"workDir,omitempty"`
	Trace   *TraceConfig `yaml:"trace,omitempty" json:"trace,omitempty"`
}

// OTLPEndpoints returns the configured OTLP/HTTP and OTLP/gRPC endpoints
// ("" when unset).
func (c *AppConfig) OTLPEndpoints() (httpEndpoint, grpcEndpoint string) {
	if c == nil || c.Trace == nil {
		return "", ""
	}
	return c.Trace.OTLPHTTPEndpoint, c.Trace.OTLPGrpcEndpoint
}

func LoadAppConfig(path string) (*AppConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// 展开 ${VAR} / $VAR 形式的环境变量引用，使密钥等敏感值
	// 可以通过环境变量注入而不必写入配置文件
	expanded := os.ExpandEnv(string(data))
	appConfig := &AppConfig{}
	if err = yaml.Unmarshal([]byte(expanded), appConfig); err != nil {
		return nil, err
	}
	return appConfig, nil
}
