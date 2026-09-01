package agent

import "time"

const (
	DefaultMaxIterations          = 100
	DefaultCompressionThreshold   = 0.8
	DefaultCompressionKeepTurns   = 6
	DefaultCompressionMinBatch    = 8
	DefaultIterationTimeout       = 120 * time.Second
	DefaultCompressionMaxFailures = 3
)

type Config struct {
	MaxIterations          int           `yaml:"maxIterations,omitempty" json:"maxIterations,omitempty"`
	CompressionThreshold   float64       `yaml:"compressionThreshold,omitempty" json:"compressionThreshold,omitempty"`
	CompressionKeepTurns   int           `yaml:"compressionKeepTurns,omitempty" json:"compressionKeepTurns,omitempty"`
	CompressionMinBatch    int           `yaml:"compressionMinBatch,omitempty" json:"compressionMinBatch,omitempty"`
	CompressionMaxFailures int           `yaml:"compressionMaxFailures,omitempty" json:"compressionMaxFailures,omitempty"`
	IterationTimeout       time.Duration `yaml:"iterationTimeout,omitempty" json:"iterationTimeout,omitempty"`
}

func (c *Config) Validate() {
	if c.MaxIterations <= 0 {
		c.MaxIterations = DefaultMaxIterations
	}
	if c.CompressionThreshold <= 0 || c.CompressionThreshold > 1 {
		c.CompressionThreshold = DefaultCompressionThreshold
	}
	if c.CompressionKeepTurns <= 0 {
		c.CompressionKeepTurns = DefaultCompressionKeepTurns
	}
	if c.CompressionMinBatch <= 0 {
		c.CompressionMinBatch = DefaultCompressionMinBatch
	}
	if c.CompressionMaxFailures <= 0 {
		c.CompressionMaxFailures = DefaultCompressionMaxFailures
	}
	if c.IterationTimeout <= 0 {
		c.IterationTimeout = DefaultIterationTimeout
	}
}
