package toolkit

import (
	"tars/pkg/skill"
	"tars/pkg/tool/kernel"

	"tars/pkg/ask"
	"tars/pkg/mcp"
	"tars/pkg/sandbox"
	"tars/pkg/todo"
)

func RegisterBuiltinTools(registry *kernel.Registry, sandbox sandbox.SandboxProvider, todo todo.TodoProvider, ask ask.AskProvider, skill skill.SkillProvider, mcp mcp.McpProvider, archive ArchiveProvider) {
	registry.Register(NewAskTool(ask))
	registry.Register(NewCodeInterpreter(sandbox, archive))
	registry.Register(NewDiscoverTool(skill, mcp))
	registry.Register(NewFileTools(sandbox, archive))
	registry.Register(NewSkillTool(skill))
	registry.Register(NewShell(sandbox, archive))
	registry.Register(NewTodoTool(todo))
}
