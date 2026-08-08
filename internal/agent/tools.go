package agent

// import "context"

// // ToolCall mirrors one tool invocation requested by the model.
// type ToolCall struct {
// 	ID   string
// 	Name string
// 	Args string
// }

// // ToolResult is the output of a single tool execution.
// type ToolResult struct {
// 	ID     string // matches ToolCall.ID
// 	Name   string
// 	Args   string
// 	Output string
// }

// // ToolsExecutor executes a batch of tool calls. Implementations may run them
// // serially or in parallel. Each result must be reported exactly once via the
// // onResult callback; the returned slice must contain one entry per input call
// // in the same order.
// type ToolsExecutor interface {
// 	Execute(ctx context.Context, calls []ToolCall, onResult func(ToolResult)) []ToolResult
// }
