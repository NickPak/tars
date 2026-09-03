package mcp

import (
	"context"
	"os"
	"sort"
	"testing"
	"time"

	"tars/pkg/schema"
	"tars/pkg/tool"
)

// 子进程服务器复用 registry_test.go 的 TestHelperMCPServer（echo 工具，
// "echo: " 前缀回显）。

func newRuntimeTestManager(t *testing.T) *Manager {
	t.Helper()
	mgr := NewManager(t.TempDir())
	if err := mgr.Startup(); err != nil {
		t.Fatal(err)
	}
	if err := mgr.UpsertServer("spike", &ServerConfig{
		Command:     os.Args[0],
		Args:        []string{"-test.run=TestHelperMCPServer", "--"},
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
	return mgr
}

// TestMCPRuntime_EndToEnd 覆盖 #7 核心闭环：
// 检索命中 → Materialize（懒启动进程）→ Definition 注册进会话 Registry →
// 经 Registry 按全名真实调用 → 结果回传 → 幂等 → GetLoadedTools。
func TestMCPRuntime_EndToEnd(t *testing.T) {
	mgr := newRuntimeTestManager(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	reg := tool.NewRegistry(nil)
	rt := mgr.NewRuntime(reg, newMemToolState())

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
	if got := rt.GetLoadedTools(); len(got) != 1 || got[0] != "mcp__spike__echo" {
		t.Errorf("GetLoadedTools = %v", got)
	}
	if names := rt.GetLoadedTools(); !sort.StringsAreSorted(names) {
		t.Errorf("GetLoadedTools should be sorted: %v", names)
	}
}

// memToolState 是 StateProvider 的内存实现（测试用）。
type memToolState struct {
	set map[string]bool
}

func newMemToolState() *memToolState { return &memToolState{set: map[string]bool{}} }

func (m *memToolState) IsToolLoaded(name string) bool { return m.set[name] }
func (m *memToolState) MarkToolLoaded(name string)    { m.set[name] = true }
func (m *memToolState) UnmarkToolLoaded(name string)  { delete(m.set, name) }
func (m *memToolState) GetLoadedTools() []string {
	out := make([]string, 0, len(m.set))
	for n := range m.set {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// TestMCPRuntime_StartupRestore 恢复路径：已加载名单中的工具在 Startup 时
// 重新注册进会话 Registry（不拉起进程）；失效条目被剔除。
func TestMCPRuntime_StartupRestore(t *testing.T) {
	mgr := newRuntimeTestManager(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 模拟"上次会话"的持久化状态：一个有效工具 + 一个失效工具
	state := newMemToolState()
	state.MarkToolLoaded("mcp__spike__echo")
	state.MarkToolLoaded("mcp__ghost__gone") // 服务器不存在

	reg := tool.NewRegistry(nil)
	rt := mgr.NewRuntime(reg, state)
	if err := rt.Startup(); err != nil {
		t.Fatalf("Startup: %v", err)
	}

	// 有效工具恢复注册（且未拉起进程——Startup 不调 EnsureClient）
	def, ok := reg.FindTool("mcp__spike__echo")
	if !ok {
		t.Fatal("restored tool should be registered into the session registry")
	}
	if def.Description == "" || def.Parameters == nil {
		t.Errorf("restored definition should carry description and schema: %+v", def)
	}

	// 失效条目被剔除
	if state.IsToolLoaded("mcp__ghost__gone") {
		t.Error("stale entry should be pruned from the loaded set")
	}
	if !state.IsToolLoaded("mcp__spike__echo") {
		t.Error("valid entry should stay in the loaded set")
	}

	// 恢复后可真实调用（此时才拉起进程）
	results := reg.Execute(ctx, []schema.ToolCall{{
		ID: "call-1", Name: "mcp__spike__echo", Args: `{"text":"restored"}`,
	}})
	if len(results) != 1 || results[0].Error != nil {
		t.Fatalf("Execute restored tool: %+v", results)
	}
	if results[0].Output != "echo: restored" {
		t.Errorf("output = %q", results[0].Output)
	}
}
