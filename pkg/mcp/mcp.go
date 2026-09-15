package mcp

// ToolHit 是 MCP 工具检索的命中视图（discover_tools 返回用）。
type ToolHit struct {
	Server      string         `json:"server"`
	Name        string         `json:"name"`
	FullName    string         `json:"fullName"` // mcp__<server>__<tool>
	Description string         `json:"description,omitempty"`
	SourceType  string         `json:"sourceType,omitempty"`
	InputSchema map[string]any `json:"inputSchema,omitempty"`
}

// StateProvider 是"已加载"幂等集合的会话级读写面（session.Manager
// 天然满足，构造 Runtime 时注入）。
type StateProvider interface {
	IsToolLoaded(fullName string) bool
	MarkToolLoaded(fullName string)
	UnmarkToolLoaded(fullName string)
	GetLoadedTools() []string
}

// Provider 是 discover_tools 的 MCP 能力通道（读 + 物化一体，宿主侧
// 实现见 runtime.go；tools 包只依赖本接口）。
type Provider interface {
	// Search 在启用服务器的工具缓存中按自然语言需求检索（与技能检索
	// 共用同一 bleve 引擎与候选数上限）。
	Search(query string, limit int) ([]ToolHit, error)
	// Materialize 把命中的 MCP 工具注册进本会话的工具集（懒启动服务器
	// 进程、包装 Definition、会话 Registry 注册）；此后模型可直接按
	// FullName 调用。内部幂等：重复调用立即返回，无副作用。
	Materialize(hit ToolHit) error

	IsToolLoaded(fullName string) bool
	MarkToolLoaded(fullName string)
	GetLoadedTools() []string
}
