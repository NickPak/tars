package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"tars/internal/config"
	"tars/internal/event"
	"tars/internal/session"
	"tars/internal/wire"
	"tars/pkg/tools"
	"tars/pkg/trace"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// 事件载荷类型统一定义在 internal/event 包。

// ============================================================================
// AgentService —— 对前端暴露的 API 层，同时是应用运行态的持有者。
// 运行时依赖由装配层（wire.Build）在 ServiceStartup 时创建，方法只做
// 参数校验、委托与事件桥接，不直接 new 领域对象，也不依赖全局单例。
// ============================================================================

type AgentService struct {
	rt *wire.Runtime
}

// resolveWorkDir resolves the effective workspace directory for a session,
// preferring the user's custom directory when one is set.
func (s *AgentService) resolveWorkDir(sessionID string) string {
	if meta, err := s.rt.Store.LoadMeta(sessionID); err == nil && meta != nil && meta.CustomWorkDir != "" {
		return meta.CustomWorkDir
	}
	return s.rt.Store.WorkspaceDir(sessionID)
}

func (s *AgentService) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
	if err := config.LoadAppConfig(); err != nil {
		slog.Error("Failed loading app config", "error", err)
		return err
	}
	appConfig := config.Get()

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

	// 装配全部运行时依赖（会话存储/技能/工具/模型/会话管理器/系统提示词）
	rt, err := wire.Build(wire.Options{
		WorkDir: workDir,
		LLM:     appConfig.LLM,
		Skills:  appConfig.Skills,
		Sink:    wailsSink{},
	})
	if err != nil {
		slog.Error("Failed to build runtime", "error", err)
		return err
	}
	s.rt = rt

	// 追踪器（进程级基础设施，OTLP 连接池 + 批量导出器）
	trace.InitTrace(appConfig.Trace)

	// 从磁盘恢复全部会话
	if err := s.rt.Sessions.Restore(); err != nil {
		return err
	}

	return nil
}

func (s *AgentService) ServiceShutdown() error {
	if s.rt != nil && s.rt.Sessions != nil {
		s.rt.Sessions.CancelAll() // 先取消所有运行中的会话
	}
	trace.Shutdown() // 关闭全局 OTLP 导出器
	return nil
}

// --- Session management API ---

func (s *AgentService) CreateSession() (*session.Info, error) {
	return s.rt.Sessions.Create()
}

func (s *AgentService) ListSessions() ([]*session.Info, error) {
	return s.rt.Sessions.List(), nil
}

func (s *AgentService) DeleteSession(id string) error {
	return s.rt.Sessions.Delete(id)
}

// --- Session queries ---

func (s *AgentService) GetSession(id string) (*session.Info, error) {
	sess, ok := s.rt.Sessions.Find(id)
	if !ok {
		return nil, fmt.Errorf("session not found: %s", id)
	}
	return sess, nil
}

// --- Message operations ---

// SendMessage sends a user message and starts the agent loop.
func (s *AgentService) SendMessage(sessionID, content string) error {
	return s.rt.Runner.Submit(sessionID, content)
}

// CancelMessage cancels an in-flight SendMessage turn.
func (s *AgentService) CancelMessage(sessionID string) error {
	return s.rt.Runner.Cancel(sessionID)
}

// DeleteMessage deletes a message by ID — along with all messages after it
// (truncate semantics, matching the frontend) — and returns its index.
// Rejected while a turn is running: the message list is frozen mid-turn.
func (s *AgentService) DeleteMessage(sessionID, messageID string) (int, error) {
	sess, ok := s.rt.Sessions.Find(sessionID)
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
	return s.rt.Runner.Retry(sessionID, messageID)
}

// AnswerAskUser 提交一次询问/审批的用户答复。requestID 即工具调用 ID
// （ask_user 询问或危险调用审批共用同一答复通道）。
// value：confirm 为 "confirm"/"deny"；select 为选项 id；input 为文本；
// 审批为 "allow"/"allow_always"/"deny"。reason 为可选拒绝理由。
func (s *AgentService) AnswerAskUser(requestID, value, reason string) error {
	if !s.rt.Runner.ResolveAsk(requestID, &tools.Answer{Value: value, Reason: reason, Source: "user"}) {
		return fmt.Errorf("question not found or already resolved: %s", requestID)
	}
	return nil
}

// --- Message editing ---

// EditMessage edits a user message in-place (no regeneration).
func (s *AgentService) EditMessage(sessionID, messageID, content string) error {
	sess, ok := s.rt.Sessions.Find(sessionID)
	if !ok {
		return fmt.Errorf("session not found: %s", sessionID)
	}
	return sess.EditUserMessage(messageID, content)
}

// --- Session rename ---

func (s *AgentService) RenameSession(id, title string) error {
	sess, ok := s.rt.Sessions.Find(id)
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
