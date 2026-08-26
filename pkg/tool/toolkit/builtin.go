package toolkit

import (
	"tars/pkg/skill"
	"tars/pkg/tool/kernel"

	"tars/pkg/ask"
	"tars/pkg/mcp"
	"tars/pkg/sandbox"
	"tars/pkg/todo"
)

func RegisterBuiltinTools(registry *kernel.Registry, sandbox sandbox.SandboxProvider, todo todo.TodoProvider, ask ask.AskProvider, skill skill.SkillProvider, mcp mcp.McpProvider) {
	registry.Register(NewAskTool(ask))
	registry.Register(NewCodeInterpreter(sandbox))
	registry.Register(NewDiscoverTool(skill, mcp))
	registry.Register(NewFileTools(sandbox))
	registry.Register(NewSkillTool(skill))
	registry.Register(NewShell(sandbox))
	registry.Register(NewTodoTool(todo))
}
