package main

import (
	"context"
	"fmt"
	"tars/pkg/mcp"
	"time"
)

// ---- MCP 服务器管理（设置页 MCP 页签） ----
// 与技能同生命周期：配置由 mcp.Manager 在 <workDir>/mcp/servers.yaml 自管
// 读写，以下变更操作即改即存（不经 AppConfig draft 保存流）。

// ListMCPServers 返回全部已配置 MCP 服务器（含禁用项与工具计数）。
func (s *AgentService) ListMCPServers() ([]*mcp.ServerInfo, error) {
	st := s.app.GetMCPMgr()
	if st == nil {
		return nil, fmt.Errorf("mcp store not initialized")
	}
	return st.List(), nil
}

// UpsertMCPServer 登记/覆盖一个 MCP 服务器（立即落盘生效；
// 覆盖既有服务器时其运行中连接即回收，下次调用按新配置懒重启）。
func (s *AgentService) UpsertMCPServer(name string, cfg *mcp.ServerConfig) error {
	st := s.app.GetMCPMgr()
	if st == nil {
		return fmt.Errorf("mcp store not initialized")
	}
	return st.UpsertServer(name, cfg)
}

// RemoveMCPServer 移除一个 MCP 服务器（立即落盘生效；连接即回收，
// 探测缓存同步清理）。
func (s *AgentService) RemoveMCPServer(name string) error {
	st := s.app.GetMCPMgr()
	if st == nil {
		return fmt.Errorf("mcp store not initialized")
	}
	return st.RemoveServer(name)
}

// SetMCPServerEnabled 启用/禁用服务器（立即落盘生效；禁用后对 Agent
// 不可见——索引/检索/连接排除，连接即回收，配置与探测缓存保留）。
func (s *AgentService) SetMCPServerEnabled(name string, enabled bool) error {
	st := s.app.GetMCPMgr()
	if st == nil {
		return fmt.Errorf("mcp store not initialized")
	}
	return st.SetEnabled(name, enabled)
}

// ProbeMCPServer 探测一个已启用服务器：拉起进程抓取工具清单并缓存
// （此后会话启动零进程，discover_tools 用缓存检索）。
// 服务器须已配置且启用；60s 超时（npx 类启动器首次下载可能较慢）。
func (s *AgentService) ProbeMCPServer(name string) error {
	st := s.app.GetMCPMgr()
	if st == nil {
		return fmt.Errorf("mcp store not initialized")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	return st.Probe(ctx, name)
}

// ListMCPTools 返回一个服务器的缓存工具清单（未探测返回空）。
func (s *AgentService) ListMCPTools(server string) ([]*mcp.ToolInfo, error) {
	st := s.app.GetMCPMgr()
	if st == nil {
		return nil, fmt.Errorf("mcp store not initialized")
	}
	return st.Tools(server), nil
}
