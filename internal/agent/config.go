package agent

import "time"

const (
	DefaultMaxIterations        = 100
	DefaultCompressionThreshold = 0.8
	DefaultIterationTimeout     = 120 * time.Second
)

type Config struct {
	MaxIterations        int           `yaml:"maxIterations,omitempty" json:"maxIterations,omitempty"`
	CompressionThreshold float64       `yaml:"compressionThreshold,omitempty" json:"compressionThreshold,omitempty"`
	IterationTimeout     time.Duration `yaml:"iterationTimeout,omitempty" json:"iterationTimeout,omitempty"`
}

func (c *Config) Validate() {
	if c.MaxIterations <= 0 {
		c.MaxIterations = DefaultMaxIterations
	}
	if c.CompressionThreshold <= 0 || c.CompressionThreshold > 1 {
		c.CompressionThreshold = DefaultCompressionThreshold
	}
	if c.IterationTimeout <= 0 {
		c.IterationTimeout = DefaultIterationTimeout
	}
}
