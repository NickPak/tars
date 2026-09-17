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
// AgentService —— 项目/会话/对话轮 API，同时是应用生命周期的持有者：
// 在 Services 切片中首位注册，ServiceStartup 最先执行（创建 boot.App 并
// 登记 boot.SetCurrent），ServiceShutdown 最后执行（Wails 逆序关闭，
// 等其他服务关闭后再 Shutdown 内核）。其余服务经 boot.Current() 取实例。
// ============================================================================

type AgentService struct{}

func (s *AgentService) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
	if err := config.LoadAppConfig(); err != nil {
		slog.Error("Failed loading app config", "error", err)
		return err
	}
	appConfig := config.Get()

	app := boot.NewApp(appConfig, NewWailsSink())
	boot.SetCurrent(app)
	return app.Startup()
}

func (s *AgentService) ServiceShutdown() error {
	app := boot.Current()
	if app == nil {
		return nil
	}

	return app.Shutdown()
}

// --- Project & session management API ---

// CreateProject 创建新项目（含一个默认会话，可直接开始对话）。
// 返回 ProjectView：Sessions 恰含新建的那一个默认会话。
func (s *AgentService) CreateProject() (*boot.ProjectView, error) {
	proj, sess, err := boot.Current().CreateProject()
	if err != nil {
		return nil, err
	}
	return &boot.ProjectView{Metadata: proj, Sessions: []*session.Data{sess}}, nil
}

// ListProjects 列出全部项目（含各自会话）。
func (s *AgentService) ListProjects() ([]*boot.ProjectView, error) {
	return boot.Current().ListProjects(), nil
}

// DeleteProject 删除项目（级联其下全部会话数据）。
func (s *AgentService) DeleteProject(id string) error {
	return boot.Current().DeleteProject(id)
}

// RenameProject 显式重命名项目（此后标题不再跟随会话自动命名）。
func (s *AgentService) RenameProject(id, title string) error {
	return boot.Current().RenameProject(id, title)
}

// CreateSession 在既有项目中新建会话（会话 Tab，与项目共用工作区）。
func (s *AgentService) CreateSession(projectID string) (*session.Data, error) {
	return boot.Current().CreateSession(projectID)
}

// DeleteSession 删除项目内的单个会话（真删除，清空对话记录）；
// 删除整个项目用 DeleteProject。
func (s *AgentService) DeleteSession(id string) error {
	return boot.Current().DeleteSession(id)
}

// CloseSession 关闭会话 Tab（仅视图标记：数据保留，可从已关闭列表重开）。
func (s *AgentService) CloseSession(id string) error {
	return boot.Current().CloseSession(id)
}

// OpenSession 重新打开已关闭的会话 Tab。
func (s *AgentService) OpenSession(id string) error {
	return boot.Current().OpenSession(id)
}

func (s *AgentService) RenameSession(id, title string) error {
	return boot.Current().RenameSession(id, title)
}

// --- Session queries ---

func (s *AgentService) GetSession(id string) (*session.Data, error) {
	return boot.Current().GetSession(id)
}

// --- Message operations ---

// SubmitResult 是 SubmitMessage 的返回：后端为本轮分配的两条消息 ID。
// 前端据此回填本地占位消息，DeleteMessage 等按 ID 操作无需等待会话重载。
type SubmitResult struct {
	UserMessageID      string `json:"userMessageId"`
	AssistantMessageID string `json:"assistantMessageId"`
}

// SubmitMessage submits a user message (with optional image data URLs)
// and starts the agent loop.
func (s *AgentService) SubmitMessage(sessionID, content string, images []string) (*SubmitResult, error) {
	userMsgID, assistantID, err := boot.Current().SubmitMessage(sessionID, content, images)
	if err != nil {
		return nil, err
	}
	return &SubmitResult{UserMessageID: userMsgID, AssistantMessageID: assistantID}, nil
}

// CancelMessage cancels an in-flight SubmitMessage turn.
func (s *AgentService) CancelMessage(sessionID string) error {
	return boot.Current().CancelMessage(sessionID)
}

// DeleteMessage deletes a message by ID — along with all messages after it
// (truncate semantics, matching the frontend) — and returns its index.
// Rejected while a turn is running: the message list is frozen mid-turn.
func (s *AgentService) DeleteMessage(sessionID, messageID string) (int, error) {
	return boot.Current().DeleteMessage(sessionID, messageID)
}

// RetryMessage retries the last turn (or the turn containing the given
// assistant message). It regenerates the assistant response for that turn.
// 返回新一轮 assistant 消息 ID（前端回填本地占位，同 SubmitMessage）。
func (s *AgentService) RetryMessage(sessionID string, messageID string) (string, error) {
	return boot.Current().RetryMessage(sessionID, messageID)
}

// EditMessage edits a user message in-place (no regeneration).
func (s *AgentService) EditMessage(sessionID, messageID, content string) error {
	return boot.Current().EditMessage(sessionID, messageID, content)
}

// AnswerAskUser 提交一次询问/审批的用户答复。requestID 即工具调用 ID
// （ask_user 询问或危险调用审批共用同一答复通道）。
// value：confirm 为 "confirm"/"deny"；select 为选项 id；input 为文本；
// 审批为 "allow"/"allow_always"/"deny"。reason 为可选拒绝理由。
func (s *AgentService) AnswerAskUser(requestID, value, reason string) error {
	return boot.Current().AnswerAskUser(requestID, value, reason)
}
