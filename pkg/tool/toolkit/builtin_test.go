package toolkit

import (
	"testing"

	"tars/pkg/tool/kernel"
)

// expectedBuiltinNames 是全部内置工具名，顺序即 RegisterBuiltinTools 的
// 注册展开顺序（载体块序 + 块内序；确定性优先，prompt 前缀稳定友好）。
var expectedBuiltinNames = []string{
	"ask_user",
	"code_interpreter",
	"discover_tools",
	"edit_file",
	"glob_files",
	"grep_files",
	"read_file",
	"write_file",
	"load_skill",
	"run_command",
	"todo_write",
}

// TestRegisterBuiltinTools 钉住内置工具注册集合：名称、顺序、声明完整性。
func TestRegisterBuiltinTools(t *testing.T) {
	reg := kernel.NewRegistry(nil)
	RegisterBuiltinTools(reg, nil, nil, nil, nil, nil, nil)

	names := reg.ToolNames()
	if len(names) != len(expectedBuiltinNames) {
		t.Fatalf("registered %d tools, want %d (%v)", len(names), len(expectedBuiltinNames), names)
	}
	for i, n := range names {
		if n != expectedBuiltinNames[i] {
			t.Errorf("position %d: got %q, want %q (registration order must stay deterministic)", i, n, expectedBuiltinNames[i])
		}
		def, _ := reg.FindTool(n)
		if def.Handler == nil {
			t.Errorf("%s: nil handler", n)
		}
		if def.Description == "" || def.Parameters == nil {
			t.Errorf("%s: description/parameters must be declared", n)
		}
	}
}

// TestSessionRegistryIsolation 两个会话注册表互不影响。
func TestSessionRegistryIsolation(t *testing.T) {
	reg := kernel.NewRegistry(nil)
	RegisterBuiltinTools(reg, nil, nil, nil, nil, nil, nil)
	other := kernel.NewRegistry(nil)
	RegisterBuiltinTools(other, nil, nil, nil, nil, nil, nil)

	reg.Unregister("read_file")
	if _, ok := other.FindTool("read_file"); !ok {
		t.Error("session registries must be independent")
	}
}
