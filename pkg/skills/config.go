package skills

const (
	DefaultTierFullMax         = 50
	DefaultTierResidentMax     = 500
	DefaultDiscoverResultLimit = 5
)

type Config struct {
	TierFullMax     int `yaml:"tierFullMax,omitempty" json:"tierFullMax,omitempty"`
	TierResidentMax int `yaml:"tierResidentMax,omitempty" json:"tierResidentMax,omitempty"`
	// DiscoverResultLimit 是模糊检索返回的候选数上限：
	// discover_tools（模型侧）与设置页搜索（用户侧）共用同一值。
	DiscoverResultLimit int `yaml:"discoverResultLimit,omitempty" json:"discoverResultLimit,omitempty"`
}

func NewConfig() *Config {
	return &Config{
		TierFullMax:         DefaultTierFullMax,
		TierResidentMax:     DefaultTierResidentMax,
		DiscoverResultLimit: DefaultDiscoverResultLimit,
	}
}

// Validate 修正非法字段为默认值。
func (c *Config) Validate() {
	if c.TierFullMax <= 0 {
		c.TierFullMax = DefaultTierFullMax
	}
	if c.TierResidentMax <= 0 || c.TierResidentMax < c.TierFullMax {
		c.TierResidentMax = max(DefaultTierResidentMax, c.TierFullMax)
	}
	if c.DiscoverResultLimit <= 0 {
		c.DiscoverResultLimit = DefaultDiscoverResultLimit
	}
}
