package trace

type Config struct {
	Enabled          bool   `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	OTLPHTTPEndpoint string `yaml:"otlpHttpEndpoint,omitempty" json:"otlpHttpEndpoint,omitempty"`
	OTLPGrpcEndpoint string `yaml:"otlpGrpcEndpoint,omitempty" json:"otlpGrpcEndpoint,omitempty"`
}
