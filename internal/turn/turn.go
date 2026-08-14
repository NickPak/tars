// Package turn 执行一轮对话：装配模型与工具、驱动 agent 的 ReAct 循环，
// 并把过程事件桥接到前端（Wails 事件）、持久化（pkg/store）与追踪（OTel）。
// 依赖方向：turn → session / agent；session 是状态聚合，不认识 turn。
package turn

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
	"tars/internal/skills"
	"tars/pkg/llm"
	"tars/pkg/prompt"
	"tars/pkg/store"
	"tars/pkg/tools"
	"tars/pkg/trace"

	"github.com/cloudwego/eino/schema"
	"github.com/wailsapp/wails/v3/pkg/application"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// turn 是一轮对话的运行器：持有本轮全部状态（锚点定位、spans、usage
// 累积），并实现 agent.Hooks 承接循环事件。轮级生命周期，每轮新建。
type turn struct {
	sess           *session.Info
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

// Start 对 sess 发起一轮对话。约定调用方已完成消息准备——尾部是本轮的
// 空 assistant 锚点（Info.AppendUserTurn / Info.PrepareRetry）。
// userText 仅用于 trace 展示。调用方需保证当前无运行中的轮。
func Start(sess *session.Info, userText string) {
	if len(sess.Messages) == 0 {
		slog.Error("turn.Start: missing assistant anchor", "session", sess.ID)
		return
	}
	t := &turn{
		sess:           sess,
		assistantIndex: len(sess.Messages) - 1,
		assistantID:    sess.Messages[len(sess.Messages)-1].ID,
		userText:       userText,
	}
	t.ctx, sess.Cancel = context.WithCancel(context.Background())
	go t.run()
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

	chatModel, modelCfg, err := llm.GetRegistry().Active()
	if err != nil {
		turnErr = err
		application.Get().Event.Emit("agent:error", event.StreamError{
			SessionID: t.sess.ID, MessageID: t.assistantID, Error: turnErr.Error(),
		})
		trace.EndTurn(turnSpan, turnErr, time.Since(startTime).Milliseconds(), "")
		return
	}

	modelWithTools, err := chatModel.WithTools(tools.DefaultManager().ToolInfos())
	if err != nil {
		turnErr = fmt.Errorf("failed to bind tools: %w", err)
		application.Get().Event.Emit("agent:error", event.StreamError{
			SessionID: t.sess.ID, MessageID: t.assistantID, Error: turnErr.Error(),
		})
		trace.EndTurn(turnSpan, turnErr, time.Since(startTime).Milliseconds(), "")
		return
	}

	t.modelID = modelCfg.ModelId // 真实模型名，用于 trace 展示
	t.modelEntry = modelCfg.ID   // 配置条目 ID，用于费用核算
	t.toolSchemas = tools.DefaultManager().ToolSchemasJSON()

	// per-session TODO 状态机由脚手架组合并注入 ctx：
	// todo_write 工具与 agent 状态栏均从 ctx 读取（agent 引擎不认识会话）。
	todoStore := store.NewTodoStore(store.GetSessionStore().SessionDir(sess.ID))
	if err := todoStore.Load(); err != nil {
		slog.Warn("failed to load todo store", "session", sess.ID, "error", err)
	}
	ctx = store.WithTodoStore(ctx, todoStore)

	// 工具工作目录：会话 workspace（或用户自定义 workDir），
	// 工具经 tools.WorkDirFromCtx 读取；缺省会回退到全局根目录。
	ctx = tools.WithWorkDir(ctx, store.GetSessionStore().ResolveWorkDir(sess.ID))

	// 交互通道：ask_user 询问与危险调用审批经此阻塞等待用户答复。
	ctx = tools.WithAsker(ctx, newAsker(sess))

	// 技能运行时：load_skill 工具读 SKILL.md、幂等状态走会话。
	ctx = tools.WithSkillRuntime(ctx, newSkillRuntime(sess))

	cfg := config.Get()
	ag := agent.New(cfg.Agent.MaxIterations, cfg.Agent.IterationTimeout, tools.DefaultManager(), modelWithTools)

	// LLM 上下文：系统提示词 + 技能目录（动态）+ 全部消息现派生。
	// 纯函数转换、每轮重建，无缓存即无失效同步问题；消息数 × 指针拷贝的
	// 成本相对一轮 LLM 调用可忽略。技能目录作为独立 system 消息每轮动态
	// 读取——装/卸技能对下一次 turn 立即生效，无需"重载"或"会话内冻结"。
	messages := make([]*schema.Message, 0, len(sess.Messages)+2)
	if sm := prompt.GetSystemMessage(); sm != nil {
		messages = append(messages, sm)
	}
	if idx := skills.GetManager().RenderIndex(); idx != "" {
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
			application.Get().Event.Emit("agent:done", event.StreamDone{
				SessionID: t.sess.ID, MessageID: t.assistantID,
				ElapsedMs: elapsedMs,
			})
		} else {

			turnErr = err
			llm.GetRegistry().SetHealthy(modelCfg.ID, false)
			// Classify iteration timeouts so the frontend can show a
			// targeted hint ("provider congested, retry?") instead of a
			// generic failure.
			kind := "error"
			if errors.Is(err, context.DeadlineExceeded) {
				kind = "timeout"
			}
			application.Get().Event.Emit("agent:error", event.StreamError{
				SessionID: t.sess.ID, MessageID: t.assistantID, Error: err.Error(), Kind: kind,
			})
		}
	} else {
		llm.GetRegistry().SetHealthy(modelCfg.ID, true)
		application.Get().Event.Emit("agent:done", event.StreamDone{
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
		if err := store.GetSessionStore().AppendMessage(sess.ID, assistantMsg); err != nil {
			slog.Warn("Failed to store message", "id", sess.ID, "error", err)
		}
	}

	trace.EndTurn(turnSpan, turnErr, elapsedMs, finalOutput)
}
