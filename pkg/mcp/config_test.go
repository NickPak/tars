package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newTestManager 建一个带初始服务器配置的 Manager（经 UpsertServer 落盘，
// 与运行期同一写入路径）。
func newTestManager(t *testing.T, servers map[string]*ServerConfig) *Manager {
	t.Helper()
	m, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for name, srv := range servers {
		if err := m.UpsertServer(name, srv); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(m.CloseAll)
	return m
}

func TestConfigValidate(t *testing.T) {
	cfg := &Config{Servers: map[string]*ServerConfig{
		"good-server": {Command: "npx", Args: []string{"-y", "some-mcp"}},
	}}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	srv := cfg.Servers["good-server"]
	if srv.Risk != RiskMedium {
		t.Errorf("risk = %q, want default medium", srv.Risk)
	}
	if srv.Enabled {
		t.Error("enabled should default to false (fail-safe)")
	}
}

func TestConfigValidate_Errors(t *testing.T) {
	cases := map[string]*Config{
		"bad name":       {Servers: map[string]*ServerConfig{"Bad_Name": {Command: "x"}}},
		"empty name":     {Servers: map[string]*ServerConfig{"": {Command: "x"}}},
		"empty command":  {Servers: map[string]*ServerConfig{"srv": {}}},
		"unknown risk":   {Servers: map[string]*ServerConfig{"srv": {Command: "x", Risk: "fatal"}}},
		"nil server cfg": {Servers: map[string]*ServerConfig{"srv": nil}},
	}
	for label, cfg := range cases {
		if err := cfg.Validate(); err == nil {
			t.Errorf("%s: expected error, got nil", label)
		}
	}
}

func TestManagerListEnabled(t *testing.T) {
	m := newTestManager(t, map[string]*ServerConfig{
		"on":  {Command: "a", Enabled: true, Description: "stock data", SourceType: "query"},
		"off": {Command: "b"},
	})
	if got := len(m.List()); got != 2 {
		t.Fatalf("List() = %d, want 2", got)
	}
	en := m.Enabled()
	if len(en) != 1 || en[0].Name != "on" || en[0].Risk != RiskMedium {
		t.Fatalf("Enabled() = %+v, want only the enabled server with default risk", en)
	}
	if m.Server("off") == nil {
		t.Fatal("Server(off) should exist")
	}
	if m.Server("nope") != nil {
		t.Fatal("Server(nope) should be nil")
	}
}

// TestServersFile_Persistence 服务器配置的磁盘自管闭环：
// Upsert 落盘 → 重开 Manager 配置回来（重启不丢）；${VAR} 引用原样保留
// （不在加载/保存层展开，写回不泄露密钥）。
func TestServersFile_Persistence(t *testing.T) {
	workDir := t.TempDir()
	m, err := NewManager(workDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.CloseAll)

	if err := m.UpsertServer("fetch", &ServerConfig{
		Command:    "python",
		Args:       []string{"-m", "mcp_server_fetch"},
		Env:        map[string]string{"API_KEY": "${FETCH_API_KEY}"},
		SourceType: "read",
		Enabled:    true,
	}); err != nil {
		t.Fatalf("UpsertServer: %v", err)
	}

	// 磁盘文件存在且 ${VAR} 引用未展开
	raw, err := os.ReadFile(filepath.Join(m.RootDir(), serversFile))
	if err != nil {
		t.Fatalf("servers file not written: %v", err)
	}
	if !strings.Contains(string(raw), "${FETCH_API_KEY}") {
		t.Errorf("env ref should be preserved raw on disk:\n%s", raw)
	}

	// 重开 Manager：配置回来（模拟重启）
	m2, err := NewManager(workDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m2.CloseAll)
	srv := m2.Server("fetch")
	if srv == nil || srv.Command != "python" || !srv.Enabled || srv.SourceType != "read" {
		t.Fatalf("reloaded server mismatch: %+v", srv)
	}
	if got := m2.GetConfig().Servers["fetch"].Env["API_KEY"]; got != "${FETCH_API_KEY}" {
		t.Errorf("env ref expanded or lost: %q", got)
	}
	if srv.Risk != RiskMedium {
		t.Errorf("risk should default to medium, got %q", srv.Risk)
	}
}

func TestUpsertServer(t *testing.T) {
	m, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.CloseAll)

	// 校验：非法名字 / nil 配置 / 空命令（Save 路径 Validate 把关）
	if err := m.UpsertServer("Bad_Name", &ServerConfig{Command: "x"}); err == nil {
		t.Error("bad name should error")
	}
	if err := m.UpsertServer("srv", nil); err == nil {
		t.Error("nil config should error")
	}
	if err := m.UpsertServer("srv", &ServerConfig{}); err == nil {
		t.Error("empty command should error")
	}
	if m.Server("srv") != nil {
		t.Error("failed upsert should not leave a partial entry")
	}

	// 防御性拷贝：调用方事后改其持有的切片/map 不污染已发布配置
	args := []string{"-y", "some-mcp"}
	env := map[string]string{"K": "v"}
	if err := m.UpsertServer("srv", &ServerConfig{Command: "npx", Args: args, Env: env, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	args[0] = "MUTATED"
	env["K"] = "MUTATED"
	got := m.GetConfig().Servers["srv"]
	if got.Args[0] != "-y" || got.Env["K"] != "v" {
		t.Errorf("stored config shares caller memory: %+v", got)
	}

	// 覆盖：同名列替换（连接回收路径由 pool 测试覆盖）
	if err := m.UpsertServer("srv", &ServerConfig{Command: "uvx", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if got := m.GetConfig().Servers["srv"]; got.Command != "uvx" {
		t.Errorf("overwrite mismatch: %+v", got)
	}
}

func TestSetEnabled(t *testing.T) {
	workDir := t.TempDir()
	m, err := NewManager(workDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.CloseAll)

	if err := m.SetEnabled("nope", true); err == nil {
		t.Error("SetEnabled on unconfigured server should error")
	}
	if err := m.UpsertServer("srv", &ServerConfig{Command: "x", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := m.SetEnabled("srv", false); err != nil {
		t.Fatal(err)
	}
	if len(m.Enabled()) != 0 {
		t.Error("disabled server should leave the enabled set")
	}
	// 幂等：同值无操作
	if err := m.SetEnabled("srv", false); err != nil {
		t.Fatal(err)
	}

	// 磁盘同步：重开后仍禁用
	m2, err := NewManager(workDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m2.CloseAll)
	if m2.Server("srv").Enabled {
		t.Error("disabled state should persist across reload")
	}
}

func TestRemoveServer(t *testing.T) {
	workDir := t.TempDir()
	m, err := NewManager(workDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.CloseAll)
	for name, srv := range map[string]*ServerConfig{
		"keep": {Command: "a", Enabled: true},
		"drop": {Command: "b", Enabled: true},
	} {
		if err := m.UpsertServer(name, srv); err != nil {
			t.Fatal(err)
		}
	}

	if err := m.RemoveServer("drop"); err != nil {
		t.Fatal(err)
	}
	if m.Server("drop") != nil {
		t.Error("removed server should be gone from config")
	}
	// 幂等
	if err := m.RemoveServer("drop"); err != nil {
		t.Fatal(err)
	}

	// 磁盘同步：重开后 drop 不在、keep 仍在
	m2, err := NewManager(workDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m2.CloseAll)
	if m2.Server("drop") != nil || m2.Server("keep") == nil {
		t.Errorf("reload mismatch: %+v", m2.List())
	}
}
