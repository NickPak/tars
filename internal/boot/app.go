// Package boot 负责应用的组装与启动：App 是全局唯一入口，持有进程级
// 共享依赖（模型注册表/技能/持久化/工具目录），并以 ProjectManager 为
// 项目/会话/Controller 生命周期的唯一操作面——层次为
// App → ProjectManager → Project（ctrls）→ Controller（一个会话的完整
// 封装，见 controller.go / project.go）。
package boot

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"tars/internal/config"
	"tars/internal/project"
	"tars/internal/session"
	"tars/pkg/ask"
	"tars/pkg/event"
	"tars/pkg/llm"
	"tars/pkg/mcp"
	"tars/pkg/memory"
	"tars/pkg/skill"
	"tars/pkg/trace"
)

// App 是应用的全局唯一入口：进程级共享依赖 + 项目/会话运行时（委托
// ProjectManager，App 不直接持有 Controller）。
// 普通对象（非单例），由服务层创建并注入。
type App struct {
	cfg      *config.AppConfig
	skillMgr *skill.Manager
	mcpMgr   *mcp.Manager
	llmMgr   *llm.Manager
	projMgr  *ProjectManager
	memMgr   *memory.Manager
	sink     event.Sink
	askMgr   *ask.Manager
}

// NewApp 创建并连接全部共享依赖，返回就绪的 App。
// 它是"new 领域对象"的唯一入口（composition root）。
func NewApp(cfg *config.AppConfig, sink event.Sink) *App {
	a := &App{
		cfg:      cfg,
		skillMgr: skill.NewManager(cfg.WorkDir, cfg.Skills),
		mcpMgr:   mcp.NewManager(cfg.WorkDir),
		llmMgr:   llm.NewManager(cfg.LLM),
		projMgr:  nil,
		memMgr:   memory.NewManager(cfg.WorkDir, cfg.Memory),
		sink:     event.NewFanOut(sink, NewTraceSink()),
		askMgr:   ask.NewManager(),
	}
	return a
}

func (a *App) Startup() error {
	// 工作目录
	if err := os.MkdirAll(a.cfg.WorkDir, 0755); err != nil {
		slog.Error("Failed to create work directory", "dir", a.cfg.WorkDir, "error", err)
		return err
	}
	slog.Info("Agent work directory", "path", a.cfg.WorkDir)

	// 启动技能管理器
	err := a.skillMgr.Startup()
	if err != nil {
		return fmt.Errorf("boot: skills index: %w", err)
	}

	// 启动 MCP 管理器
	err = a.mcpMgr.Startup()
	if err != nil {
		return fmt.Errorf("boot: mcp manager: %w", err)
	}
	a.mcpMgr.RenderIndex()

	// 启动记忆管理器（全局记忆根初始化 + 索引重建对齐磁盘真相）
	err = a.memMgr.Startup()
	if err != nil {
		return fmt.Errorf("boot: memory manager: %w", err)
	}

	// 零模型条目是合法状态（用户可能在设置中清空了全部模型）：启动容忍，
	// 错误延迟到对话时经 Active() 暴露，用户可在设置页修复配置。
	err = a.llmMgr.Startup()
	if err != nil {
		slog.Warn("LLM registry startup degraded, continuing", "error", err)
	}

	err = a.askMgr.Startup()
	if err != nil {
		return fmt.Errorf("boot: ask registry: %w", err)
	}

	// 追踪器（进程级基础设施，OTLP 连接池 + 批量导出器）
	trace.InitTrace(a.cfg.Trace)

	a.projMgr = NewProjectManager(a.cfg, a.sink, a.llmMgr, a.skillMgr, a.mcpMgr, a.memMgr, a.askMgr)
	err = a.projMgr.Startup()
	if err != nil {
		return fmt.Errorf("boot: project manager: %w", err)
	}

	return nil
}

func (a *App) Shutdown() error {
	err := a.projMgr.Shutdown()
	if err != nil {
		slog.Error("Failed to shutdown project manager", "error", err)
		return err
	}

	err = a.askMgr.Shutdown()
	if err != nil {
		slog.Error("Failed to shutdown ask manager", "error", err)
		return err
	}

	err = a.llmMgr.Shutdown()
	if err != nil {
		slog.Error("Failed to shutdown llm manager", "error", err)
		return err
	}

	err = a.mcpMgr.Shutdown()
	if err != nil {
		slog.Error("Failed to shutdown MCP manager", "error", err)
		return err
	}

	err = a.skillMgr.Shutdown()
	if err != nil {
		slog.Error("Failed to shutdown skill manager", "error", err)
		return err
	}

	trace.Shutdown() // 关闭全局 OTLP 导出器
	return nil
}

// --- 依赖访问器（服务层使用） ---

// GetLLMMgr 返回模型注册表。
func (a *App) GetLLMMgr() *llm.Manager { return a.llmMgr }

// GetSkillMgr 返回技能管理器。
func (a *App) GetSkillMgr() *skill.Manager { return a.skillMgr }

// GetMCPMgr 返回 MCP 管理器。
func (a *App) GetMCPMgr() *mcp.Manager { return a.mcpMgr }

// GetMemoryMgr 返回记忆管理器。
func (a *App) GetMemoryMgr() *memory.Manager { return a.memMgr }

// GetMemoryDir 解析记忆范围对应的根目录："global" → 全局用户记忆；
// "project" → 指定项目级记忆（projectID 必须存在）。
func (a *App) GetMemoryDir(scope, projectID string) (string, error) {
	if scope == string(memory.ScopeGlobal) {
		return a.memMgr.GetGlobalMemoryDir(), nil
	}
	dir, err := a.projMgr.GetProjectDir(projectID)
	if err != nil {
		return "", err
	}
	return a.memMgr.GetProjectMemoryDir(dir), nil
}

// --- 项目与会话生命周期（全部委托 ProjectManager） ---

// CreateProject 创建新项目（含一个默认会话）。
func (a *App) CreateProject() (*project.Metadata, *session.Data, error) {
	p, sess, err := a.projMgr.Create()
	if err != nil {
		return nil, nil, err
	}
	return p.GetMetadata(), sess, nil
}

// CreateSession 在既有项目中创建新会话（单项目多会话：会话 Tab）。
func (a *App) CreateSession(projectID string) (*session.Data, error) {
	return a.projMgr.CreateSession(projectID)
}

// DeleteSession 删除项目内的单个会话（真删除）；级联删除用 DeleteProject。
func (a *App) DeleteSession(id string) error {
	return a.projMgr.DeleteSession(id)
}

// CloseSession 关闭会话 Tab（仅视图标记；重开用 OpenSession）。
func (a *App) CloseSession(id string) error {
	return a.projMgr.CloseSession(id)
}

// OpenSession 重新打开已关闭的会话 Tab。
func (a *App) OpenSession(id string) error {
	return a.projMgr.OpenSession(id)
}

// ListProjects 按创建时间升序列出全部项目（含各自会话）。
func (a *App) ListProjects() []*ProjectView {
	return a.projMgr.List()
}

// GetProjectWorkspaceDir 返回项目的生效工作区路径（自定义或默认目录）。
func (a *App) GetProjectWorkspaceDir(projectID string) (string, error) {
	return a.projMgr.GetWorkspaceDir(projectID)
}

// RenameProject 显式重命名项目（此后标题不再跟随会话自动命名）。
func (a *App) RenameProject(id, title string) error {
	return a.projMgr.Rename(id, title)
}

// DeleteProject 删除项目：级联关闭并删除其全部会话。
func (a *App) DeleteProject(id string) error {
	return a.projMgr.Delete(id)
}

// SetSessionWorkspace 设置会话所属项目的工作区（项目级：同项目全部会话
// 共享；守卫见 Project.SetWorkspace）。
func (a *App) SetSessionWorkspace(sessionID, dir string) error {
	return a.projMgr.SetSessionWorkspace(sessionID, dir)
}

func (a *App) RenameSession(id, title string) error {
	c, ok := a.projMgr.FindController(id)
	if !ok {
		return fmt.Errorf("session not found: %s", id)
	}
	return c.RenameSession(title)
}

func (a *App) GetSession(id string) (*session.Data, error) {
	sess, ok := a.projMgr.FindSession(id)
	if !ok {
		return nil, fmt.Errorf("session not found: %s", id)
	}
	return sess, nil
}

// FindSession 按 ID 取会话。
func (a *App) FindSession(id string) (*session.Data, bool) {
	return a.projMgr.FindSession(id)
}

// HasSession 报告会话是否存在。
func (a *App) HasSession(id string) bool {
	return a.projMgr.HasSession(id)
}

// FindController 按会话 ID 取 Controller。
func (a *App) FindController(id string) (*Controller, bool) {
	return a.projMgr.FindController(id)
}

// --- 轮生命周期入口（服务层使用） ---

// SubmitMessage 提交一条用户消息并启动一轮对话（委托给会话的 Controller）。
// 返回后端分配的 user/assistant 消息 ID。
func (a *App) SubmitMessage(sessionID, content string) (string, string, error) {
	c, ok := a.projMgr.FindController(sessionID)
	if !ok {
		return "", "", fmt.Errorf("session not found: %s", sessionID)
	}
	uid, aid, err := c.SubmitMessage(content)
	if err != nil {
		return "", "", err
	}
	// 首条消息已完成会话自动命名：默认标题的项目跟随（显式命名不跟随）。
	a.projMgr.FollowSessionTitle(sessionID)
	return uid, aid, nil
}

// CancelMessage 取消会话当前运行的轮（无运行中的轮则无事发生）。
func (a *App) CancelMessage(sessionID string) error {
	if c, ok := a.projMgr.FindController(sessionID); ok {
		c.Cancel()
	}
	return nil
}

func (a *App) DeleteMessage(sessionID, messageID string) (int, error) {
	c, ok := a.projMgr.FindController(sessionID)
	if !ok {
		return -1, fmt.Errorf("session not found: %s", sessionID)
	}
	if c.IsRunning() {
		return -1, fmt.Errorf("turn in progress, cancel it first")
	}
	return c.GetSessionMgr().DeleteFrom(messageID)
}

// RetryMessage 重试一轮对话（委托给会话的 Controller）。返回新一轮 assistant 消息 ID。
func (a *App) RetryMessage(sessionID, messageID string) (string, error) {
	c, ok := a.projMgr.FindController(sessionID)
	if !ok {
		return "", fmt.Errorf("session not found: %s", sessionID)
	}
	return c.Retry(messageID)
}

// AnswerAskUser 提交一次询问/审批的用户答复。requestID 即工具调用 ID
// （ask_user 询问或危险调用审批共用同一答复通道）。
// value：confirm 为 "confirm"/"deny"；select 为选项 id；input 为文本；
// 审批为 "allow"/"allow_always"/"deny"。reason 为可选拒绝理由。
func (a *App) AnswerAskUser(requestID, value, reason string) error {
	answer := &ask.Answer{Value: value, Reason: reason, Source: "user"}
	if !a.askMgr.Resolve(requestID, answer) {
		return fmt.Errorf("question not found or already resolved: %s", requestID)
	}
	return nil
}

func (a *App) EditMessage(sessionID, messageID, content string) error {
	sess, ok := a.projMgr.FindSession(sessionID)
	if !ok {
		return fmt.Errorf("session not found: %s", sessionID)
	}
	return sess.EditUserMessage(messageID, content)
}

func (a *App) SaveAppConfig(v *config.AppConfig) error {
	if v == nil {
		return errors.New("config is nil")
	}

	// 校验并修正（默认值填充 + LLM 结构校验，逻辑在配置结构自身）
	err := v.Validate()
	if err != nil {
		return err
	}

	// 先热更新注册表：UpdateConfig 会预构建激活模型，配置无效则
	// 整体不落盘、不生效，保持现状。
	err = a.llmMgr.UpdateConfig(v.LLM)
	if err != nil {
		slog.Warn("Failed to update llm config", "error", err)
		return err
	}

	err = config.SaveAppConfigFile(v)
	if err != nil {
		slog.Warn("Failed to save config file", "error", err)
		return err
	}
	config.Set(v)

	// 追踪配置（开关/端点）可能已变：重建全局 tracer
	trace.Rebuild(v.Trace)

	// 技能索引档位阈值可能已变：更新并重建索引（下一次对话立即生效）
	err = a.skillMgr.UpdateConfig(v.Skills)
	if err != nil {
		slog.Warn("Failed to update skills config", "error", err)
		return err
	}

	// 记忆配置（开关/索引上限）可能已变：原子替换（下一轮对话生效）
	err = a.memMgr.UpdateConfig(v.Memory)
	if err != nil {
		slog.Warn("Failed to update memory config", "error", err)
		return err
	}

	// MCP 服务器配置不在此流：由 mcp.Manager 自管（skillservice.go 的
	// Upsert/Remove/SetEnabled 即改即存，与技能同生命周期）。
	return nil
}
