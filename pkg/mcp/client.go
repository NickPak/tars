package mcp

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// withServer 是"拉起服务器进程→连接→执行 fn→关闭"的一次性连接原语：
// 探测（Probe）与后续懒启动连接池（#4）共用同一条拉起路径。
// env 支持 ${VAR} 展开（在子进程环境中按当前进程环境替换）。
func withServer(ctx context.Context, name string, cfg *ServerConfig, fn func(*mcp.ClientSession) error) error {
	cmd := exec.CommandContext(ctx, cfg.Command, cfg.Args...)
	if len(cfg.Env) > 0 {
		env := os.Environ()
		for k, v := range cfg.Env {
			env = append(env, k+"="+os.ExpandEnv(v))
		}
		cmd.Env = env
	}

	transport := &mcp.CommandTransport{Command: cmd}
	client := mcp.NewClient(&mcp.Implementation{
		Name:    "tars",
		Version: "v0.1.0",
	}, nil)
	cs, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return fmt.Errorf("mcp: server %q: connect: %w", name, err)
	}
	defer cs.Close()

	return fn(cs)
}

// listToolInfos 抓取服务器的完整工具清单（翻页安全：MCP 协议支持分页，
// 当前按单页实现——服务器工具量通常在个位数到几十个）。
func listToolInfos(ctx context.Context, cs *mcp.ClientSession) ([]*ToolInfo, error) {
	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		return nil, err
	}
	infos := make([]*ToolInfo, 0, len(res.Tools))
	for _, t := range res.Tools {
		var schema map[string]any
		switch s := t.InputSchema.(type) {
		case map[string]any:
			schema = s
		case nil:
			// 无参数工具
		default:
			// AddTool 生成的结构体等形式：经 JSON 归一化为 map
			var m map[string]any
			if err := remarshalJSON(s, &m); err == nil {
				schema = m
			}
		}
		infos = append(infos, &ToolInfo{
			Name:        t.Name,
			Description: strings.TrimSpace(t.Description),
			InputSchema: schema,
		})
	}
	return infos, nil
}
