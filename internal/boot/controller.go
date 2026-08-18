package boot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"tars/internal/agent"
	"tars/internal/config"
	"tars/internal/event"
	"tars/internal/session"
	"tars/internal/skills"
	"tars/pkg/llm"
	"tars/pkg/schema"
	"tars/pkg/todo"
	"tars/pkg/tools"

	"github.com/google/uuid"
)

// Controller 是一个会话的完整封装：会话级组件（执行环境 Env/工具执行器/
// 权限门/交互通道）在此组装并跨轮复用；对外只做一件事——跑一轮。
// 轮的运行态（cancel 标记）也由 Controller 持有：goroutine 在此创建，
// Info 只是数据。
//
// 依赖方向：App → Controller（单向）。Controller 不持有 App，所需的
// 进程级共享依赖由 App 在 NewController 时以参数传入（deps）。
//
// 事件出口：Controller 只认一个 sink（deps.NewSink 装配好的会话级出口），
// 有多少订阅者（UI/trace/…）、如何组合，由外部决定。
type Controller struct {
	sess *session.Info
	deps Deps
	sink event.Sink // 会话级事件出口（装配层组合，含 UI/trace 等订阅者）

	// cancel 非 nil 表示有运行中的轮（运行标记 + 取消通道）。
	mu     sync.Mutex
	cancel context.CancelFunc

	asker    *asker
	env      *tools.Env
	registry *tools.Registry
	// agent 会话级 ReAct 循环（跨轮复用）。
	agent agent.Agent
}

// Deps 是 Controller 需要的进程级共享依赖，由 App 在装配时传入。
type Deps struct {
	// Store 会话存储（会话目录/工作目录解析）。
	Store *session.Store
	// NewSink 为每个会话组装事件出口（UI + trace 等订阅者由外部组合）；
	// nil 时静默。
	NewSink func(sessionID string) event.Sink
	// LLM 模型注册表（Active/SetHealthy）。
	LLM *llm.Registry
	// SysMsg 静态系统提示词。
	SysMsg *schema.Message
	// Skills 技能管理器（技能索引与运行时委托）。
	Skills *skills.Manager
	// Asks 答复通道注册表（跨会话共享）。
	Asks *askRegistry
}

// NewController 组装会话级组件：事件出口、TODO 状态机、交互通道、
// 工具执行器（进程级目录 + 会话级 Env + 权限门 Gate）。
func NewController(deps Deps, sess *session.Info) *Controller {
	// 会话级事件出口：订阅者组合由装配层决定，Controller 只用它发事件。
	sink := event.Sink(event.Discard)
	if deps.NewSink != nil {
		sink = deps.NewSink(sess.ID)
	}

	// 会话级 TODO 状态机：todo_write 工具与 agent 状态栏经 Env 读取。
	todoStore := todo.NewTodoStore(deps.Store.SessionDir(sess.ID))
	if err := todoStore.Load(); err != nil {
		slog.Warn("failed to load todo store", "session", sess.ID, "error", err)
	}

	// 交互通道：ask_user 询问（Asker）与危险调用审批（Approver）双角色，
	// 同一 asker 实例承担。
	asker := newAsker(sess, deps.Asks, sink)

	// 每会话工具执行器：进程级目录 + 会话级执行环境（Env）+ 权限门（Gate）。
	env := &tools.Env{
		WorkDir: deps.Store.ResolveWorkDir(sess.ID),
		Todo:    todoStore,
		Asker:   asker,
		Skills:  newSkillRuntime(deps.Skills, sess),
	}
	c := &Controller{
		sess:     sess,
		deps:     deps,
		sink:     sink,
		asker:    asker,
		env:      env,
		registry: tools.NewRegistry(env, tools.NewGate(asker, sess.RiskTable())),
	}

	// 会话级 agent：跨轮复用（会话级依赖构造注入；模型/消息 ID 等轮级
	// 输入经 Run 参数传入；配置热更新经 Limits 每轮解析）。
	c.agent = agent.NewReAct(agent.Options{
		System:    c.systemMessages,
		Registry:  c.registry,
		Session:   sess,
		Sink:      sink,
		SessionID: sess.ID,
		Limits: func() agent.Limits {
			cfg := config.Get()
			return agent.Limits{
				MaxIterations:    cfg.Agent.MaxIterations,
				IterationTimeout: cfg.Agent.IterationTimeout,
			}
		},
	})
	return c
}

// GetSession 返回本 Controller 持有的会话。
func (c *Controller) GetSession() *session.Info { return c.sess }

// Submit 提交一条用户消息并启动一轮对话：消息准备（追加 user 消息，
// assistant 首轮产出时创建）与运行标记同步完成，循环异步执行。
// 返回后端分配的 user/assistant 消息 ID（服务层透传给前端回填本地占位）。
func (c *Controller) Submit(content string) (string, string, error) {
	if c.IsRunning() {
		return "", "", fmt.Errorf("turn in progress, cancel it first")
	}
	userMsgID := c.sess.AppendUser(content)
	assistantID := uuid.NewString()
	c.start(content, assistantID)
	return userMsgID, assistantID, nil
}

// Retry 重试一轮对话：消息准备（截断到目标轮的 user 消息）+ 启动执行。
// 返回新一轮 assistant 消息 ID。
func (c *Controller) Retry(messageID string) (string, error) {
	if c.IsRunning() {
		return "", fmt.Errorf("turn in progress, cancel it first")
	}
	userText, err := c.sess.PrepareRetry(messageID)
	if err != nil {
		return "", err
	}
	assistantID := uuid.NewString()
	c.start(userText, assistantID)
	return assistantID, nil
}

// IsRunning 报告本会话是否有运行中的轮。
func (c *Controller) IsRunning() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cancel != nil
}

// Cancel 取消当前运行中的轮（无运行中的轮则无事发生）。
func (c *Controller) Cancel() {
	c.mu.Lock()
	cancel := c.cancel
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// start 设置运行标记后异步启动一轮对话。调用方需保证当前无运行中的轮。
func (c *Controller) start(userText, assistantID string) {
	ctx, cancel := context.WithCancel(context.Background())
	// 运行标记同步设置：start 返回后轮即在运行，CancelMessage 立即有效。
	c.mu.Lock()
	c.cancel = cancel
	c.mu.Unlock()
	go c.run(ctx, userText, assistantID)
}

// run 执行一轮对话：本轮组件（Provider/traceSink/FanOut/agent）装配 →
// agent.Run → 轮级收尾（Done/Error 事件、usage 写回、turn span）。
// ctx 已携带本轮的取消通道（start 注入）。
func (c *Controller) run(ctx context.Context, userText, assistantID string) {
	sess := c.sess

	// 清除"轮运行中"标记：panic 也要解除，否则该会话的删除/重试/发送被永久拒绝。
	defer func() {
		c.mu.Lock()
		c.cancel = nil
		c.mu.Unlock()
	}()

	startTime := time.Now()
	elapsed := func() int64 { return time.Since(startTime).Milliseconds() }

	sink := c.sink

	chatModel, modelCfg, err := c.deps.LLM.Active()
	if err != nil {
		emitError(sink, sess.ID, assistantID, err, "", 0)
		return
	}

	// Provider 适配：工具描述在 llm 适配层完成绑定（WithTools）。
	provider, err := llm.NewProvider(chatModel, c.registry.Schemas(), modelCfg.EntryID)
	if err != nil {
		emitError(sink, sess.ID, assistantID, fmt.Errorf("failed to bind tools: %w", err), "", 0)
		return
	}

	// 每轮刷新工作目录（会话运行期间用户可能改了自定义 workDir）。
	c.env.WorkDir = c.deps.Store.ResolveWorkDir(sess.ID)

	// 轮开始事件：轮级元信息经载荷传给 trace 等订阅者（它们据此建立
	// 轮级状态）。Controller 不关心外部有哪些订阅者。
	sink.Emit(event.Event{Kind: event.KindTurnStarted, Turn: &event.TurnEvent{
		SessionID: sess.ID, MessageID: assistantID, UserText: userText,
		ModelID: modelCfg.ModelId, System: c.sysContent(),
		ToolSchemas: c.registry.ToolSchemasJSON(),
	}})

	result, err := c.agent.Run(ctx, userText, assistantID, provider)
	elapsedMs := elapsed()

	var finalOutput string
	var usage *schema.UsageInfo
	if result != nil {
		finalOutput = result.Content
		usage = result.Usage
	}

	if err != nil {
		// 取消是干净停止，不算错误。
		if ctx.Err() != nil {
			sink.Emit(event.Event{Kind: event.KindDone, Done: &event.StreamDone{
				SessionID: sess.ID, MessageID: assistantID,
				ElapsedMs: elapsedMs, FinalOutput: finalOutput,
			}})
		} else {
			c.deps.LLM.SetHealthy(modelCfg.EntryID, false)
			// 迭代超时单独分类，前端据此给出针对性提示（"provider 拥塞，重试？"）。
			kind := "error"
			if errors.Is(err, context.DeadlineExceeded) {
				kind = "timeout"
			}
			emitError(sink, sess.ID, assistantID, err, kind, elapsedMs)
		}
	} else {
		c.deps.LLM.SetHealthy(modelCfg.EntryID, true)
		sink.Emit(event.Event{Kind: event.KindDone, Done: &event.StreamDone{
			SessionID: sess.ID, MessageID: assistantID, Usage: usage,
			ElapsedMs: elapsedMs, FinalOutput: finalOutput,
		}})
	}

	// 把本轮的 token 统计与总耗时写入 assistant 消息并持久化快照，
	// 历史会话重新打开后每条消息的用量信息才能恢复。
	// 一轮未产出（首轮即失败）时无 assistant 消息，静默跳过。
	sess.FinalizeAssistant(assistantID, usage, elapsedMs)
}

// emitError 发射一轮错误结束事件。
func emitError(sink event.Sink, sessionID, messageID string, err error, kind string, elapsedMs int64) {
	sink.Emit(event.Event{Kind: event.KindError, Error: &event.StreamError{
		SessionID: sessionID, MessageID: messageID, Error: err.Error(), Kind: kind,
		ElapsedMs: elapsedMs,
	}})
}

// systemMessages 构建本轮的 system 消息列表：静态提示词 + 技能索引（动态）。
// 纯函数、每轮重建，无缓存即无失效同步问题；装/卸技能对下一轮立即生效。
func (c *Controller) systemMessages() []*schema.Message {
	var sys []*schema.Message
	if sm := c.deps.SysMsg; sm != nil {
		sys = append(sys, sm)
	}
	if idx := c.deps.Skills.RenderIndex(); idx != "" {
		sys = append(sys, &schema.Message{Role: schema.RoleSystem, Content: idx})
	}
	return sys
}

// sysContent 返回静态系统提示词全文（trace 展示用）。
func (c *Controller) sysContent() string {
	if sm := c.deps.SysMsg; sm != nil {
		return sm.Content
	}
	return ""
}
