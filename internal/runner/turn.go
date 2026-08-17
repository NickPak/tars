// 本文件实现一轮对话的短命执行单元 turn：装配模型与工具、驱动 agent 的
// ReAct 循环，并把过程事件经 event.Sink 发往前端、持久化（pkg/store）
// 与追踪（OTel）。turn 每轮新建，由 Runner.start 创建并启动。
package runner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"tars/internal/agent"
	"tars/internal/config"
	"tars/internal/event"
	"tars/internal/session"
	"tars/pkg/store"
	"tars/pkg/tools"
	"tars/pkg/trace"

	"github.com/cloudwego/eino/schema"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// turn 是一轮对话的执行单元：持有本轮全部状态（锚点定位、spans、usage
// 累积），并实现 agent.Hooks 承接循环事件。轮级生命周期，每轮新建。
type turn struct {
	sess           *session.Info
	deps           Deps
	asks           *askRegistry
	sink           event.Sink
	assistantID    string
	assistantIndex int
	userText       string

	ctx         context.Context
	turnCtx     context.Context // turn span ctx, for tool/llm spans
	modelID     string          // 真实模型名（trace 展示用）
	modelEntry  string          // 配置条目 ID（usage 费用核算用）
	toolSchemas []string

	llmSpan     oteltrace.Span
	toolSpans   map[string]oteltrace.Span
	usage       *store.UsageInfo
	finalOutput strings.Builder
}

// run 执行 ReAct 循环，自身即 hooks 传入 agent。
func (t *turn) run() {
	sess := t.sess
	// 清除"轮运行中"标记：panic 也要解除，否则该会话的删除/重试/发送被永久拒绝。
	defer func() {
		sess.Cancel = nil
	}()

	startTime := time.Now()

	ctx, turnSpan := trace.StartTurn(t.ctx, t.sess.ID, t.assistantID, t.userText)
	t.turnCtx = ctx
	var turnErr error

	chatModel, modelCfg, err := t.deps.LLM.Active()
	if err != nil {
		turnErr = err
		t.sink.Error(event.StreamError{
			SessionID: t.sess.ID, MessageID: t.assistantID, Error: turnErr.Error(),
		})
		trace.EndTurn(turnSpan, turnErr, time.Since(startTime).Milliseconds(), "")
		return
	}

	modelWithTools, err := chatModel.WithTools(t.deps.Tools.ToolInfos())
	if err != nil {
		turnErr = fmt.Errorf("failed to bind tools: %w", err)
		t.sink.Error(event.StreamError{
			SessionID: t.sess.ID, MessageID: t.assistantID, Error: turnErr.Error(),
		})
		trace.EndTurn(turnSpan, turnErr, time.Since(startTime).Milliseconds(), "")
		return
	}

	t.modelID = modelCfg.ModelId // 真实模型名，用于 trace 展示
	t.modelEntry = modelCfg.ID   // 配置条目 ID，用于费用核算
	t.toolSchemas = t.deps.Tools.ToolSchemasJSON()

	// per-session TODO 状态机由脚手架组合并注入 ctx：
	// todo_write 工具与 agent 状态栏均从 ctx 读取（agent 引擎不认识会话）。
	todoStore := store.NewTodoStore(t.deps.Store.SessionDir(sess.ID))
	if err := todoStore.Load(); err != nil {
		slog.Warn("failed to load todo store", "session", sess.ID, "error", err)
	}
	ctx = store.WithTodoStore(ctx, todoStore)

	// 工具工作目录：会话 workspace（或用户自定义 workDir），
	// 工具经 tools.WorkDirFromCtx 读取；缺省会回退到全局根目录。
	ctx = tools.WithWorkDir(ctx, t.deps.Store.ResolveWorkDir(sess.ID))

	// 交互通道：ask_user 询问与危险调用审批经此阻塞等待用户答复。
	ctx = tools.WithAsker(ctx, newAsker(sess, t.asks, t.sink))

	// 技能运行时：load_skill 工具读 SKILL.md、幂等状态走会话。
	ctx = tools.WithSkillRuntime(ctx, newSkillRuntime(t.deps.Skills, sess))

	cfg := config.Get()
	ag := agent.New(cfg.Agent.MaxIterations, cfg.Agent.IterationTimeout, t.deps.Tools, modelWithTools)

	// LLM 上下文：系统提示词 + 技能目录（动态）+ 全部消息现派生。
	// 纯函数转换、每轮重建，无缓存即无失效同步问题；消息数 × 指针拷贝的
	// 成本相对一轮 LLM 调用可忽略。技能目录作为独立 system 消息每轮动态
	// 读取——装/卸技能对下一次 turn 立即生效，无需"重载"或"会话内冻结"。
	messages := make([]*schema.Message, 0, len(sess.Messages)+2)
	if sm := t.deps.SysMsg; sm != nil {
		messages = append(messages, sm)
	}
	if idx := t.deps.Skills.RenderIndex(); idx != "" {
		messages = append(messages, schema.SystemMessage(idx))
	}
	for _, m := range sess.Messages {
		messages = append(messages, m.ToSchemaMessage())
	}

	result, err := ag.Run(ctx, messages, t)

	var finalOutput string
	if result != nil && result.FinalMessage != nil {
		finalOutput = result.FinalMessage.Content
	} else {
		finalOutput = t.finalOutput.String()
	}

	elapsedMs := time.Since(startTime).Milliseconds()

	if err != nil {
		// Cancel is treated as a clean stop, not an error.
		if ctx.Err() != nil {
			t.sink.Done(event.StreamDone{
				SessionID: t.sess.ID, MessageID: t.assistantID,
				ElapsedMs: elapsedMs,
			})
		} else {

			turnErr = err
			t.deps.LLM.SetHealthy(modelCfg.ID, false)
			// Classify iteration timeouts so the frontend can show a
			// targeted hint ("provider congested, retry?") instead of a
			// generic failure.
			kind := "error"
			if errors.Is(err, context.DeadlineExceeded) {
				kind = "timeout"
			}
			t.sink.Error(event.StreamError{
				SessionID: t.sess.ID, MessageID: t.assistantID, Error: err.Error(), Kind: kind,
			})
		}
	} else {
		t.deps.LLM.SetHealthy(modelCfg.ID, true)
		t.sink.Done(event.StreamDone{
			SessionID: t.sess.ID, MessageID: t.assistantID, Usage: t.usage,
			ElapsedMs: elapsedMs,
		})
	}

	// 把本轮的 token 统计与总耗时写入内存消息并持久化快照，
	// 历史会话重新打开后每条消息的用量信息才能恢复。
	if t.assistantIndex < len(sess.Messages) {
		assistantMsg := sess.Messages[t.assistantIndex]
		assistantMsg.Usage = t.usage
		assistantMsg.ElapsedMs = elapsedMs
		if err := t.deps.Store.AppendMessage(sess.ID, assistantMsg); err != nil {
			slog.Warn("Failed to store message", "id", sess.ID, "error", err)
		}
	}

	trace.EndTurn(turnSpan, turnErr, elapsedMs, finalOutput)
}
