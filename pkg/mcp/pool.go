package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// clientHandle 是一个服务器连接槽（singleflight）：
// 首个调用方创建句柄入池后在锁外建立连接，并发调用方等待同一 ready；
// 建立成功句柄常驻池中复用，失败时 err 记录、句柄移出池（下次调用重试）。
type clientHandle struct {
	session *mcpsdk.ClientSession
	err     error
	ready   chan struct{}
}

// EnsureClient 返回指定服务器的运行中连接；首次调用时拉起进程（懒启动，
// D3 应用级池化：连接跨会话共享，应用退出统一关闭）。
// 服务器必须已配置且启用；连接失败不缓存（下次调用重新拉起）。
// 装配层（internal/boot 的动态注册桥接）在注册 MCP 工具前调用它以预拉进程。
func (m *Manager) EnsureClient(ctx context.Context, name string) (*mcpsdk.ClientSession, error) {
	cfg := m.GetConfig()
	if cfg == nil {
		return nil, fmt.Errorf("mcp: no config")
	}
	srv, ok := cfg.Servers[name]
	if !ok {
		return nil, fmt.Errorf("mcp: server %q not configured", name)
	}
	if !srv.Enabled {
		return nil, fmt.Errorf("mcp: server %q is disabled", name)
	}

	m.mu.Lock()
	h, ok := m.clients[name]
	if ok {
		m.mu.Unlock()
		<-h.ready
		if h.err != nil {
			return nil, h.err
		}
		return h.session, nil
	}
	h = &clientHandle{ready: make(chan struct{})}
	m.clients[name] = h
	m.mu.Unlock()

	// 连接建立放在锁外：子进程拉起与握手可能耗时（如 npx 首次下载），
	// 不阻塞其他服务器的并发懒启动。
	session, err := m.connect(ctx, name, srv)
	h.session = session
	h.err = err
	close(h.ready)
	if err != nil {
		// 失败不入池（仅当句柄仍是本次创建的——Close 可能已先行移除）
		m.mu.Lock()
		if m.clients[name] == h {
			delete(m.clients, name)
		}
		m.mu.Unlock()
		return nil, err
	}
	return session, nil
}

// connect 拉起服务器进程并完成 initialize 握手。
// 进程生命周期与应用同级（D3：连接池跨会话共享）：命令不绑定轮级 ctx——
// 轮取消不能杀死共享进程；ctx 只约束握手超时，超时时确保子进程被回收。
func (m *Manager) connect(ctx context.Context, name string, cfg *ServerConfig) (*mcpsdk.ClientSession, error) {
	cmd := exec.Command(cfg.Command, cfg.Args...)
	if len(cfg.Env) > 0 {
		env := os.Environ()
		for k, v := range cfg.Env {
			env = append(env, k+"="+os.ExpandEnv(v))
		}
		cmd.Env = env
	}

	transport := &mcpsdk.CommandTransport{Command: cmd}
	client := mcpsdk.NewClient(&mcpsdk.Implementation{
		Name:    "tars",
		Version: "v0.1.0",
	}, nil)
	cs, err := client.Connect(ctx, transport, nil)
	if err != nil {
		// 握手失败：进程可能已拉起但不可用，防御性回收
		// （Start 失败时 cmd.Process 为 nil，Kill 安全跳过）。
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		return nil, fmt.Errorf("mcp: server %q: connect: %w", name, err)
	}
	return cs, nil
}

// CallTool 调用指定服务器的工具（经懒启动连接）。rawArgs 是模型输出的
// 参数原文（JSON），空值按无参处理。结果是 SDK 原样视图——文本提取与
// 错误包装由调用方（#7 的 Definition handler）负责。
func (m *Manager) CallTool(ctx context.Context, serverName, toolName string, rawArgs json.RawMessage) (*mcpsdk.CallToolResult, error) {
	cs, err := m.EnsureClient(ctx, serverName)
	if err != nil {
		return nil, err
	}
	var args map[string]any
	if len(rawArgs) > 0 {
		if err := json.Unmarshal(rawArgs, &args); err != nil {
			return nil, fmt.Errorf("mcp: server %q tool %q: invalid arguments: %w", serverName, toolName, err)
		}
	}
	return cs.CallTool(ctx, &mcpsdk.CallToolParams{Name: toolName, Arguments: args})
}

// Close 关闭并移除一个服务器的连接（GUI 禁用/删除服务器、配置变更回收用）。
// 幂等：连接不存在时不做任何事。
func (m *Manager) Close(name string) {
	m.mu.Lock()
	h, ok := m.clients[name]
	if ok {
		delete(m.clients, name)
	}
	m.mu.Unlock()
	if !ok {
		return
	}
	<-h.ready // 等建立中的连接落定，避免漏关进程
	if h.session != nil {
		_ = h.session.Close()
	}
}

// CloseAll 关闭全部连接（应用退出时调用，D3 统一关闭）。
func (m *Manager) CloseAll() {
	m.mu.Lock()
	handles := m.clients
	m.clients = make(map[string]*clientHandle)
	m.mu.Unlock()
	for _, h := range handles {
		<-h.ready
		if h.session != nil {
			_ = h.session.Close()
		}
	}
}
