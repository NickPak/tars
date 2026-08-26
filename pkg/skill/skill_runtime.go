package skill

// SkillSummary 是一次检索命中的技能摘要（discover_tools 返回用）。
type SkillSummary struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Category    string `json:"category"`
}

// SkillProvider 是 load_skill / discover_tools 工具与状态栏所需的宿主注入能力。
type SkillProvider interface {
	// Load 返回指定 Skill 的 SKILL.md 全文。
	Load(name string) (string, error)
	// IsLoaded / MarkLoaded 管理会话级"已加载"幂等状态。
	IsLoaded(name string) bool
	MarkLoaded(name string)
	// Loaded 返回已加载技能名（排序后），供状态栏展示。
	Loaded() []string
	// Search 按自然语言需求检索技能（BM25），返回候选；无命中返回空。
	Search(query string, limit int) ([]SkillSummary, error)
	// SearchLimit 是检索返回的候选数上限（配置驱动；discover_tools 与
	// 设置页搜索共用，保证"页面所见 = 模型所得"）。
	SearchLimit() int
}
