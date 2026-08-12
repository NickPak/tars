// Package agent provides a generic, framework-agnostic ReAct loop:
// LLM → tool calls → execute → feed results back → LLM, until the model
// returns a plain text response or maxIterations is reached.
//
// The Agent knows nothing about Wails events, jsonl persistence, or tracing.
// All side effects are delivered through the Hooks interface, so the loop is
// reusable and unit-testable with a stubbed model + tools.
package agent

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"tars/pkg/store"
	"tars/pkg/tools"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// LoopResult summarizes one Run call.
type LoopResult struct {
	// FinalMessage is the last assistant message (plain text response).
	FinalMessage *schema.Message
	// Iterations is how many LLM calls were made.
	Iterations int
}

// Agent runs the ReAct loop. The loop itself is stateless; a single Agent can
// serve multiple sessions as long as messages/hooks are passed per-call.
type Agent struct {
	// Model is the chat model (must support tool calling).
	model model.ToolCallingChatModel
	// Executor executes tool calls (parallel; the model emits independent
	// calls in one round, dependent calls across rounds — so executors only
	// need parallel semantics within a batch).
	executor *tools.Manager
	// MaxIterations caps the ReAct loop rounds; <= 0 means a single LLM call.
	maxIterations int

	// IterationTimeout bounds a single model call (one iteration). It guards
	// against a stuck stream that never yields data. When it fires, the
	// failure is reported to Hooks.OnError as an error wrapping
	// context.DeadlineExceeded, and the host decides whether to retry.
	// Zero means no per-iteration timeout. Set it generously — busy model
	// providers may queue requests for a long while.
	IterationTimeout time.Duration

	// statusBar 是 Agent 的内部状态栏（每轮迭代前注入 <agent_status>）。
	// 在 New 中创建，无需外部设置。
	statusBar *StatusBar

	// todoStore 是 per-session 的 TODO 状态机（设计文档 2.10）。
	// Agent 内部创建并管理：InitTodoStore 完成 New + Load + 绑定 StatusBar。
	// 工具执行时 Agent 把它放入 ctx，让 todo_write Handler 能取到。
	todoStore *store.TodoStore
}

// New creates an Agent from the given config.
// sessionID 用于从全局 SessionStore 定位会话目录，自动初始化 TodoStore
// （todo.json 在会话目录下，跨会话恢复）。多 Agent 时子 Agent 传自己的 ID。
func New(sessionID string, maxIterations int, timeout time.Duration, executor *tools.Manager, model model.ToolCallingChatModel) *Agent {
	if maxIterations <= 0 {
		maxIterations = 1
	}
	a := &Agent{
		model:            model,
		executor:         executor,
		maxIterations:    maxIterations,
		IterationTimeout: timeout,
		statusBar:        NewStatusBar(),
	}
	a.initTodoStore(sessionID)
	return a
}

func (a *Agent) initTodoStore(sessionID string) {
	baseDir := ""
	if ss := store.GetSessionStore(); ss != nil && sessionID != "" {
		baseDir = ss.SessionDir(sessionID)
	}
	a.todoStore = store.NewTodoStore(baseDir)
	if err := a.todoStore.Load(); err != nil {
		slog.Error("failed to load todo store", "session", sessionID, "error", err)
	}
	a.statusBar.SetTodoStore(a.todoStore)
}

// Run executes the ReAct loop over the given message history.
// The caller's messages slice is never mutated; assistant/tool messages
// produced during the loop are appended to an internal copy.
func (a *Agent) Run(ctx context.Context, messages []*schema.Message, hooks Hooks) (*LoopResult, error) {
	if hooks == nil {
		hooks = nopHooks{}
	}

	// 工具 Handler 通过 ctx 获取 per-session 的 TodoStore
	//（todo_write 工具需要）。StatusBar 不经 ctx，直接持有引用。
	if a.todoStore != nil {
		ctx = store.WithTodoStore(ctx, a.todoStore)
	}

	msgs := append([]*schema.Message{}, messages...)

	var finalMsg *schema.Message
	iterations := 0

	for iter := 1; iter <= a.maxIterations; iter++ {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("context cancelled at iteration %d: %w", iter, err)
		}
		iterations = iter

		// 注入状态栏：追加到内存上下文尾部（不改 system 前缀，保住 KV Cache），
		// 只在内存中存在——IterationEnd 的 delta 不含它，宿主不会持久化。
		// 必须在 IterationStart 之前：IterationStart 会把 msgs 传给 trace，
		// 状态栏消息需要包含在内，Phoenix 才能看到完整的 input messages。
		msgs = append(msgs, a.statusBar.Render(ctx, iter))

		hooks.IterationStart(ctx, iter, msgs)

		msg, err := a.callModel(ctx, iter, msgs, hooks)
		if err != nil {
			return nil, err
		}
		if msg == nil {
			return nil, fmt.Errorf("model returned empty message at iteration %d", iter)
		}

		// Append this round's assistant message; delta starts with it.
		msgs = append(msgs, msg)
		delta := []*schema.Message{msg}

		// Execute tool calls if the model requested any.
		if len(msg.ToolCalls) > 0 {
			hooks.ToolsStart(ctx, msg.ToolCalls)

			results := a.executor.Execute(ctx, msg.ToolCalls, func(tr tools.ToolResult) {
				hooks.ToolResult(ctx, tr)
			})

			hooks.ToolsEnd(ctx, results)

			for _, r := range results {
				toolMsg := schema.ToolMessage(r.Output, r.ID)
				msgs = append(msgs, toolMsg)
				delta = append(delta, toolMsg)

				// 更新状态栏计数器：工具名→次数；Error 非 nil 视为失败
				a.statusBar.RecordToolCall(r.Name, r.Error)
			}
		}

		// One full iteration done (LLM call + any tool executions): hand the
		// host the full history and this round's delta for persistence etc.
		// Fires every round, including the final plain-text round.
		hooks.IterationEnd(ctx, iter, msgs, delta)

		// Plain text answer: loop ends.
		if len(msg.ToolCalls) == 0 {
			finalMsg = msg
			break
		}
	}

	if finalMsg == nil {
		return nil, fmt.Errorf("reached max iterations (%d) without a final answer", a.maxIterations)
	}

	return &LoopResult{FinalMessage: finalMsg, Iterations: iterations}, nil
}

// callModel performs the model call of one iteration with retry semantics:
// on failure it consults Hooks.OnError and retries after the returned delay
// until the host declines. User cancellation (parent ctx done) aborts
// immediately without consulting OnError.
func (a *Agent) callModel(ctx context.Context, iter int, msgs []*schema.Message, hooks Hooks) (*schema.Message, error) {
	for attempt := 1; ; attempt++ {
		callCtx := ctx
		cancel := func() {}
		if a.IterationTimeout > 0 {
			callCtx, cancel = context.WithTimeout(ctx, a.IterationTimeout)
		}

		msg, chunksSeen, err := a.streamRound(callCtx, iter, msgs, hooks)
		cancel()

		if err == nil {
			return msg, nil
		}

		// Distinguish our own iteration deadline from parent cancellation:
		// parent done → user cancelled, abort without consulting OnError.
		if ctx.Err() != nil {
			return nil, err
		}
		// Normalize iteration-timeout failures so hosts can reliably detect
		// them via errors.Is(err, context.DeadlineExceeded).
		if callCtx.Err() == context.DeadlineExceeded {
			err = fmt.Errorf("iteration %d timed out after %v: %w", iter, a.IterationTimeout, context.DeadlineExceeded)
		}

		retry, delay := hooks.OnError(ctx, iter, attempt, chunksSeen, err)
		if !retry {
			return nil, err
		}
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
		}
	}
}

// streamRound performs one streaming model call, forwarding chunks to hooks
// as they arrive, then concatenating all chunks into a single message once
// the stream ends. It reports how many chunks were delivered to the host
// before any failure, so Hooks.OnError can judge whether a retry would
// duplicate already-presented content.
func (a *Agent) streamRound(ctx context.Context, iter int, msgs []*schema.Message, hooks Hooks) (*schema.Message, int, error) {
	sr, err := a.model.Stream(ctx, msgs)
	if err != nil {
		return nil, 0, fmt.Errorf("model stream failed at iteration %d: %w", iter, err)
	}
	defer sr.Close()

	var chunks []*schema.Message
	for {
		chunk, err := sr.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, len(chunks), fmt.Errorf("model stream recv failed at iteration %d: %w", iter, err)
		}
		hooks.StreamChunk(ctx, iter, chunk)
		chunks = append(chunks, chunk)
	}

	if len(chunks) == 0 {
		return nil, 0, nil
	}
	msg, err := schema.ConcatMessages(chunks)
	if err != nil {
		return nil, len(chunks), fmt.Errorf("concat chunks failed at iteration %d: %w", iter, err)
	}
	return msg, len(chunks), nil
}
