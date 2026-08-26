// Package prompt loads and assembles the system prompt for the agent.
//
// The prompt is split into two layers:
//   - Base prompt: a stable methodology loaded from an embedded markdown file
//     (prompts/system.md). It rarely changes and benefits from prefix caching.
//   - Environment context: static process-level info (OS, available tools)
//     injected at runtime. Per-session dynamic info (working directory,
//     time, git state) is NOT included here — it's delivered via the StatusBar
//     each iteration, so the system prompt stays identical across sessions
//     and prefix caching is preserved.
//
// This mirrors the architecture used by Codex and Claude Code: a fixed system
// prompt plus an injected environment block.
package prompt

import (
	"embed"
	"fmt"
	"runtime"
	"strings"

	"tars/pkg/schema"
)

//go:embed system.md
var basePromptFS embed.FS

type Composer interface {
	GetSystemMessage() []*schema.Message
}

// BasePrompt returns the stable base prompt (without environment context).
// Loaded once from the embedded markdown file.
func BasePrompt() string {
	base, err := basePromptFS.ReadFile("system.md")
	if err != nil {
		return ""
	}
	return string(base)
}

// RenderEnvContext builds the static environment context section.
// Working directory is NOT included — see EnvironmentContext doc.
func RenderEnvContext(toolNames []string) string {
	var b strings.Builder
	b.WriteString("\n\n## Environment Context\n\n")
	_, _ = fmt.Fprintf(&b, "- Operating system: %s\n", runtime.GOOS)
	_, _ = fmt.Fprintf(&b, "- Platform: %s/%s\n", runtime.GOOS, runtime.GOARCH)
	if len(toolNames) > 0 {
		b.WriteString("- Available tools: ")
		b.WriteString(strings.Join(toolNames, ", "))
		b.WriteString("\n")
	}
	return b.String()
}

// BuildSystemPrompt assembles the base prompt from the embedded markdown file
// and appends the static environment context section.
func BuildSystemPrompt(toolNames []string) string {
	base := BasePrompt()
	if base == "" {
		return fallbackPrompt()
	}
	return base + RenderEnvContext(toolNames)
}

// BuildSystemMessage 构建系统提示词消息（含工具列表等静态环境上下文）。
// 返回 *schema.Message 供 agent 直接拼接到消息列表；纯函数，无全局状态。
func BuildSystemMessage(toolNames []string) *schema.Message {
	return &schema.Message{Role: schema.RoleSystem, Content: BuildSystemPrompt(toolNames)}
}

// fallbackPrompt is used only if the embedded file cannot be read (should
// never happen with go:embed). It provides a minimal inline prompt so the
// agent can still operate.
func fallbackPrompt() string {
	return fmt.Sprintf(`You are a helpful AI assistant running inside a user's desktop application.
Operating system: %s`, runtime.GOOS)
}
