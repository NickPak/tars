package skill

// Summary 是一次检索命中的技能摘要（discover_tools 返回用）。
type Summary struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Category    string `json:"category"`
}

// StateProvider 是"已加载"幂等集合的会话级读写面（session.Manager
// 天然满足，构造 Runtime 时注入）。
type StateProvider interface {
	IsSkillLoaded(name string) bool
	MarkSkillLoaded(name string)
	GetLoadedSkills() []string
}

// Provider 是技能工具（load_skill / write_skill / discover_tools）与
// 状态栏所需的宿主注入能力（读 + 写一体，宿主侧实现见 runtime.go）。
type Provider interface {
	StateProvider
	// Load 返回指定 Skill 的 SKILL.md 全文。
	Load(name string) (string, error)
	// Search 按自然语言需求检索技能（BM25），返回候选；无命中返回空。
	Search(query string, limit int) ([]Summary, error)
	// SearchLimit 是检索返回的候选数上限（配置驱动；discover_tools 与
	// 设置页搜索共用，保证"页面所见 = 模型所得"）。
	SearchLimit() int
	// WriteSkill 创建或覆盖一个技能（新建默认禁用；覆盖保留启用状态）；
	// created 表示本次是新建。审批由框架在执行层拦截（工具声明
	// RiskRules），本方法假定调用方已过审批门。
	WriteSkill(name, description, category, body string, overwrite bool) (created bool, err error)
}
