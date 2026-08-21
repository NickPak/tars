package tools

import (
	"context"
	"sync"
	"tars/internal/event"
	"tars/pkg/ask"
	"tars/pkg/mcp"
	"tars/pkg/skills"

	"tars/pkg/todo"
)

// Env 是工具的会话级执行环境，由宿主（Controller）按会话装配，
// 经 Registry 注入每次工具执行的 ctx。它承载工具的全部运行时依赖
// （工作目录 / 各工具的会话级状态 / 交互通道），handler 经 EnvFromCtx
// 一处读取；Definition 因此保持纯声明（schema + handler），可全局共享。
type Env struct {
	// WorkspaceDir 工具的工作目录（文件/命令工具的路径根）。
	WorkspaceDir string

	// SessionData 会话数据
	SessionID string

	// Sink 是事件的 sink，用于记录事件。
	// 会话级状态：跨轮共享（持久终端语义），零值可用。
	Sink event.Sink
	// Todo 是 todo_write 的会话级状态机；nil 时 todo_write 报错。
	Todo todo.TodoProvider
	// Asker 是 ask_user 的交互通道；nil 表示非交互（ask_user 报错）。
	Ask ask.AskProvider
	// Skills 是 load_skill / discover_tools 的技能运行时；nil 时报错。
	Skills skills.SkillProvider
	// MCP 是 discover_tools 的 MCP 工具通道（检索 + 命中即注册到本会话）；
	// nil 时 discover_tools 只检索技能。
	MCP mcp.MCPProvider
	// TermSessions 是 run_command 的持久终端会话表（按工作目录键控）。
	// 会话级状态：跨轮共享（持久终端语义），零值可用。
	TermSessions sync.Map
}

type envCtxKey struct{}

// WithEnv 把会话级执行环境放入 ctx（由 Registry.Execute 统一注入）。
func WithEnv(ctx context.Context, env *Env) context.Context {
	return context.WithValue(ctx, envCtxKey{}, env)
}

// EnvFromCtx 取出执行环境；不存在（如测试直调 Handler）返回 nil。
func EnvFromCtx(ctx context.Context) *Env {
	e, _ := ctx.Value(envCtxKey{}).(*Env)
	return e
}
