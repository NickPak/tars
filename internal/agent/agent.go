// Package agent 定义 Agent 的核心抽象与默认 ReAct 实现。
// Agent 的唯一职责是 Run；运行过程通过 event.Sink 对外发射事件。
// 循环面向领域词汇（pkg/schema）工作，eino 类型不越出 pkg/llm 边界。
package agent

import (
	"context"
	"fmt"
	"io"
	"time"

	"tars/internal/event"
	"tars/pkg/llm"
	"tars/pkg/schema"
	"tars/pkg/tools"

	"github.com/google/uuid"
)

// Agent 是核心抽象：唯一职责就是 Run。
// 用户通常使用默认实现 ReActAgent，罕见才自己实现这个接口。
type Agent interface {
	// Run 驱动一轮对话（一个 turn）：ReAct 循环直到模型给出最终回复。
	// userMsg 仅用于事件与 trace 展示——用户消息由调用方写入会话。
	// assistantID 是本轮 assistant 消息的 ID（事件的 MessageID 与之对应）。
	// provider 是本轮的模型（宿主每轮解析，模型热切换由此生效）。
	// 运行过程通过构造时注入的 event.Sink 发射事件。
	Run(ctx context.Context, userMsg, assistantID string, provider llm.Provider) (*Result, error)
}

// Result 是一轮对话的结果。
type Result struct {
	// AssistantID 是本轮 assistant 消息的 ID（与事件的 MessageID 对应）。
	AssistantID string
	// Content 是最终回复文本。
	Content string
	// Usage 是最后一帧的 token 用量（可能为 nil；EntryID 由 llm
	// 适配层在构造 provider 时标注，agent 只是透传）。
	Usage *schema.UsageInfo
	// Iterations 是实际执行的迭代数。
	Iterations int
}

// Session 是 agent 循环需要的会话能力（由 session.Info 实现）。
// 会话是单一事实来源：循环只负责往里写，下游（UI/持久化/遥测）经
// 事件流订阅，循环不认识任何下游。
type Session interface {
	// History 返回消息历史（副本）。
	History() []*schema.Message
	// UpsertAssistant 把一轮迭代的 assistant 产出聚合进指定 ID 的消息
	// （不存在则创建），含持久化快照与事件通知。
	UpsertAssistant(id string, delta *schema.Message)
	// AppendMessage 追加消息（工具结果等），含持久化与事件通知。
	AppendMessage(updateAt int64, msg ...*schema.Message)
}

// Limits 是循环的轮级约束。
type Limits struct {
	// MaxIterations ReAct 循环上限；<= 0 时单次调用。
	MaxIterations int
	// IterationTimeout 约束单次模型调用，防卡死流；0 表示不限时。
	// 触发时错误包装 context.DeadlineExceeded 返回，宿主据此分类提示。
	IterationTimeout time.Duration
}

// ReActAgent 是 Agent 的默认实现：标准 ReAct 循环，会话级长命对象
// （由 boot.Controller 持有并跨轮复用）。
//
// 会话级依赖（system/registry/session/sink）构造时注入；
// 轮级输入（assistantID/provider）经 Run 参数传入——模型热切换与
// 每轮新消息 ID 由此表达；轮级状态（statusBar）是 Run 的局部变量。
// 工具执行（查找/权限门/并行/事件）由 tools.Registry 负责；
// 消息聚合/持久化由 Session 负责。
type ReActAgent struct {
	system   func() []*schema.Message // system prompt + 技能索引（每轮动态读取）
	registry *tools.Registry
	session  Session
	sink     event.Sink
	// limits 每轮解析循环约束（配置热更新经此生效）。
	limits    func() Limits
	sessionID string
}

var _ Agent = (*ReActAgent)(nil)

// Options 是 NewReAct 的装配参数（全部会话级，跨轮复用）。
type Options struct {
	// System 返回 system 消息列表（静态提示词 + 动态技能索引），
	// 每轮迭代前调用——装/卸技能对下一轮立即生效。
	System func() []*schema.Message
	// Registry 会话级工具执行器（含执行环境 Env 与权限门 Gate）。
	Registry *tools.Registry
	// Session 会话（消息历史的单一事实来源）。
	Session Session
	// Sink 事件出口；nil 时静默。
	Sink event.Sink
	// SessionID 所属会话的 ID（事件的 SessionID 与之对应）。
	SessionID string
	// Limits 每轮解析循环约束（maxIterations/iterationTimeout），
	// 配置热更新经此生效；nil 时取默认（单次调用、不限时）。
	Limits func() Limits
}

// NewReAct 创建一个 ReAct 循环的 agent（会话级，由 Controller 持有复用）。
func NewReAct(opts Options) *ReActAgent {
	if opts.Sink == nil {
		opts.Sink = event.Discard
	}
	if opts.Limits == nil {
		opts.Limits = func() Limits { return Limits{MaxIterations: 1} }
	}
	return &ReActAgent{
		system:    opts.System,
		registry:  opts.Registry,
		session:   opts.Session,
		sink:      opts.Sink,
		limits:    opts.Limits,
		sessionID: opts.SessionID,
	}
}

// Run 实现 Agent 接口。
func (a *ReActAgent) Run(ctx context.Context, userMsg, assistantID string, provider llm.Provider) (*Result, error) {
	sessionID := a.sessionID

	// 轮级约束与轮级状态：每轮解析/新建。
	limits := a.limits()
	if limits.MaxIterations <= 0 {
		limits.MaxIterations = 1
	}
	statusBar := NewStatusBar()

	for iter := 1; iter <= limits.MaxIterations; iter++ {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("context cancelled at iteration %d: %w", iter, err)
		}

		// 1. 构建 LLM 输入：system（动态）+ 会话历史 + 状态栏。
		//    状态栏追加到内存上下文尾部（不改 system 前缀，保住 KV Cache），
		//    只在内存中存在——不写回会话，宿主不会持久化。
		history := a.session.History()
		sys := a.system()
		msgs := make([]*schema.Message, 0, len(sys)+len(history)+1)
		msgs = append(msgs, sys...)
		msgs = append(msgs, history...)
		envCtx := tools.WithEnv(ctx, a.registry.Env())
		msgs = append(msgs, statusBar.Render(envCtx, iter))

		a.emit(event.Event{Kind: event.KindIterationStart, Iteration: &event.IterationEvent{
			SessionID: sessionID, MessageID: assistantID, Iteration: iter, Messages: msgs,
		}})

		// 2. 调模型（流式，带超时归一化；失败即终止——重试策略见下）
		msg, err := a.callModel(ctx, provider, limits.IterationTimeout, iter, msgs, sessionID, assistantID)
		if err != nil {
			return nil, err
		}
		if msg == nil {
			return nil, fmt.Errorf("model returned empty message at iteration %d", iter)
		}

		// 3. assistant 增量落会话（聚合/持久化/事件由 session 负责）
		a.session.UpsertAssistant(assistantID, msg)

		a.emit(event.Event{Kind: event.KindIterationEnd, Iteration: &event.IterationEvent{
			SessionID: sessionID, MessageID: assistantID, Iteration: iter, Assistant: msg,
		}})

		// 4. 没有工具调用 → 这就是最终回复
		if len(msg.ToolCalls) == 0 {
			return &Result{
				AssistantID: assistantID,
				Content:     msg.Content,
				Usage:       msg.Usage,
				Iterations:  iter,
			}, nil
		}

		// 5. 执行工具：整体委托给 Registry（查找/权限门/并行都在内部完成）
		for _, tc := range msg.ToolCalls {
			a.emit(event.Event{Kind: event.KindToolDispatch, Tool: &event.ToolEvent{
				SessionID: sessionID, MessageID: assistantID,
				ToolCallID: tc.ID, ToolName: tc.Name, Args: tc.Args,
			}})
		}
		results := a.registry.Execute(ctx, msg.ToolCalls, func(tr tools.ToolResult) {
			// 并发回调（每个工具完成时）：只发射事件，sink 链路需并发安全。
			a.emit(event.Event{Kind: event.KindToolResult, ToolResult: &event.ToolResultEvent{
				SessionID: sessionID, MessageID: assistantID,
				ToolCallID: tr.ID, Output: tr.Output,
			}})
		})

		// 6. 工具结果落会话 + 状态栏计数（串行段），循环回到第 1 步
		now := time.Now().UnixMilli()
		for _, r := range results {
			statusBar.RecordToolCall(r.Name, r.Error)
			a.session.AppendMessage(now, &schema.Message{
				ID:         uuid.NewString(),
				Role:       schema.RoleTool,
				Content:    r.Output,
				ToolCallID: r.ID,
				CreatedAt:  now,
			})
		}
	}

	return nil, fmt.Errorf("reached max iterations (%d) without a final answer", limits.MaxIterations)
}

// callModel 执行一次带超时的流式模型调用：逐帧转发流式事件，
// 流结束后返回拼接好的完整消息。失败即返回错误（不重试——已部分
// 流式输出的重试会在 UI 产生重复内容；如需重试策略，应加独立策略
// 接口而非塞回循环）。
func (a *ReActAgent) callModel(ctx context.Context, provider llm.Provider, timeout time.Duration, iter int, msgs []*schema.Message, sessionID, assistantID string) (*schema.Message, error) {
	callCtx := ctx
	cancel := func() {}
	if timeout > 0 {
		callCtx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()

	stream, err := provider.Stream(callCtx, &llm.ChatRequest{
		Messages: msgs,
		Tools:    a.registry.Schemas(),
	})
	if err != nil {
		return nil, normalizeErr(callCtx, ctx, timeout, iter, fmt.Errorf("model stream failed at iteration %d: %w", iter, err))
	}
	defer stream.Close()

	for {
		frame, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, normalizeErr(callCtx, ctx, timeout, iter, fmt.Errorf("model stream recv failed at iteration %d: %w", iter, err))
		}
		if frame.Reasoning != "" {
			a.emit(event.Event{Kind: event.KindReasoning, Reasoning: &event.ReasoningEvent{
				SessionID: sessionID, MessageID: assistantID, Content: frame.Reasoning,
			}})
		}
		if frame.Content != "" {
			a.emit(event.Event{Kind: event.KindStreamChunk, Chunk: &event.StreamChunk{
				SessionID: sessionID, MessageID: assistantID, Chunk: frame.Content,
			}})
		}
	}

	full, err := stream.Final()
	if err != nil {
		return nil, normalizeErr(callCtx, ctx, timeout, iter, fmt.Errorf("concat chunks failed at iteration %d: %w", iter, err))
	}
	return full, nil
}

// normalizeErr 把迭代超时归一化为包装 context.DeadlineExceeded 的错误，
// 宿主经 errors.Is 可靠识别（区分于用户取消：父 ctx 完成时不做包装）。
func normalizeErr(callCtx, ctx context.Context, timeout time.Duration, iter int, err error) error {
	if ctx.Err() == nil && callCtx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("iteration %d timed out after %v: %w", iter, timeout, context.DeadlineExceeded)
	}
	return err
}

// emit 发射事件（sink 已保证非 nil，见 NewReAct）。
func (a *ReActAgent) emit(e event.Event) {
	a.sink.Emit(e)
}
