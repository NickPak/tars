package main

import (
	"context"
	"log/slog"
	"tars/internal/boot"
	"tars/internal/config"
	"tars/internal/session"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// 事件载荷类型统一定义在 pkg/event 包。

// ============================================================================
// AgentService —— 对前端暴露的 API 层，同时是应用运行态的持有者。
// 运行时依赖由装配层（boot.NewApp）在 ServiceStartup 时创建，方法只做
// 参数校验、委托与事件桥接，不直接 new 领域对象，也不依赖全局单例。
// ============================================================================

type AgentService struct {
	app *boot.App
}

func (s *AgentService) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
	if err := config.LoadAppConfig(); err != nil {
		slog.Error("Failed loading app config", "error", err)
		return err
	}
	appConfig := config.Get()

	s.app = boot.NewApp(appConfig, NewWailsSink())
	return s.app.Startup()
}

func (s *AgentService) ServiceShutdown() error {
	if s.app == nil {
		return nil
	}

	return s.app.Shutdown()
}

// --- Session management API ---

func (s *AgentService) CreateSession() (*session.Data, error) {
	return s.app.CreateSession()
}

func (s *AgentService) ListSessions() ([]*session.Data, error) {
	return s.app.ListSessions(), nil
}

func (s *AgentService) DeleteSession(id string) error {
	return s.app.DeleteSession(id)
}

func (s *AgentService) RenameSession(id, title string) error {
	return s.app.RenameSession(id, title)
}

// --- Session queries ---

func (s *AgentService) GetSession(id string) (*session.Data, error) {
	return s.app.GetSession(id)
}

// --- Message operations ---

// SubmitResult 是 SubmitMessage 的返回：后端为本轮分配的两条消息 ID。
// 前端据此回填本地占位消息，DeleteMessage 等按 ID 操作无需等待会话重载。
type SubmitResult struct {
	UserMessageID      string `json:"userMessageId"`
	AssistantMessageID string `json:"assistantMessageId"`
}

// SubmitMessage submits a user message and starts the agent loop.
func (s *AgentService) SubmitMessage(sessionID, content string) (*SubmitResult, error) {
	userMsgID, assistantID, err := s.app.SubmitMessage(sessionID, content)
	if err != nil {
		return nil, err
	}
	return &SubmitResult{UserMessageID: userMsgID, AssistantMessageID: assistantID}, nil
}

// CancelMessage cancels an in-flight SubmitMessage turn.
func (s *AgentService) CancelMessage(sessionID string) error {
	return s.app.CancelMessage(sessionID)
}

// DeleteMessage deletes a message by ID — along with all messages after it
// (truncate semantics, matching the frontend) — and returns its index.
// Rejected while a turn is running: the message list is frozen mid-turn.
func (s *AgentService) DeleteMessage(sessionID, messageID string) (int, error) {
	return s.app.DeleteMessage(sessionID, messageID)
}

// RetryMessage retries the last turn (or the turn containing the given
// assistant message). It regenerates the assistant response for that turn.
// 返回新一轮 assistant 消息 ID（前端回填本地占位，同 SubmitMessage）。
func (s *AgentService) RetryMessage(sessionID string, messageID string) (string, error) {
	return s.app.RetryMessage(sessionID, messageID)
}

// EditMessage edits a user message in-place (no regeneration).
func (s *AgentService) EditMessage(sessionID, messageID, content string) error {
	return s.app.EditMessage(sessionID, messageID, content)
}

// AnswerAskUser 提交一次询问/审批的用户答复。requestID 即工具调用 ID
// （ask_user 询问或危险调用审批共用同一答复通道）。
// value：confirm 为 "confirm"/"deny"；select 为选项 id；input 为文本；
// 审批为 "allow"/"allow_always"/"deny"。reason 为可选拒绝理由。
func (s *AgentService) AnswerAskUser(requestID, value, reason string) error {
	return s.app.AnswerAskUser(requestID, value, reason)
}
