package boot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"tars/pkg/ask"
	"tars/pkg/skill"
	"tars/pkg/tool/kernel"
	"tars/pkg/tool/toolkit"
	"time"

	"tars/internal/agent"
	"tars/internal/config"
	"tars/internal/session"
	"tars/pkg/event"
	"tars/pkg/llm"
	"tars/pkg/mcp"
	"tars/pkg/sandbox"
	"tars/pkg/schema"
	"tars/pkg/todo"
	"tars/pkg/tool/guard"

	"github.com/google/uuid"
)

// Controller 是一个会话的完整封装：会话级组件（工具执行器/权限门/
// 交互通道）在此组装并跨轮复用；对外只做一件事——跑一轮。
// 轮的运行态（cancel 标记）也由 Controller 持有：goroutine 在此创建，
// Info 只是数据。
//
// 依赖方向：App → Controller（单向）。Controller 不持有 App，所需的
// 进程级共享依赖由 App 在 NewController 时以参数传入（deps）。
//
// 事件出口：Controller 只认一个 sink（deps.NewSink 装配好的会话级出口），
// 有多少订阅者（UI/trace/…）、如何组合，由外部决定。
type Controller struct {
	cfg        *config.AppConfig
	sink       event.Sink
	llmMgr     *llm.Manager
	mu         sync.Mutex
	cancel     context.CancelFunc
	sessionMgr *session.Manager
	todoMgr    *todo.Manager
	skillPv    *SkillProvider
	sandbox    *sandbox.NativeFs
	prompt     *PromptCompose
	gate       *guard.Gate
	toolReg    *kernel.Registry
	mcpPv      *McpProvider
	agent      agent.Agent
}

// NewController 组装会话级组件：事件出口、TODO 状态机、交互通道、
// 工具执行器（构造器注入依赖 + 权限门 Gate）。
func NewController(cfg *config.AppConfig, data *session.Data, sink event.Sink, llmMgr *llm.Manager, skillMgr *skill.Manager, mcpMgr *mcp.Manager, askMgr *ask.Manager) *Controller {
	c := &Controller{
		cfg:        cfg,
		sink:       sink,
		llmMgr:     llmMgr,
		mu:         sync.Mutex{},
		cancel:     nil,
		sessionMgr: nil,
		todoMgr:    nil,
		gate:       nil,
		toolReg:    nil,
		sandbox:    nil,
		prompt:     nil,
		skillPv:    nil,
		mcpPv:      nil,
		agent:      nil,
	}

	c.sessionMgr = session.NewManager(data, sink, llmMgr, cfg.Agent.CompressionThreshold, cfg.Agent.CompressionKeepTurns, cfg.Agent.CompressionMinBatch, cfg.Agent.CompressionMaxFailures)

	c.todoMgr = todo.NewManager(c.sessionMgr.GetSessionDir())

	c.gate = guard.NewGate(askMgr, c.sessionMgr.RiskTable(), sink, c.sessionMgr.GetID())

	c.toolReg = kernel.NewRegistry(c.gate)

	// sandbox 根在构造时固定（WorkspaceDir 已由 session.NewManager 解析），
	// 不再经 provider 每次回调；零消息窗口内换目录走 Controller.SetWorkspaceDir。
	c.sandbox = sandbox.NewNativeFs(c.sessionMgr.GetWorkspaceDir())

	c.skillPv = NewSkillProvider(skillMgr, c.sessionMgr)

	// MCP 通道：闭包捕获会话 Registry（动态注册归宿）；无 MCP 时为 nil。
	c.mcpPv = NewMCPProvider(mcpMgr, c.toolReg, c.sessionMgr)

	toolkit.RegisterBuiltinTools(c.toolReg, c.sandbox, c.todoMgr, askMgr, c.skillPv, c.mcpPv, c.sessionMgr)

	c.prompt = NewPromptCompose(c.toolReg, c.skillPv, c.mcpPv)

	// 会话级 agent：跨轮复用（会话级依赖构造注入；模型/消息 ID 等轮级
	// 输入经 Run 参数传入；配置热更新经 Limits 每轮解析）。
	c.agent = agent.NewReAct(cfg.Agent, c.prompt, c.sessionMgr, c.sink, c.toolReg, toolkit.SystemEnv{}, c.todoMgr, c.skillPv, c.mcpPv)
	return c
}

func (c *Controller) Startup() error {
	err := c.sessionMgr.Startup()
	if err != nil {
		slog.Warn("failed to startup session manager", "session", c.sessionMgr.GetID(), "error", err)
		return err
	}

	err = c.todoMgr.Startup()
	if err != nil {
		slog.Warn("failed to startup todo manager", "session", c.sessionMgr.GetID(), "error", err)
		return err
	}

	err = c.gate.Startup()
	if err != nil {
		slog.Error("failed to startup gate", "session", c.sessionMgr.GetID(), "error", err)
		return err
	}

	err = c.sandbox.Startup()
	if err != nil {
		slog.Error("failed to start up sandbox", "session", c.sessionMgr.GetID(), "error", err)
		return err
	}

	err = c.prompt.Startup()
	if err != nil {
		slog.Error("failed to start up prompt compose", "session", c.sessionMgr.GetID(), "error", err)
		return err
	}

	err = c.skillPv.Startup()
	if err != nil {
		slog.Error("failed to start up skill provider", "session", c.sessionMgr.GetID(), "error", err)
		return err
	}

	err = c.mcpPv.Startup()
	if err != nil {
		slog.Error("failed to start up mcp provider", "session", c.sessionMgr.GetID(), "error", err)
		return err
	}

	err = c.agent.Startup()
	if err != nil {
		slog.Error("failed to start up agent", "session", c.sessionMgr.GetID(), "error", err)
		return err
	}

	return nil
}

func (c *Controller) Shutdown() error {
	err := c.agent.Shutdown()
	if err != nil {
		slog.Error("failed to shutdown agent", "session", c.sessionMgr.GetID(), "error", err)
		return err
	}

	err = c.mcpPv.Shutdown()
	if err != nil {
		slog.Error("failed to shutdown mcp provider", "session", c.sessionMgr.GetID(), "error", err)
		return err
	}

	err = c.skillPv.Shutdown()
	if err != nil {
		slog.Error("failed to shutdown skill provider", "session", c.sessionMgr.GetID(), "error", err)
		return err
	}

	err = c.prompt.Shutdown()
	if err != nil {
		slog.Error("failed to shutdown prompt compose", "session", c.sessionMgr.GetID(), "error", err)
		return err
	}

	err = c.sandbox.Shutdown()
	if err != nil {
		slog.Error("failed to shutdown sandbox", "session", c.sessionMgr.GetID(), "error", err)
		return err
	}

	err = c.gate.Shutdown()
	if err != nil {
		slog.Error("failed to shutdown gate", "session", c.sessionMgr.GetID(), "error", err)
		return err
	}

	err = c.todoMgr.Shutdown()
	if err != nil {
		slog.Error("failed to shutdown todo manager", "session", c.sessionMgr.GetID(), "error", err)
		return err
	}

	err = c.sessionMgr.Shutdown()
	if err != nil {
		slog.Error("failed to shutdown session manager", "session", c.sessionMgr.GetID(), "error", err)
		return err
	}
	return nil
}

// GetSessionMgr 返回本 Controller 持有的会话。
func (c *Controller) GetSessionMgr() *session.Manager { return c.sessionMgr }

// SetWorkspaceDir 会话层守卫 + sandbox 根同步（零消息窗口内，见
// session.Manager.SetWorkspaceDir 的锁定语义）。sandbox 根是固定值，
// 不跟随 provider——成功换目录后必须显式通知。
func (c *Controller) SetWorkspaceDir(dir string) error {
	if err := c.sessionMgr.SetWorkspaceDir(dir); err != nil {
		return err
	}
	c.sandbox.SetRoot(c.sessionMgr.GetWorkspaceDir())
	return nil
}

// SubmitMessage 提交一条用户消息并启动一轮对话：消息准备（追加 user 消息，
// assistant 首轮产出时创建）与运行标记同步完成，循环异步执行。
// 返回后端分配的 user/assistant 消息 ID（服务层透传给前端回填本地占位）。
func (c *Controller) SubmitMessage(content string) (string, string, error) {
	if c.IsRunning() {
		return "", "", fmt.Errorf("turn in progress, cancel it first")
	}
	userMsgID := c.sessionMgr.AppendUserMessage(content)
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
	userText, err := c.sessionMgr.PrepareRetry(messageID)
	if err != nil {
		return "", err
	}
	assistantID := uuid.NewString()
	c.start(userText, assistantID)
	return assistantID, nil
}

func (c *Controller) RenameSession(title string) error {
	return c.sessionMgr.RenameSession(title)
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
	// 清除"轮运行中"标记：panic 也要解除，否则该会话的删除/重试/发送被永久拒绝。
	defer func() {
		c.mu.Lock()
		c.cancel = nil
		c.mu.Unlock()
	}()

	startTime := time.Now()
	elapsed := func() int64 { return time.Since(startTime).Milliseconds() }

	sink := c.sink

	chatModel, modelCfg, err := c.llmMgr.Active()
	if err != nil {
		EmitError(sink, c.sessionMgr.GetID(), assistantID, err, "", 0)
		return
	}

	// Provider 适配：工具描述在 llm 适配层完成绑定（WithTools）。
	provider, err := llm.NewProvider(chatModel, c.toolReg.Schemas(), modelCfg.EntryID)
	if err != nil {
		EmitError(sink, c.sessionMgr.GetID(), assistantID, fmt.Errorf("failed to bind tools: %w", err), "", 0)
		return
	}

	// 轮开始事件：轮级元信息经载荷传给 trace 等订阅者（它们据此建立
	// 轮级状态）。Controller 不关心外部有哪些订阅者。
	sink.Emit(event.Event{
		Kind: event.KindTurnStarted,
		Turn: &event.TurnEvent{
			SessionID:   c.sessionMgr.GetID(),
			MessageID:   assistantID,
			UserText:    userText,
			ModelID:     modelCfg.ModelId,
			System:      c.prompt.GetBaseMessage().Content,
			ToolSchemas: c.toolReg.ToolSchemasJSON(),
		},
	})

	result, err := c.agent.Run(ctx, assistantID, provider)
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
			sink.Emit(event.Event{Kind: event.KindTurnEnded, Done: &event.StreamDone{
				SessionID: c.sessionMgr.GetID(), MessageID: assistantID,
				ElapsedMs: elapsedMs, FinalOutput: finalOutput,
			}})
		} else {
			c.llmMgr.SetHealthy(modelCfg.EntryID, false)
			// 迭代超时单独分类，前端据此给出针对性提示（"provider 拥塞，重试？"）。
			kind := "error"
			if errors.Is(err, context.DeadlineExceeded) {
				kind = "timeout"
			}
			EmitError(sink, c.sessionMgr.GetID(), assistantID, err, kind, elapsedMs)
		}
	} else {
		c.llmMgr.SetHealthy(modelCfg.EntryID, true)
		sink.Emit(event.Event{Kind: event.KindTurnEnded, Done: &event.StreamDone{
			SessionID: c.sessionMgr.GetID(), MessageID: assistantID, Usage: usage,
			ElapsedMs: elapsedMs, FinalOutput: finalOutput,
		}})
	}

	// 每迭代 assistant 消息自带用量（交错式存储）；轮级合计用量与耗时经
	// KindTurnEnded 透出（Usage 为逐迭代累加的轮级合计）。
}

// EmitError 发射一轮错误结束事件。
func EmitError(sink event.Sink, sessionID, messageID string, err error, kind string, elapsedMs int64) {
	sink.Emit(event.Event{Kind: event.KindError, Error: &event.StreamError{
		SessionID: sessionID, MessageID: messageID, Error: err.Error(), Kind: kind,
		ElapsedMs: elapsedMs,
	}})
}
