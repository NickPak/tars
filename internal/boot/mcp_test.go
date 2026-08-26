package boot

import (
	"context"
	"encoding/json"
	"os"
	"sort"
	"tars/pkg/tool/kernel"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"tars/pkg/mcp"
	"tars/pkg/schema"
)

// TestMCPRuntime_EndToEnd 覆盖 #7 核心闭环：
// 检索命中 → Materialize（懒启动进程）→ Definition 注册进会话 Registry →
// 经 Registry 按全名真实调用 → 结果回传 → 幂等 → MaterializedNames。
func TestMCPRuntime_EndToEnd(t *testing.T) {
	mgr := mcp.NewManager(t.TempDir())
	if err := mgr.Startup(); err != nil {
		t.Fatal(err)
	}
	if err := mgr.UpsertServer("spike", &mcp.ServerConfig{
		Command:     os.Args[0],
		Args:        []string{"-test.run=TestHelperBootMCPServer", "--"},
		Env:         map[string]string{"GO_WANT_HELPER_PROCESS": "1"},
		Description: "spike server",
		SourceType:  "query",
		Enabled:     true,
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mgr.CloseAll)

	// 探测填充缓存（真实 stdio 链路）
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := mgr.Probe(ctx, "spike"); err != nil {
		t.Fatalf("Probe: %v", err)
	}

	reg := kernel.NewRegistry(nil)
	rt := NewMCPProvider(mgr, reg)

	// 检索命中
	hits, err := rt.Search("echo", 5)
	if err != nil || len(hits) == 0 {
		t.Fatalf("Search: %v, %+v", err, hits)
	}
	hit := hits[0]
	if hit.FullName != "mcp__spike__echo" {
		t.Fatalf("unexpected hit: %+v", hit)
	}

	// Materialize：懒启动 + 注册
	if err := rt.Materialize(hit); err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	def, ok := reg.FindTool("mcp__spike__echo")
	if !ok {
		t.Fatal("tool should be registered into the session registry")
	}
	if def.Description == "" || def.Parameters == nil {
		t.Errorf("definition should carry description and schema: %+v", def)
	}
	// Schemas 随注册自动带上（下一轮模型可见）
	found := false
	for _, s := range reg.Schemas() {
		if s.Name == "mcp__spike__echo" {
			found = true
		}
	}
	if !found {
		t.Error("registered tool should appear in Schemas()")
	}

	// 经 Registry 真实调用（工具消息内容回传）
	results := reg.Execute(ctx, []schema.ToolCall{{
		ID:   "call-1",
		Name: "mcp__spike__echo",
		Args: `{"text":"hello dynamic"}`,
	}})
	if len(results) != 1 || results[0].Error != nil {
		t.Fatalf("Execute: %+v", results)
	}
	if results[0].Output != "echo: hello dynamic" {
		t.Errorf("output = %q", results[0].Output)
	}

	// 幂等：再次 Materialize 无副作用
	if err := rt.Materialize(hit); err != nil {
		t.Fatalf("idempotent Materialize: %v", err)
	}
	if got := rt.Loaded(); len(got) != 1 || got[0] != "mcp__spike__echo" {
		t.Errorf("Loaded = %v", got)
	}
	if names := rt.Loaded(); !sort.StringsAreSorted(names) {
		t.Errorf("Loaded should be sorted: %v", names)
	}
}

// TestHelperBootMCPServer 是 boot 包 MCP 测试的子进程服务器入口
// （go 测试自启动模式，与 pkg/mcp 的 helper 同套路，包隔离故各备一份）。
func TestHelperBootMCPServer(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	server := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "boot-helper", Version: "v0.0.1"}, nil)
	mcpsdk.AddTool(server, &mcpsdk.Tool{Name: "echo", Description: "echo back"},
		func(ctx context.Context, req *mcpsdk.CallToolRequest, args struct {
			Text string `json:"text" jsonschema:"text to echo"`
		}) (*mcpsdk.CallToolResult, any, error) {
			return &mcpsdk.CallToolResult{
				Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "echo: " + args.Text}},
			}, nil, nil
		})
	if err := server.Run(context.Background(), &mcpsdk.StdioTransport{}); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

var _ = json.RawMessage(nil) // 防 json 未使用（Materialize handler 内联使用）
