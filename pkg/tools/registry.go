package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"tars/pkg/schema"
	"tars/pkg/zcopy"
)

// Handler 是工具的执行体。args 是模型生成的 JSON 参数原文，
// 返回的字符串会作为 tool 消息的 content 回填给模型。
type Handler func(ctx context.Context, args json.RawMessage) (string, error)

// Definition 描述一个可注册的工具：对模型的申明 + 本地执行体。
type Definition struct {
	// Name 工具名，模型按此发起调用。a-z、A-Z、0-9、下划线、连字符，最长 64。
	Name string
	// Description 给模型看的说明，是模型选择何时/如何调用工具的唯一依据。
	Description string
	// Parameters 参数的 JSON Schema，例如：
	// 	map[string]any{
	// 		"type": "object",
	// 		"properties": map[string]any{"path": map[string]any{"type": "string"}},
	// 		"required": []string{"path"},
	// 	}
	Parameters map[string]any
	// Handler 本地执行体。
	Handler Handler
}

// ToolResult 记录一次工具调用的执行结果。
type ToolResult struct {
	ID     string // 对应的 tool_call_id
	Name   string // 工具名
	Args   string // 模型生成的原始 JSON 参数
	Output string // 执行结果文本；失败时为失败原因，永远不为空
	// Error 非空表示执行失败（Handler 返回 error / panic / 工具不存在）。
	// 成功时为 nil——判断成败用这个字段，不要靠 Output 的字符串前缀。
	Error error
}

// OnToolComplete is invoked when a single tool finishes execution (in its own
// goroutine), carrying the full ToolResult. Use it to push per-tool progress
// to the frontend in real time without waiting for all tools to complete.
type OnToolComplete func(result ToolResult)

// Registry 是每会话的工具注册表与执行器：创建时把包级内置目录（见
// builtin.go）实例化并拷贝进自己的 map，之后可独立增删（会话级视图，
// 不影响其他会话）；Execute 内部完成 查找 → 权限门 → 并行执行。
//
// 权限策略是 Gate 的职责；会话级执行环境（WorkDir/Todo/交互通道/技能
// 运行时）经 Env 注入每次执行的 ctx。
type Registry struct {
	mu    sync.RWMutex
	tools map[string]*Definition
	order []string

	env  *Env
	gate *Gate
}

// NewRegistry 创建会话级工具注册表：包级内置目录（纯声明的 Definition，
// 可安全共享）拷贝进会话视图。env/gate 为会话级组件（gate 为 nil 时
// 危险调用按"非交互默认拒绝"处理）。
func NewRegistry(env *Env, gate *Gate) *Registry {
	r := &Registry{
		tools: make(map[string]*Definition, len(builtins)),
		env:   env,
		gate:  gate,
	}
	for name, def := range builtins {
		r.tools[name] = def
		r.order = append(r.order, name)
	}
	return r
}

// Register 在会话视图中注册一个工具，重名会覆盖旧定义。
// 会话级调整不影响其他会话，也不影响包级内置目录。
func (r *Registry) Register(def *Definition) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.tools[def.Name]; !exists {
		r.order = append(r.order, def.Name)
	}
	r.tools[def.Name] = def
}

// Unregister 从会话视图中移除一个工具。
func (r *Registry) Unregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.tools, name)
	for i, n := range r.order {
		if n == name {
			r.order = append(r.order[:i], r.order[i+1:]...)
			break
		}
	}
}

// FindTool 按名查找工具。
func (r *Registry) FindTool(name string) (*Definition, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	def, ok := r.tools[name]
	return def, ok
}

// Schemas 收集会话视图中全部工具的模型描述（按注册顺序），发给模型。
// 与 eino ToolInfo 的互转在 pkg/llm 适配层完成。
func (r *Registry) Schemas() []*schema.ToolSchema {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*schema.ToolSchema, 0, len(r.order))
	for _, name := range r.order {
		def := r.tools[name]
		out = append(out, &schema.ToolSchema{
			Name:        def.Name,
			Description: def.Description,
			Parameters:  def.Parameters,
		})
	}
	return out
}

// ToolNames 返回会话视图中工具的名称列表，按注册顺序排列。
func (r *Registry) ToolNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, len(r.order))
	copy(names, r.order)
	return names
}

// ToolSchemasJSON returns each tool in the session view as a JSON string
// ({"name","description","parameters"}), in registration order.
// Used for tracing: OpenInference llm.tools.N.tool.json_schema attributes.
func (r *Registry) ToolSchemasJSON() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.order))
	for _, name := range r.order {
		def := r.tools[name]
		b, err := json.Marshal(map[string]any{
			"name":        def.Name,
			"description": def.Description,
			"parameters":  def.Parameters,
		})
		if err != nil {
			continue
		}
		out = append(out, string(b))
	}
	return out
}

// Env 返回执行环境（宿主可按轮刷新 WorkDir 等字段）。
func (r *Registry) Env() *Env { return r.env }

// Execute executes tool calls in parallel and returns the full
// results (including tool name and arguments). An optional onComplete callback
// is invoked the moment each individual tool finishes — in that tool's
// goroutine, before the WaitGroup unblocks — so callers can report per-tool
// progress immediately rather than after every tool is done.
func (r *Registry) Execute(ctx context.Context, calls []schema.ToolCall, onComplete ...OnToolComplete) []ToolResult {
	var callback OnToolComplete
	if len(onComplete) > 0 {
		callback = onComplete[0]
	}

	results := make([]ToolResult, len(calls))
	var wg sync.WaitGroup
	for i, call := range calls {
		results[i] = ToolResult{
			ID:   call.ID,
			Name: call.Name,
			Args: call.Args,
		}
		def, ok := r.FindTool(call.Name)
		if !ok {
			results[i].Error = fmt.Errorf("unknown tool %q", call.Name)
			results[i].Output = results[i].Error.Error()
			if callback != nil {
				callback(results[i])
			}
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i].Output, results[i].Error = r.executeOne(ctx, def, call)
			if callback != nil {
				callback(results[i])
			}
		}()
	}
	wg.Wait()
	return results
}

func (r *Registry) executeOne(ctx context.Context, def *Definition, call schema.ToolCall) (output string, err error) {
	// 防止单个工具 panic 拖垮整个 agent loop
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("tool %q panicked: %v", call.Name, rec)
			output = err.Error()
		}
	}()

	// 工具调用 ID 入 ctx（执行期上下文）：交互工具（ask_user）与审批门
	// 用它关联用户答复。
	ctx = WithToolCallID(ctx, call.ID)
	// 会话级执行环境入 ctx：工具 handler 经 EnvFromCtx 读取。
	ctx = WithEnv(ctx, r.env)

	// 危险调用审批门：handler 执行前拦截，由框架（而非模型）发起用户审批。
	if req := classifyRisk(call); req != nil {
		ans, aerr := r.gate.Check(ctx, req)
		if aerr != nil {
			return aerr.Error(), fmt.Errorf("tool %s: %w", call.Name, aerr)
		}
		if ans.Value != "allow" {
			// 拒绝作为正常工具结果返回（理由回模型，据此调整方案）。
			out := map[string]any{"approved": false}
			switch {
			case ans.Reason != "":
				out["reason"] = ans.Reason
			case ans.Source == "timeout_default":
				out["reason"] = "approval timed out; denied by default"
			default:
				out["reason"] = "denied by user"
			}
			b, _ := json.Marshal(out)
			return string(b), nil
		}
	}

	out, e := def.Handler(ctx, zcopy.UnsafeStringToBytes(call.Args))
	if e != nil {
		return e.Error(), fmt.Errorf("tool %s: %w", call.Name, e)
	}
	return out, nil
}
