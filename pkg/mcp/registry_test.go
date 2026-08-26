package mcp

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestRegistry_SaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	reg := NewRegistry(dir)
	reg.Servers["demo"] = &ServerTools{
		ProbedAt: "2026-08-19 16:00:00",
		Tools: []*ToolInfo{
			{Name: "echo", Description: "echo back", InputSchema: map[string]any{"type": "object"}},
			{Name: "add"},
		},
	}
	if err := reg.Save(); err != nil {
		t.Fatal(err)
	}

	fresh := NewRegistry(dir)
	if err := fresh.Load(); err != nil {
		t.Fatal(err)
	}
	if fresh.Version != cacheVersion {
		t.Errorf("Version = %d, want %d", fresh.Version, cacheVersion)
	}
	st, ok := fresh.FindServer("demo")
	if !ok || len(st.Tools) != 2 || st.Tools[0].Name != "echo" {
		t.Fatalf("round trip mismatch: %+v", fresh.Servers)
	}
	if st.Tools[0].InputSchema["type"] != "object" {
		t.Errorf("InputSchema not preserved: %+v", st.Tools[0].InputSchema)
	}

	// 缺失文件：空注册表而非错误
	if err := NewRegistry(t.TempDir()).Load(); err != nil {
		t.Fatalf("missing registry file should not error: %v", err)
	}
}

// TestProbe_EndToEnd 用进程内测试服务器走真实 stdio 链路：
// Probe 拉起进程→抓取清单→退出→缓存落盘→Tools 可读。
func TestProbe_EndToEnd(t *testing.T) {
	workDir := t.TempDir()

	m := NewManager(workDir)
	if err := m.Startup(); err != nil {
		t.Fatal(err)
	}
	if err := m.UpsertServer("spike", &ServerConfig{
		Command:     os.Args[0],
		Args:        []string{"-test.run=TestHelperMCPServer", "--"},
		Env:         map[string]string{"GO_WANT_HELPER_PROCESS": "1"},
		Description: "spike test server",
		Enabled:     true,
	}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := m.Probe(ctx, "spike"); err != nil {
		t.Fatalf("Probe: %v", err)
	}

	// 内存缓存
	tools := m.Tools("spike")
	if len(tools) != 2 {
		t.Fatalf("Tools() = %d, want 2", len(tools))
	}
	names := map[string]bool{}
	for _, ti := range tools {
		names[ti.Name] = true
	}
	if !names["echo"] || !names["add"] {
		t.Fatalf("unexpected tool set: %v", names)
	}
	for _, ti := range tools {
		if ti.Name == "add" && ti.InputSchema == nil {
			t.Error("add should carry an input schema")
		}
	}

	// 磁盘缓存：重开 Manager 后仍在
	if _, err := os.Stat(filepath.Join(m.RootDir(), registryFile)); err != nil {
		t.Fatalf("registry file not written: %v", err)
	}
	m2 := NewManager(workDir)
	if err := m2.Startup(); err != nil {
		t.Fatal(err)
	}
	if got := m2.Tools("spike"); len(got) != 2 {
		t.Fatalf("reloaded Tools() = %d, want 2", len(got))
	}

	// List 的 ToolCount 接线
	for _, info := range m2.List() {
		if info.Name == "spike" && info.ToolCount != 2 {
			t.Errorf("ToolCount = %d, want 2", info.ToolCount)
		}
	}

	// 禁用的服务器拒绝探测
	if err := m2.Probe(ctx, "nope"); err == nil {
		t.Error("Probe on unconfigured server should error")
	}
}

// TestHelperMCPServer 不是测试：是 Probe 测试的子进程入口（go 测试的
// 经典自启动模式——以自身二进制作为被测服务器，避免依赖外部可执行文件）。
func TestHelperMCPServer(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	server := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "helper", Version: "v0.0.1"}, nil)
	mcpsdk.AddTool(server, &mcpsdk.Tool{Name: "echo", Description: "echo back"},
		func(ctx context.Context, req *mcpsdk.CallToolRequest, args struct {
			Text string `json:"text" jsonschema:"text to echo"`
		}) (*mcpsdk.CallToolResult, any, error) {
			return &mcpsdk.CallToolResult{
				Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "echo: " + args.Text}},
			}, nil, nil
		})
	mcpsdk.AddTool(server, &mcpsdk.Tool{Name: "add", Description: "add two integers"},
		func(ctx context.Context, req *mcpsdk.CallToolRequest, args struct {
			A int `json:"a" jsonschema:"first operand"`
			B int `json:"b" jsonschema:"second operand"`
		}) (*mcpsdk.CallToolResult, any, error) {
			return &mcpsdk.CallToolResult{
				Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "42"}},
			}, nil, nil
		})
	if err := server.Run(context.Background(), &mcpsdk.StdioTransport{}); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}
