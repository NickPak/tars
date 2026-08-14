package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"tars/internal/config"
	"tars/internal/event"
	"tars/internal/session"
	"tars/internal/skills"
	"tars/internal/turn"
	"tars/pkg/llm"
	"tars/pkg/prompt"
	"tars/pkg/store"
	"tars/pkg/tools"
	"tars/pkg/trace"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// 事件载荷类型统一定义在 internal/event 包。

// ============================================================================
// AgentService —— 对前端暴露的 API 层。
// 会话运行态由 internal/session.Manager 全局单例承载，本服务只做
// 参数校验、委托与事件桥接。
// ============================================================================

type AgentService struct{}

// resolveWorkDir resolves the effective workspace directory for a session,
// preferring the user's custom directory when one is set.
func (s *AgentService) resolveWorkDir(sessionID string) string {
	if meta, err := store.GetSessionStore().LoadMeta(sessionID); err == nil && meta != nil && meta.CustomWorkDir != "" {
		return meta.CustomWorkDir
	}
	return store.GetSessionStore().WorkspaceDir(sessionID)
}

func (s *AgentService) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
	if err := config.LoadAppConfig(); err != nil {
		slog.Error("Failed loading app config", "error", err)
		return err
	}
	appConfig := config.Get()

	// 注册表启动期容错：配置问题不阻塞启动，首次对话时才暴露，
	// 用户可通过设置界面修复。
	llm.InitRegistry(appConfig.LLM)

	if !appConfig.Trace.Enabled {
		slog.Info("Tracing disabled (trace.enabled is not set)")
	} else {
		if appConfig.Trace.OTLPHTTPEndpoint != "" {
			slog.Info("OTLP/HTTP trace export enabled", "endpoint", appConfig.Trace.OTLPHTTPEndpoint)
		}
		if appConfig.Trace.OTLPGrpcEndpoint != "" {
			slog.Info("OTLP/gRPC trace export enabled", "endpoint", appConfig.Trace.OTLPGrpcEndpoint)
		}
	}

	// 工作目录
	workDir := appConfig.WorkDir
	if err := os.MkdirAll(workDir, 0755); err != nil {
		slog.Error("Failed to create work directory", "dir", workDir, "error", err)
		return err
	}
	slog.Info("Agent work directory", "path", workDir)

	// 初始化全局会话存储单例
	if err := store.InitSessionStore(workDir); err != nil {
		slog.Error("Failed to init session store", "error", err)
		return err
	}

	// 初始化 Skills 存储单例，并生成索引
	if err := skills.InitManager(workDir, appConfig.Skills); err != nil {
		slog.Error("Failed to init skills store", "error", err)
		return err
	}
	err := skills.GetManager().GenerateIndex()
	if err != nil {
		slog.Warn("Failed to generate skills index", "error", err)
		return err
	}

	// 初始化全局工具管理器单例并注册内置工具
	tools.InitManager(workDir)

	// 初始化全局追踪器单例（OTLP 连接池 + 批量导出器）
	trace.InitTrace(appConfig.Trace)

	// 构建全局系统提示词单例（base + 静态 env），必须在 InitManager 之后
	prompt.InitSystemPrompt(tools.DefaultManager().ToolNames())

	// 初始化会话管理器单例并从磁盘恢复全部会话
	sessionMgr := session.InitManager()
	if err := sessionMgr.Restore(); err != nil {
		return err
	}

	return nil
}

func (s *AgentService) ServiceShutdown() error {
	sessionMgr := session.GetManager()
	if sessionMgr != nil {
		sessionMgr.CancelAll() // 先取消所有运行中的会话
	}
	trace.Shutdown() // 关闭全局 OTLP 导出器
	return nil
}

// --- Session management API ---

func (s *AgentService) CreateSession() (*session.Info, error) {
	return session.GetManager().Create()
}

func (s *AgentService) ListSessions() ([]*session.Info, error) {
	return session.GetManager().List(), nil
}

func (s *AgentService) DeleteSession(id string) error {
	return session.GetManager().Delete(id)
}

// --- Session queries ---

func (s *AgentService) GetSession(id string) (*session.Info, error) {
	sess, ok := session.GetManager().Find(id)
	if !ok {
		return nil, fmt.Errorf("session not found: %s", id)
	}
	return sess, nil
}

// --- Message operations ---

// SendMessage sends a user message and starts the agent loop.
func (s *AgentService) SendMessage(sessionID, content string) error {
	sess, ok := session.GetManager().Find(sessionID)
	if !ok {
		return fmt.Errorf("session not found: %s", sessionID)
	}
	// 轮运行中禁止再次发送：会覆盖运行中轮的 cancel，
	// 且旧 goroutine 注销时会误删新轮的标记。
	if sess.IsRunning() {
		return fmt.Errorf("turn in progress, cancel it first")
	}

	sess.AppendUserTurn(content) // 消息准备：尾部留下本轮空 assistant 锚点
	turn.Start(sess, content)
	return nil
}

// CancelMessage cancels an in-flight SendMessage turn.
func (s *AgentService) CancelMessage(sessionID string) error {
	sessionMgr := session.GetManager()
	if !sessionMgr.Has(sessionID) {
		return fmt.Errorf("session not found: %s", sessionID)
	}
	sessionMgr.Cancel(sessionID)
	return nil
}

// DeleteMessage deletes a message by ID — along with all messages after it
// (truncate semantics, matching the frontend) — and returns its index.
// Rejected while a turn is running: the message list is frozen mid-turn.
func (s *AgentService) DeleteMessage(sessionID, messageID string) (int, error) {
	sess, ok := session.GetManager().Find(sessionID)
	if !ok {
		return -1, fmt.Errorf("session not found: %s", sessionID)
	}
	if sess.IsRunning() {
		return -1, fmt.Errorf("turn in progress, cancel it first")
	}
	return sess.DeleteFrom(messageID)
}

// RetryMessage retries the last turn (or the turn containing the given
// assistant message). It regenerates the assistant response for that turn.
func (s *AgentService) RetryMessage(sessionID string, messageID string) error {
	sess, ok := session.GetManager().Find(sessionID)
	if !ok {
		return fmt.Errorf("session not found: %s", sessionID)
	}
	if sess.IsRunning() {
		return fmt.Errorf("turn in progress, cancel it first")
	}
	userText, err := sess.PrepareRetry(messageID)
	if err != nil {
		return err
	}
	turn.Start(sess, userText)
	return nil
}

// AnswerAskUser 提交一次询问/审批的用户答复。requestID 即工具调用 ID
// （ask_user 询问或危险调用审批共用同一答复通道）。
// value：confirm 为 "confirm"/"deny"；select 为选项 id；input 为文本；
// 审批为 "allow"/"allow_always"/"deny"。reason 为可选拒绝理由。
func (s *AgentService) AnswerAskUser(requestID, value, reason string) error {
	if !turn.ResolveAsk(requestID, &tools.Answer{Value: value, Reason: reason, Source: "user"}) {
		return fmt.Errorf("question not found or already resolved: %s", requestID)
	}
	return nil
}

// --- Message editing ---

// EditMessage edits a user message in-place (no regeneration).
func (s *AgentService) EditMessage(sessionID, messageID, content string) error {
	sess, ok := session.GetManager().Find(sessionID)
	if !ok {
		return fmt.Errorf("session not found: %s", sessionID)
	}
	return sess.EditUserMessage(messageID, content)
}

// --- Session rename ---

func (s *AgentService) RenameSession(id, title string) error {
	sess, ok := session.GetManager().Find(id)
	if !ok {
		return fmt.Errorf("session not found: %s", id)
	}
	if err := sess.SetTitle(title); err != nil {
		return err
	}
	application.Get().Event.Emit("session:renamed", event.SessionRenamedEvent{
		SessionID: id,
		Title:     title,
	})
	return nil
}
