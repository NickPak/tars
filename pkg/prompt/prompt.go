// Package prompt loads and assembles the system prompt for the agent.
//
// The prompt is split into two layers:
//   - Base prompt: a stable methodology loaded from an embedded markdown file
//     (prompts/system.md). It rarely changes and benefits from prefix caching.
//   - Environment context: dynamic per-session info (working directory, OS,
//     available tools) injected at runtime so the model knows its environment.
//
// This mirrors the architecture used by Codex and Claude Code: a fixed system
// prompt plus a dynamically injected environment block.
package prompt

import (
	"embed"
	"fmt"
	"runtime"
	"strings"
)

//go:embed system.md
var basePromptFS embed.FS

// EnvironmentContext holds the dynamic context injected into each session.
type EnvironmentContext struct {
	WorkDir  string   // workspace root directory
	OS       string   // operating system
	Platform string   // GOOS / GOARCH
	Tools    []string // names of registered tools
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

// RenderEnvContext builds the dynamic environment context section.
// Call this per-conversation with the conversation's workspace directory.
func RenderEnvContext(env EnvironmentContext) string {
	var b strings.Builder
	b.WriteString("\n\n## Environment Context\n\n")
	fmt.Fprintf(&b, "- Working directory: `%s`\n", env.WorkDir)
	fmt.Fprintf(&b, "- Operating system: %s\n", env.OS)
	fmt.Fprintf(&b, "- Platform: %s/%s\n", runtime.GOOS, runtime.GOARCH)
	if len(env.Tools) > 0 {
		b.WriteString("- Available tools: ")
		b.WriteString(strings.Join(env.Tools, ", "))
		b.WriteString("\n")
	}
	return b.String()
}

// BuildSystemPrompt assembles the base prompt from the embedded markdown file
// and appends a dynamically rendered environment context section.
func BuildSystemPrompt(env EnvironmentContext) string {
	base := BasePrompt()
	if base == "" {
		return fallbackPrompt(env)
	}
	return base + RenderEnvContext(env)
}

// fallbackPrompt is used only if the embedded file cannot be read (should
// never happen with go:embed). It provides a minimal inline prompt so the
// agent can still operate.
func fallbackPrompt(env EnvironmentContext) string {
	return fmt.Sprintf(`You are a helpful AI assistant running inside a user's desktop application.
Working directory: %s
Operating system: %s`, env.WorkDir, env.OS)
}
