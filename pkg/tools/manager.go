package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"tars/pkg/zcopy"

	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"
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

func ToolResultsToMessage(results []ToolResult) []*schema.Message {
	msgs := make([]*schema.Message, len(results))
	for i, r := range results {
		msgs[i] = schema.ToolMessage(r.Output, r.ID)
	}
	return msgs
}

type Manager struct {
	mu    sync.RWMutex
	tools map[string]*Definition
	order []string
	infos []*schema.ToolInfo
}

func NewManager() *Manager {
	return &Manager{
		mu:    sync.RWMutex{},
		tools: make(map[string]*Definition),
		order: nil,
		infos: nil,
	}
}

// Register 注册一个工具，重名会覆盖旧定义。
func (m *Manager) Register(def *Definition) {
	m.mu.Lock()
	defer m.mu.Unlock()

	_, exists := m.tools[def.Name]
	if !exists {
		m.order = append(m.order, def.Name)
	}
	m.tools[def.Name] = def

	// 把 map[string]any 转成 *jsonschema.Schema
	schemaBytes, _ := json.Marshal(def.Parameters)
	var js jsonschema.Schema
	_ = json.Unmarshal(schemaBytes, &js)

	m.infos = append(m.infos, &schema.ToolInfo{
		Name:        def.Name,
		Desc:        def.Description,
		ParamsOneOf: schema.NewParamsOneOfByJSONSchema(&js),
	})
}

// Unregister 取消注册一个工具。
func (m *Manager) Unregister(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.tools, name)
	for i, n := range m.order {
		if n == name {
			m.order = append(m.order[:i], m.order[i+1:]...)
			m.infos = append(m.infos[:i], m.infos[i+1:]...)
			break
		}
	}
}

// ToolInfos 返回 Eino ChatModel 所需的工具声明列表。
func (m *Manager) ToolInfos() []*schema.ToolInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	infos := make([]*schema.ToolInfo, len(m.infos))
	copy(infos, m.infos)
	return infos
}

// ToolNames 返回已注册工具的名称列表，按注册顺序排列。
// 用于注入系统提示词，让模型知道自己有哪些工具可用。
func (m *Manager) ToolNames() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	names := make([]string, len(m.order))
	copy(names, m.order)
	return names
}

// ToolSchemasJSON returns each registered tool as a JSON string
// ({"name","description","parameters"}), in registration order.
// Used for tracing: OpenInference llm.tools.N.tool.json_schema attributes.
func (m *Manager) ToolSchemasJSON() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]string, 0, len(m.order))
	for _, name := range m.order {
		def := m.tools[name]
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

func (m *Manager) FindTool(name string) (*Definition, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	def, ok := m.tools[name]
	return def, ok
}

// Execute executes tool calls in parallel and returns the full
// results (including tool name and arguments). An optional onComplete callback
// is invoked the moment each individual tool finishes — in that tool's
// goroutine, before the WaitGroup unblocks — so callers can report per-tool
// progress immediately rather than after every tool is done.
func (m *Manager) Execute(ctx context.Context, calls []schema.ToolCall, onComplete ...OnToolComplete) []ToolResult {
	var callback OnToolComplete
	if len(onComplete) > 0 {
		callback = onComplete[0]
	}

	results := make([]ToolResult, len(calls))
	var wg sync.WaitGroup
	for i, call := range calls {
		results[i] = ToolResult{
			ID:   call.ID,
			Name: call.Function.Name,
			Args: call.Function.Arguments,
		}
		def, ok := m.FindTool(call.Function.Name)
		if !ok {
			results[i].Error = fmt.Errorf("unknown tool %q", call.Function.Name)
			results[i].Output = results[i].Error.Error()
			if callback != nil {
				callback(results[i])
			}
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i].Output, results[i].Error = m.executeOne(ctx, def, call)
			if callback != nil {
				callback(results[i])
			}
		}()
	}
	wg.Wait()
	return results
}

func (m *Manager) executeOne(ctx context.Context, def *Definition, call schema.ToolCall) (output string, err error) {
	// 防止单个工具 panic 拖垮整个 agent loop
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("tool %q panicked: %v", call.Function.Name, r)
			output = err.Error()
		}
	}()
	out, e := def.Handler(ctx, zcopy.UnsafeStringToBytes(call.Function.Arguments))
	if e != nil {
		return e.Error(), fmt.Errorf("tool %s: %w", call.Function.Name, e)
	}
	return out, nil
}
