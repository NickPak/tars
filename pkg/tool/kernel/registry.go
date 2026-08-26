package kernel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"tars/pkg/schema"
	"tars/pkg/zcopy"
)

// Registry 是工具注册表与并行执行器：Execute 内部完成
// 查找 → 策略裁决（Policy）→ 并行执行。
//
// Registry 不持有会话执行环境：ctx 中的会话级值由调用方在调用 Execute 前注入；
// 本类型可安全地按会话各建一份，独立增删工具互不影响。
type Registry struct {
	mu       sync.RWMutex
	tools    map[string]*Definition
	order    []string
	carriers []Carrier // 仅为 Close 保留
	policy   PolicyProvider
}

// NewRegistry 创建空注册表。policy 为 nil 时不做任何调用前裁决
func NewRegistry(policy PolicyProvider) *Registry {
	return &Registry{
		mu:       sync.RWMutex{},
		tools:    make(map[string]*Definition),
		order:    nil,
		carriers: nil,
		policy:   policy,
	}
}

// Register 注册一个 Carrier：立即展开 Tools() 写入运行索引（重名覆盖），
// Tools() 只在注册时调用一次；载体本身入列，等待 Close。
// 动态注册（如 MCP 物化）同样以实名载体 struct 注册。
// 未声明 Risk 时归一为 low（不审批）。
func (r *Registry) Register(c Carrier) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, def := range c.Definitions() {
		if def.Risk == "" {
			def.Risk = RiskLevelLow
		}
		if _, exists := r.tools[def.Name]; !exists {
			r.order = append(r.order, def.Name)
		}
		r.tools[def.Name] = def
	}
	r.carriers = append(r.carriers, c)
}

// Close 逆序调用所有已注册载体的 Close（会话销毁时调用一次）。
// 单个载体关闭失败不中断其余载体的清理，错误聚合返回。
func (r *Registry) Close() error {
	r.mu.Lock()
	carriers := make([]Carrier, len(r.carriers))
	copy(carriers, r.carriers)
	r.carriers = nil
	r.mu.Unlock()

	var errs []error
	for i := len(carriers) - 1; i >= 0; i-- {
		if err := carriers[i].Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Unregister 从注册表中移除一个工具。
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

// Schemas 收集全部工具的模型描述（按注册顺序），发给模型。
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

// ToolNames 返回工具的名称列表，按注册顺序排列。
func (r *Registry) ToolNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, len(r.order))
	copy(names, r.order)
	return names
}

// ToolSchemasJSON returns each tool as a JSON string
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

	// 工具调用 ID 入 ctx（执行期上下文）：交互工具（ask_user）与审批
	// 策略用它关联用户答复。
	ctx = WithCallID(ctx, call.ID)

	// 策略裁决：handler 执行前拦截，由框架（而非模型）发起审批。
	if r.policy != nil {
		dec, pErr := r.policy.Check(ctx, def, call)
		if pErr != nil {
			return pErr.Error(), fmt.Errorf("tool %s: %w", call.Name, pErr)
		}
		if !dec.Allow {
			return dec.Output, nil
		}
	}

	out, e := def.Handler(ctx, zcopy.UnsafeStringToBytes(call.Args))
	if e != nil {
		return e.Error(), fmt.Errorf("tool %s: %w", call.Name, e)
	}
	return out, nil
}
