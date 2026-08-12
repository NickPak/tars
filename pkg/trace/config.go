package trace

// NewTracerForSession 按当前全局追踪配置创建新 tracer
// TraceConfig controls OpenTelemetry span export.
// Spans are only exported to the configured OTLP collectors; there is no
// local file sink. Enabled=false (the default when absent) disables all
// tracing regardless of configured endpoints.
type TraceConfig struct {
	// Enabled is the master switch for tracing. Absent/false = 不产生任何 span。
	Enabled bool `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	// OTLPHTTPEndpoint exports spans to an OTLP/HTTP collector
	// (e.g. "localhost:4318" for Jaeger). Empty disables this exporter.
	OTLPHTTPEndpoint string `yaml:"otlpHttpEndpoint,omitempty" json:"otlpHttpEndpoint,omitempty"`
	// OTLPGrpcEndpoint exports spans to an OTLP/gRPC collector
	// (e.g. "localhost:4317" for Arize Phoenix). Empty disables this exporter.
	OTLPGrpcEndpoint string `yaml:"otlpGrpcEndpoint,omitempty" json:"otlpGrpcEndpoint,omitempty"`
}
