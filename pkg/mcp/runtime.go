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

// McpProvider 是 discover_tools 的 MCP 能力通道（宿主在 internal/boot 装配，
// 闭包捕获会话 Registry 与 mcp.Manager；tools 包只依赖本接口）。
// 与 skills.SkillProvider 同模式：接口在能力源包定义，实现在装配层桥接。
type McpProvider interface {
	// Search 在启用服务器的工具缓存中按自然语言需求检索（与技能检索
	// 共用同一 BM25 引擎与候选数上限）。
	Search(query string, limit int) ([]ToolHit, error)
	// Materialize 把命中的 MCP 工具注册进本会话的工具集（懒启动服务器
	// 进程、包装 Definition、会话 Registry 注册）；此后模型可直接按
	// FullName 调用。内部幂等：重复调用立即返回，无副作用。
	Materialize(hit ToolHit) error
	// Loaded 返回本会话已注册的 MCP 工具名（状态栏 loaded 区 tools: 集合的数据源）。
	Loaded() []string
}
