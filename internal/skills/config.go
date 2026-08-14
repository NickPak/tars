package skills

const (
	DefaultTierFullMax     = 50
	DefaultTierResidentMax = 500
)

type Config struct {
	TierFullMax     int `yaml:"tierFullMax,omitempty" json:"tierFullMax,omitempty"`
	TierResidentMax int `yaml:"tierResidentMax,omitempty" json:"tierResidentMax,omitempty"`
}

// Validate 修正非法字段为默认值。
func (c *Config) Validate() {
	if c.TierFullMax <= 0 {
		c.TierFullMax = DefaultTierFullMax
	}
	if c.TierResidentMax <= 0 || c.TierResidentMax < c.TierFullMax {
		c.TierResidentMax = max(DefaultTierResidentMax, c.TierFullMax)
	}
}
