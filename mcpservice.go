package main

import (
	"context"
	"fmt"
	"strings"
	"tars/internal/boot"
	"tars/pkg/mcp"
	"time"
)

// MCPService —— MCP 服务器管理（设置页 MCP 页签）。
type MCPService struct{}

// ---- MCP 服务器管理（设置页 MCP 页签） ----
// 与技能同生命周期：配置由 mcp.Manager 在 <workDir>/mcp/servers.yaml 自管
// 读写，以下变更操作即改即存（不经 AppConfig draft 保存流）。

// ListMCPServers 返回全部已配置 MCP 服务器（含禁用项与工具计数）。
func (s *MCPService) ListMCPServers() ([]*mcp.ServerInfo, error) {
	st := boot.GetApp().GetMCPMgr()
	if st == nil {
		return nil, fmt.Errorf("mcp store not initialized")
	}
	return st.List(), nil
}

// SearchMCPTools 按自然语言检索启用服务器的工具——与 discover_tools 同款
// bleve 检索和候选数上限，页面所见 = 模型所得。空查询返回全部启用服务器
// 的工具（等价于模型的全集视图）。
func (s *MCPService) SearchMCPTools(query string) ([]*mcp.ToolHit, error) {
	st := boot.GetApp().GetMCPMgr()
	if st == nil {
		return nil, fmt.Errorf("mcp store not initialized")
	}
	limit := 5
	if sk := boot.GetApp().GetSkillMgr(); sk != nil {
		limit = sk.GetConfig().DiscoverResultLimit // 与 discover_tools 同上限
	}
	query = strings.TrimSpace(query)
	if query == "" {
		// 空查询：展开全部启用服务器的工具缓存
		var all []*mcp.ToolHit
		for _, srv := range st.Enabled() {
			for _, ti := range st.Tools(srv.Name) {
				all = append(all, &mcp.ToolHit{
					Server:      srv.Name,
					Name:        ti.Name,
					FullName:    mcp.FullToolName(srv.Name, ti.Name),
					Description: ti.Description,
					SourceType:  srv.SourceType,
				})
			}
		}
		return all, nil
	}
	hits := st.Search(query, limit)
	out := make([]*mcp.ToolHit, 0, len(hits))
	for i := range hits {
		out = append(out, &hits[i])
	}
	return out, nil
}

// UpsertMCPServer 登记/覆盖一个 MCP 服务器（立即落盘生效；
// 覆盖既有服务器时其运行中连接即回收，下次调用按新配置懒重启）。
func (s *MCPService) UpsertMCPServer(name string, cfg *mcp.ServerConfig) error {
	st := boot.GetApp().GetMCPMgr()
	if st == nil {
		return fmt.Errorf("mcp store not initialized")
	}
	return st.UpsertServer(name, cfg)
}

// RemoveMCPServer 移除一个 MCP 服务器（立即落盘生效；连接即回收，
// 探测缓存同步清理）。
func (s *MCPService) RemoveMCPServer(name string) error {
	st := boot.GetApp().GetMCPMgr()
	if st == nil {
		return fmt.Errorf("mcp store not initialized")
	}
	return st.RemoveServer(name)
}

// SetMCPServerEnabled 启用/禁用服务器（立即落盘生效；禁用后对 Agent
// 不可见——索引/检索/连接排除，连接即回收，配置与探测缓存保留）。
func (s *MCPService) SetMCPServerEnabled(name string, enabled bool) error {
	st := boot.GetApp().GetMCPMgr()
	if st == nil {
		return fmt.Errorf("mcp store not initialized")
	}
	return st.SetEnabled(name, enabled)
}

// ProbeMCPServer 探测一个已启用服务器：拉起进程抓取工具清单并缓存
// （此后会话启动零进程，discover_tools 用缓存检索）。
// 服务器须已配置且启用；60s 超时（npx 类启动器首次下载可能较慢）。
func (s *MCPService) ProbeMCPServer(name string) error {
	st := boot.GetApp().GetMCPMgr()
	if st == nil {
		return fmt.Errorf("mcp store not initialized")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	return st.Probe(ctx, name)
}

// ListMCPTools 返回一个服务器的缓存工具清单（未探测返回空）。
func (s *MCPService) ListMCPTools(server string) ([]*mcp.ToolInfo, error) {
	st := boot.GetApp().GetMCPMgr()
	if st == nil {
		return nil, fmt.Errorf("mcp store not initialized")
	}
	return st.Tools(server), nil
}
