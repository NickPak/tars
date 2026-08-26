package boot

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"tars/pkg/schema"
	"tars/pkg/tool/kernel"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"tars/pkg/mcp"
)

// McpProvider 是 mcp.MCPProvider 的实现（装配层桥接）：闭包捕获会话级
// tool.Registry，把命中的 MCP 工具包装为 Definition 动态注册进本会话
// （plan 3.6 D1 动态注册决策）。与 skillRuntime 同模式：接口在能力源
// 包定义，实现在装配层。
type McpProvider struct {
	mgr     *mcp.Manager
	toolReg *kernel.Registry // 会话级注册表（动态注册的归宿）

	// mu 保护 materialized（本会话已注册的 MCP 工具名集合，幂等与状态栏 loaded 区 tools: 集合的数据源）。
	mu     sync.Mutex
	loaded map[string]bool
}

var _ mcp.McpProvider = (*McpProvider)(nil)

// mcpConnectTimeout 是懒启动握手超时（npx 类启动器首次下载可能较慢，
// 但注册发生在轮内，必须有界）。
const mcpConnectTimeout = 60 * time.Second

// NewMCPProvider 创建会话级 MCP 通道；mgr 为 nil 时返回 nil（MCP 是可选能力，
// discover_tools 只检索技能）。
func NewMCPProvider(mgr *mcp.Manager, toolReg *kernel.Registry) *McpProvider {
	if mgr == nil {
		return nil
	}
	return &McpProvider{
		mgr:     mgr,
		toolReg: toolReg,
		loaded:  make(map[string]bool),
	}
}

func (r *McpProvider) Startup() error {
	return nil
}

func (r *McpProvider) Shutdown() error {
	return nil
}

func (r *McpProvider) GetSystemMessage() *schema.Message {
	return &schema.Message{
		Role:    schema.RoleSystem,
		Content: r.mgr.RenderIndex(),
	}
}

func (r *McpProvider) Search(query string, limit int) ([]mcp.ToolHit, error) {
	hits := r.mgr.Search(query, limit)
	out := make([]mcp.ToolHit, 0, len(hits))
	for _, h := range hits {
		out = append(out, mcp.ToolHit{
			Server:      h.Server,
			Name:        h.Name,
			FullName:    h.FullName,
			Description: h.Description,
			SourceType:  h.SourceType,
			InputSchema: h.InputSchema,
		})
	}
	return out, nil
}

// Materialize 命中即注册：幂等 → 懒启动进程 → 包装 Definition →
// 会话 Registry 注册。此后下一轮 Schemas() 自动带上该工具，模型可直接调用。
func (r *McpProvider) Materialize(hit mcp.ToolHit) error {
	r.mu.Lock()
	if r.loaded[hit.FullName] {
		r.mu.Unlock()
		return nil
	}
	r.mu.Unlock()

	// 懒启动服务器进程（D3 应用级池化：跨会话共享连接）。
	// ctx：注册发生在 discover_tools 执行期间，用轮级 ctx 约束握手超时；
	// 进程本身生命周期与应用同级（见 pool.go connect 注释）。
	ctx, cancel := context.WithTimeout(context.Background(), mcpConnectTimeout)
	defer cancel()
	if _, err := r.mgr.EnsureClient(ctx, hit.Server); err != nil {
		return err
	}

	// 包装为载体注册：handler 转发 MCP call（参数原文透传），
	// 文本结果提取回传；风险级别按服务器配置声明（审批门按声明拦截）。
	r.toolReg.Register(newMCPToolCarrier(r.mgr, hit, r.serverRiskLevel(hit.Server)))

	r.mu.Lock()
	r.loaded[hit.FullName] = true
	r.mu.Unlock()
	return nil
}

// Loaded 返回本会话已注册的 MCP 工具名（排序稳定）。
func (r *McpProvider) Loaded() []string {
	r.mu.Lock()
	out := make([]string, 0, len(r.loaded))
	for name := range r.loaded {
		out = append(out, name)
	}
	r.mu.Unlock()
	sort.Strings(out)
	return out
}

// mcpToolCarrier 是 MCP 物化工具的载体（Carrier）：持有转发所需的
// 服务器连接管理器与工具坐标。连接资源归进程级 mcp.Manager 池化持有，
// 不属载体，Close 为空方法。
type mcpToolCarrier struct {
	mgr  *mcp.Manager
	hit  mcp.ToolHit
	risk kernel.RiskLevel
}

func newMCPToolCarrier(mgr *mcp.Manager, hit mcp.ToolHit, risk kernel.RiskLevel) *mcpToolCarrier {
	return &mcpToolCarrier{mgr: mgr, hit: hit, risk: risk}
}

// Definitions 实现 tool.Carrier：把命中的 MCP 工具包装为 Definition
// （handler 转发 JSON-RPC call，参数原文透传，文本结果提取回传）。
func (c *mcpToolCarrier) Definitions() []*kernel.Definition {
	server, toolName := c.hit.Server, c.hit.Name
	return []*kernel.Definition{{
		Name:        c.hit.FullName,
		Description: mcpToolDescription(c.hit),
		Parameters:  c.hit.InputSchema,
		Risk:        c.risk,
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			res, err := c.mgr.CallTool(ctx, server, toolName, raw)
			if err != nil {
				return "", err
			}
			return callResultText(res), nil
		},
	}}
}

// Close 实现 tool.Carrier：无载体自有资源。
func (c *mcpToolCarrier) Close() error { return nil }

// serverRiskLevel 读取服务器配置的风险声明，映射到 tool.RiskLevel；
// 未配置/未知服务器回退 medium（失败安全默认值：宁可多审批一次）。
func (r *McpProvider) serverRiskLevel(server string) kernel.RiskLevel {
	srv := r.mgr.Server(server)
	if srv == nil {
		return kernel.RiskLevelMedium
	}
	switch srv.Risk {
	case mcp.RiskLow:
		return kernel.RiskLevelLow
	case mcp.RiskHigh:
		return kernel.RiskLevelHigh
	default:
		return kernel.RiskLevelMedium
	}
}

// mcpToolDescription 生成注册给模型的工具描述：来源服务器 + 原始描述。
// description 是服务器的不可信输入（3.3 审查清单），前缀标注来源使
// 模型能识别其外部性质。
func mcpToolDescription(hit mcp.ToolHit) string {
	if hit.Description == "" {
		return fmt.Sprintf("[MCP server: %s] %s", hit.Server, hit.Name)
	}
	return fmt.Sprintf("[MCP server: %s] %s", hit.Server, hit.Description)
}

// callResultText 提取 MCP 调用结果的文本内容（多个 TextContent 拼接）。
func callResultText(res *mcpsdk.CallToolResult) string {
	var parts []string
	for _, c := range res.Content {
		if tc, ok := c.(*mcpsdk.TextContent); ok {
			parts = append(parts, tc.Text)
		}
	}
	return strings.Join(parts, "\n")
}
