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

// --- Project & session management API ---

// CreateProject 创建新项目（含一个默认会话，可直接开始对话）。
// 返回 ProjectView：Sessions 恰含新建的那一个默认会话。
func (s *AgentService) CreateProject() (*boot.ProjectView, error) {
	proj, sess, err := s.app.CreateProject()
	if err != nil {
		return nil, err
	}
	return &boot.ProjectView{Metadata: proj, Sessions: []*session.Data{sess}}, nil
}

// ListProjects 列出全部项目（含各自会话）。
func (s *AgentService) ListProjects() ([]*boot.ProjectView, error) {
	return s.app.ListProjects(), nil
}

// DeleteProject 删除项目（级联其下全部会话数据）。
func (s *AgentService) DeleteProject(id string) error {
	return s.app.DeleteProject(id)
}

// RenameProject 显式重命名项目（此后标题不再跟随会话自动命名）。
func (s *AgentService) RenameProject(id, title string) error {
	return s.app.RenameProject(id, title)
}

// CreateSession 在既有项目中新建会话（会话 Tab，与项目共用工作区）。
func (s *AgentService) CreateSession(projectID string) (*session.Data, error) {
	return s.app.CreateSession(projectID)
}

// DeleteSession 删除项目内的单个会话（真删除，清空对话记录）；
// 删除整个项目用 DeleteProject。
func (s *AgentService) DeleteSession(id string) error {
	return s.app.DeleteSession(id)
}

// CloseSession 关闭会话 Tab（仅视图标记：数据保留，可从已关闭列表重开）。
func (s *AgentService) CloseSession(id string) error {
	return s.app.CloseSession(id)
}

// OpenSession 重新打开已关闭的会话 Tab。
func (s *AgentService) OpenSession(id string) error {
	return s.app.OpenSession(id)
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
