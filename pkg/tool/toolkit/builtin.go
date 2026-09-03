package toolkit

import (
	"tars/pkg/skill"
	"tars/pkg/tool"

	"tars/pkg/ask"
	"tars/pkg/mcp"
	"tars/pkg/sandbox"
	"tars/pkg/todo"
)

func RegisterBuiltinTools(registry *tool.Registry, sandbox sandbox.SandboxProvider, todo todo.TodoProvider, ask ask.AskProvider, skill skill.Provider, mcp mcp.Provider, archive ArchiveProvider) {
	registry.Register(NewAskTool(ask))
	registry.Register(NewCodeInterpreter(sandbox, archive))
	registry.Register(NewDiscoverTool(skill, mcp))
	registry.Register(NewFileTools(sandbox, archive))
	registry.Register(NewSkillTool(skill))
	registry.Register(NewSkillWriterTool(skill))
	registry.Register(NewShell(sandbox, archive))
	registry.Register(NewTodoTool(todo))
}
