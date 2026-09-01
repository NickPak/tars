package toolkit

import (
	"strings"
	"testing"

	"tars/pkg/sandbox"
)

// stateDumpSuffix 三种方言的转储段都必须以 marker 开头、含 cwd 打印，
// 且环境行最终能落成 applyState 可解析的 KEY=VALUE 形式。
func TestStateDumpSuffix(t *testing.T) {
	cmdSfx := stateDumpSuffix(sandbox.ShellCmd)
	if !strings.Contains(cmdSfx, sessionStateMarker) || !strings.Contains(cmdSfx, "cd & set") {
		t.Errorf("cmd suffix malformed: %q", cmdSfx)
	}

	shSfx := stateDumpSuffix(sandbox.ShellSh)
	if !strings.Contains(shSfx, "\npwd\nenv") {
		t.Errorf("sh suffix malformed: %q", shSfx)
	}

	pwshSfx := stateDumpSuffix(sandbox.ShellPwsh)
	if !strings.Contains(pwshSfx, sessionStateMarker) {
		t.Errorf("pwsh suffix missing marker: %q", pwshSfx)
	}
	if !strings.Contains(pwshSfx, "(Get-Location).Path") {
		t.Errorf("pwsh suffix missing cwd print: %q", pwshSfx)
	}
	// 关键约束：Get-ChildItem Env: 默认输出是两列展示（Name Value），
	// applyState 解析不了——必须经 ForEach 拼成 KEY=VALUE。
	if !strings.Contains(pwshSfx, "ForEach-Object") || !strings.Contains(pwshSfx, "$($_.Name)=$($_.Value)") {
		t.Errorf("pwsh env dump must be reshaped to KEY=VALUE: %q", pwshSfx)
	}
}
