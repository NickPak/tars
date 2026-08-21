package mcp

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// testBinary 是当前测试二进制自身（子进程入口见 registry_test.go 的
// TestHelperMCPServer：以自身作为被测服务器，避免依赖外部可执行文件）。
var testBinary = os.Args[0]

func helperServerConfig() *ServerConfig {
	return &ServerConfig{
		Command:     testBinary,
		Args:        []string{"-test.run=TestHelperMCPServer", "--"},
		Env:         map[string]string{"GO_WANT_HELPER_PROCESS": "1"},
		Description: "spike test server",
		Enabled:     true,
	}
}

func newPoolManager(t *testing.T, srv *ServerConfig) *Manager {
	t.Helper()
	return newTestManager(t, map[string]*ServerConfig{"spike": srv})
}

func callText(t *testing.T, m *Manager, tool, args string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := m.CallTool(ctx, "spike", tool, json.RawMessage(args))
	if err != nil {
		t.Fatalf("CallTool(%s): %v", tool, err)
	}
	for _, c := range res.Content {
		if tc, ok := c.(*mcpsdk.TextContent); ok {
			return tc.Text
		}
	}
	t.Fatalf("CallTool(%s): no text content", tool)
	return ""
}

func TestPool_LazyStartAndReuse(t *testing.T) {
	m := newPoolManager(t, helperServerConfig())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 首次调用拉起进程；第二次复用同一会话（同一指针）
	s1, err := m.EnsureClient(ctx, "spike")
	if err != nil {
		t.Fatal(err)
	}
	s2, err := m.EnsureClient(ctx, "spike")
	if err != nil {
		t.Fatal(err)
	}
	if s1 != s2 {
		t.Fatal("second EnsureClient should reuse the pooled session")
	}

	// 并发懒启动：全部拿到同一会话
	const n = 8
	var wg sync.WaitGroup
	sessions := make([]*mcpsdk.ClientSession, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sessions[i], errs[i] = m.EnsureClient(ctx, "spike")
		}(i)
	}
	wg.Wait()
	for i := range sessions {
		if errs[i] != nil {
			t.Fatalf("concurrent EnsureClient[%d]: %v", i, errs[i])
		}
		if sessions[i] != s1 {
			t.Fatalf("concurrent EnsureClient[%d]: different session", i)
		}
	}
}

func TestPool_CallTool(t *testing.T) {
	m := newPoolManager(t, helperServerConfig())
	if got := callText(t, m, "echo", `{"text":"hello pool"}`); got != "echo: hello pool" {
		t.Errorf("echo = %q", got)
	}
	if got := callText(t, m, "add", `{"a":19,"b":23}`); got != "42" {
		t.Errorf("add = %q", got)
	}
	// 参数原文非法：报错而非 panic
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := m.CallTool(ctx, "spike", "echo", json.RawMessage(`{bad`)); err == nil {
		t.Error("invalid raw args should error")
	}
}

func TestPool_CloseAndRestart(t *testing.T) {
	m := newPoolManager(t, helperServerConfig())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	s1, err := m.EnsureClient(ctx, "spike")
	if err != nil {
		t.Fatal(err)
	}
	m.Close("spike")
	// 关闭后再次懒启动：新进程新会话
	s2, err := m.EnsureClient(ctx, "spike")
	if err != nil {
		t.Fatal(err)
	}
	if s1 == s2 {
		t.Fatal("restart should produce a new session")
	}
	// Close 幂等
	m.Close("spike")
	m.Close("spike")
}

func TestPool_SetEnabledReapsDisabled(t *testing.T) {
	m := newPoolManager(t, helperServerConfig())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if _, err := m.EnsureClient(ctx, "spike"); err != nil {
		t.Fatal(err)
	}
	// 禁用服务器 → 连接被回收，之后拒绝连接
	if err := m.SetEnabled("spike", false); err != nil {
		t.Fatal(err)
	}
	if len(m.clients) != 0 {
		t.Fatal("disabled server connection should be reaped")
	}
	if _, err := m.EnsureClient(ctx, "spike"); err == nil {
		t.Fatal("disabled server should refuse connection")
	}
}

func TestPool_ConnectFailureNotCached(t *testing.T) {
	m := newPoolManager(t, &ServerConfig{
		Command: "nonexistent-mcp-binary-xyz",
		Enabled: true,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if _, err := m.EnsureClient(ctx, "spike"); err == nil {
		t.Fatal("bad command should error")
	}
	// 失败不入池：句柄被移除，重试走完整路径（再次报错而非读到坏句柄）
	if len(m.clients) != 0 {
		t.Fatal("failed handle should be removed from pool")
	}
	if _, err := m.EnsureClient(ctx, "spike"); err == nil {
		t.Fatal("retry should error again, not panic")
	}
}

// TestRemoveServer_PrunesRemovedServerCache 删除服务器后其探测缓存被清理，
// 保留服务器的缓存不受影响（配置与缓存同步，与 skills.Uninstall 同款语义）。
func TestRemoveServer_PrunesRemovedServerCache(t *testing.T) {
	m := newTestManager(t, map[string]*ServerConfig{
		"keep": {Command: "a", Enabled: true},
		"drop": {Command: "b", Enabled: true},
	})

	// 预置两台服务器的探测缓存
	reg := NewRegistry(m.RootDir())
	reg.Servers["keep"] = &ServerTools{ProbedAt: "t", Tools: []*ToolInfo{{Name: "t1"}}}
	reg.Servers["drop"] = &ServerTools{ProbedAt: "t", Tools: []*ToolInfo{{Name: "t2"}}}
	if err := reg.Save(); err != nil {
		t.Fatal(err)
	}
	m.registry.Store(reg)

	// 删掉 drop
	if err := m.RemoveServer("drop"); err != nil {
		t.Fatal(err)
	}

	got := m.GetRegistry()
	if _, ok := got.FindServer("drop"); ok {
		t.Error("removed server cache should be pruned")
	}
	if _, ok := got.FindServer("keep"); !ok {
		t.Error("kept server cache should survive")
	}
	// 磁盘同步：重开后无 drop
	fresh := NewRegistry(m.RootDir())
	if err := fresh.Load(); err != nil {
		t.Fatal(err)
	}
	if _, ok := fresh.FindServer("drop"); ok {
		t.Error("removed server cache should be pruned on disk")
	}
}
