// Package boot 负责应用的组装与启动：App 是全局唯一入口，持有进程级
// 共享依赖（模型注册表/技能/持久化/工具目录），并以 Controller 为单位的
// 唯一会话索引——Controller 即会话的完整封装（见 controller.go）。
package boot

import (
	"cmp"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"sync"
	"tars/internal/config"
	"tars/internal/project"
	"tars/internal/session"
	"tars/pkg/ask"
	"tars/pkg/event"
	"tars/pkg/llm"
	"tars/pkg/mcp"
	"tars/pkg/skill"
	"tars/pkg/trace"
)

// Options 是 NewApp 的装配参数（字段来自应用配置 config.AppConfig）。
//type Options struct {
//	// WorkDir 是根目录（workspace/skills/sessions 等子目录的父）。
//	WorkDir string
//	// LLM 是模型配置（多条目 + 激活标记）。
//	LLM *llm.Config
//	// Skills 是技能配置。
//	Skills *skills.Config
//	// Sink 后端事件出口（Wails 适配器）；nil 时静默。
//	Sink event.Sink
//}

// App 是应用的全局唯一入口：进程级共享依赖 + 多会话 Controller。
// 普通对象（非单例），由服务层创建并注入。
type App struct {
	cfg      *config.AppConfig
	skillMgr *skill.Manager
	mcpMgr   *mcp.Manager
	llmMgr   *llm.Manager
	projMgr  *project.Manager
	sink     event.Sink
	askMgr   *ask.Manager
	mu       sync.RWMutex
	ctrls    map[string]*Controller
}

// NewApp 创建并连接全部共享依赖，返回就绪的 App。
// 它是"new 领域对象"的唯一入口（composition root）。
func NewApp(cfg *config.AppConfig, sink event.Sink) *App {
	return &App{
		cfg:      cfg,
		skillMgr: skill.NewManager(cfg.WorkDir, cfg.Skills),
		mcpMgr:   mcp.NewManager(cfg.WorkDir),
		projMgr:  project.NewManager(cfg.WorkDir),
		llmMgr:   llm.NewManager(cfg.LLM),
		askMgr:   ask.NewManager(),
		sink:     event.NewFanOut(sink, NewTraceSink()),
		mu:       sync.RWMutex{},
		ctrls:    make(map[string]*Controller),
	}
}

func (a *App) Startup() error {
	// 工作目录
	if err := os.MkdirAll(a.cfg.WorkDir, 0755); err != nil {
		slog.Error("Failed to create work directory", "dir", a.cfg.WorkDir, "error", err)
		return err
	}
	slog.Info("Agent work directory", "path", a.cfg.WorkDir)

	// 初始化会话存储管理器（无状态，目录参数化）
	session.InitStoreManager()

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

	// 从磁盘恢复全部会话（含各会话的 Controller）
	err = a.RestoreSessions()
	if err != nil {
		return err
	}
	return nil
}

func (a *App) Shutdown() error {
	a.CancelAll() // 先取消所有运行中的会话

	err := a.askMgr.Shutdown()
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

// --- 项目与会话生命周期 ---

// ProjectView 是项目的展示视图：项目元信息 + 其下会话。
type ProjectView struct {
	*project.Project
	Sessions []*session.Data `json:"sessions"`
}

// CreateProject 创建新项目（含一个默认会话）：持久化由 project/session
// 存储封装，这里负责 Controller 索引与创建事件。
func (a *App) CreateProject() (*project.Project, *session.Data, error) {
	proj, err := a.projMgr.Create()
	if err != nil {
		return nil, nil, err
	}
	sess, sessionDir, err := session.GetStoreManager().CreateSession(a.projMgr.DirOf(proj), proj.ID)
	if err != nil {
		return nil, nil, err
	}
	trace.LogSessionCreated(sess.ID, sess.Title)

	a.mu.Lock()
	defer a.mu.Unlock()
	ctrl := NewController(a.cfg, proj, sessionDir, sess, a.sink, a.llmMgr, a.skillMgr, a.mcpMgr, a.askMgr)
	if err := ctrl.Startup(); err != nil {
		return nil, nil, err
	}
	a.ctrls[sess.ID] = ctrl
	return proj, sess, nil
}

// CreateSession 在既有项目中创建新会话（单项目多会话：会话 Tab）。
func (a *App) CreateSession(projectID string) (*session.Data, error) {
	proj, err := a.projMgr.Get(projectID)
	if err != nil || proj == nil {
		return nil, fmt.Errorf("project not found: %s", projectID)
	}
	sess, sessionDir, err := session.GetStoreManager().CreateSession(a.projMgr.DirOf(proj), proj.ID)
	if err != nil {
		return nil, err
	}
	trace.LogSessionCreated(sess.ID, sess.Title)

	a.mu.Lock()
	defer a.mu.Unlock()
	ctrl := NewController(a.cfg, proj, sessionDir, sess, a.sink, a.llmMgr, a.skillMgr, a.mcpMgr, a.askMgr)
	if err := ctrl.Startup(); err != nil {
		return nil, err
	}
	a.ctrls[sess.ID] = ctrl
	return sess, nil
}

// DeleteSession 删除项目内的单个会话（Tab 关闭）；级联删除用 DeleteProject。
func (a *App) DeleteSession(id string) error {
	if err := a.CancelMessage(id); err != nil {
		return err
	}

	a.mu.Lock()
	ctrl, ok := a.ctrls[id]
	if ok {
		delete(a.ctrls, id)
	}
	a.mu.Unlock()
	if !ok {
		return fmt.Errorf("session not found: %s", id)
	}
	if err := ctrl.Shutdown(); err != nil {
		slog.Error("Failed to shutdown controller", "session", id, "error", err)
	}
	return session.GetStoreManager().DeleteSession(a.projMgr.DirOf(ctrl.Project()), id)
}

// ListProjects 按创建时间升序列出全部项目（含各自会话，会话按创建时间升序）。
// 以磁盘项目列表为准（目录即真相）——零会话项目也可见（空项目态），
// 不依赖 Controller 索引。
func (a *App) ListProjects() []*ProjectView {
	projects, err := a.projMgr.List()
	if err != nil {
		slog.Error("Failed to list projects", "error", err)
		return nil
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]*ProjectView, 0, len(projects))
	for _, p := range projects {
		v := &ProjectView{Project: p}
		for _, c := range a.ctrls {
			if c.Project().ID == p.ID {
				v.Sessions = append(v.Sessions, c.GetSessionMgr().GetData())
			}
		}
		session.SortByCreatedAt(v.Sessions)
		out = append(out, v)
	}
	slices.SortFunc(out, func(x, y *ProjectView) int { return cmp.Compare(x.CreatedAt, y.CreatedAt) })
	return out
}

// GetProjectWorkspaceDir 返回项目的生效工作区路径（自定义或默认目录）。
func (a *App) GetProjectWorkspaceDir(projectID string) (string, error) {
	p, err := a.projMgr.Get(projectID)
	if err != nil || p == nil {
		return "", fmt.Errorf("project not found: %s", projectID)
	}
	return p.GetWorkspaceDir(), nil
}

// RenameProject 显式重命名项目（此后标题不再跟随会话自动命名）。
// 优先改 Controller 持有的内存实例（ListProjects 的读取面），
// 零会话项目回退到磁盘副本。
func (a *App) RenameProject(id, title string) error {
	a.mu.RLock()
	var proj *project.Project
	for _, c := range a.ctrls {
		if c.Project().ID == id {
			proj = c.Project()
			break
		}
	}
	a.mu.RUnlock()

	var err error
	if proj == nil {
		proj, err = a.projMgr.Get(id)
		if err != nil {
			return err
		}
	}
	if proj == nil {
		return fmt.Errorf("project not found: %s", id)
	}
	return a.projMgr.SetTitle(proj, title)
}

// DeleteProject 删除项目：取消运行中的轮、关闭并移除其全部会话的
// Controller，最后删除磁盘数据（级联会话与默认工作区；自定义工作区
// 在用户文件系统上，不动）。
func (a *App) DeleteProject(id string) error {
	a.mu.Lock()
	var members []*Controller
	for sid, c := range a.ctrls {
		if c.Project().ID == id {
			members = append(members, c)
			delete(a.ctrls, sid)
		}
	}
	a.mu.Unlock()

	for _, c := range members {
		c.Cancel()
		if err := c.Shutdown(); err != nil {
			slog.Error("Failed to shutdown controller", "session", c.GetSessionMgr().GetID(), "error", err)
		}
	}
	return a.projMgr.Delete(id)
}

// SetSessionWorkspace 设置会话所属项目的工作区（项目级：同项目全部会话
// 共享）。守卫：项目内所有会话均为零消息且无运行中的轮——历史消息里含
// 旧根路径，改目录后模型照着旧路径操作全错而无从察觉，故锁定不迁移。
func (a *App) SetSessionWorkspace(sessionID, dir string) error {
	a.mu.RLock()
	target, ok := a.ctrls[sessionID]
	var members []*Controller
	if ok {
		for _, c := range a.ctrls {
			if c.Project().ID == target.Project().ID {
				members = append(members, c)
			}
		}
	}
	a.mu.RUnlock()
	if !ok {
		return fmt.Errorf("session not found: %s", sessionID)
	}
	proj := target.Project()

	for _, c := range members {
		if c.IsRunning() {
			return fmt.Errorf("turn in progress, cancel it first")
		}
		if len(c.GetSessionMgr().GetData().Messages) > 0 {
			return fmt.Errorf("项目已有对话记录，工作区已锁定；如需在新目录下工作，请新建项目")
		}
	}
	if err := a.projMgr.SetWorkspaceDir(proj, dir); err != nil {
		return err
	}
	for _, c := range members {
		c.SyncWorkspaceRoot()
	}
	slog.Info("Workspace changed", "project", proj.ID, "dir", dir)
	return nil
}

func (a *App) RenameSession(id, title string) error {
	c, ok := a.FindController(id)
	if !ok {
		return fmt.Errorf("session not found: %s", id)
	}
	return c.RenameSession(title)
}

func (a *App) GetSession(id string) (*session.Data, error) {
	c, ok := a.FindController(id)
	if !ok {
		return nil, fmt.Errorf("session not found: %s", id)
	}

	return c.GetSessionMgr().GetData(), nil
}

// RestoreSessions 从磁盘恢复全部项目及其会话并依次建 Controller。
// 进程启动时调用一次；恢复不是创建，不产生 session.created span。
func (a *App) RestoreSessions() error {
	projects, err := a.projMgr.List()
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, proj := range projects {
		projDir := a.projMgr.DirOf(proj)
		infos, err := session.GetStoreManager().LoadProjectSessionData(projDir)
		if err != nil {
			slog.Error("Failed to load project sessions", "project", proj.ID, "error", err)
			continue
		}
		if len(infos) == 0 {
			// 暂存项目清理：零会话 + 无自定义工作区 + 默认工作区为空 →
			// 删除（空项目只可能来自 Tab 全关/崩溃残留；有产出物或自定义
			// 工作区的项目保留——用户数据不擅自清理）。
			if swept, sErr := a.projMgr.SweepIfEmpty(proj); sErr != nil {
				slog.Warn("Failed to sweep empty project", "project", proj.ID, "error", sErr)
			} else if swept {
				slog.Info("Swept empty project", "project", proj.ID)
			}
			continue
		}
		for _, sess := range infos {
			ctrl := NewController(a.cfg, proj, session.GetSessionDir(projDir, sess.ID), sess, a.sink, a.llmMgr, a.skillMgr, a.mcpMgr, a.askMgr)
			if err := ctrl.Startup(); err != nil {
				slog.Error("Failed to startup controller", "session", sess.ID, "error", err)
				continue
			}
			a.ctrls[sess.ID] = ctrl
		}
	}
	return nil
}

// FindSession 按 ID 取会话。
func (a *App) FindSession(id string) (*session.Data, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	c, ok := a.ctrls[id]
	if !ok {
		return nil, false
	}
	return c.GetSessionMgr().GetData(), true
}

// HasSession 报告会话是否存在。
func (a *App) HasSession(id string) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	_, ok := a.ctrls[id]
	return ok
}

// CancelAll 取消所有会话的运行轮（退出前调用）。
func (a *App) CancelAll() {
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, c := range a.ctrls {
		c.Cancel()
	}
}

// --- 轮生命周期入口（服务层使用） ---

// SubmitMessage 提交一条用户消息并启动一轮对话（委托给会话的 Controller）。
// 返回后端分配的 user/assistant 消息 ID。
func (a *App) SubmitMessage(sessionID, content string) (string, string, error) {
	c, ok := a.FindController(sessionID)
	if !ok {
		return "", "", fmt.Errorf("session not found: %s", sessionID)
	}
	return c.SubmitMessage(content)
}

// CancelMessage 取消会话当前运行的轮（无运行中的轮则无事发生）。
func (a *App) CancelMessage(sessionID string) error {
	if c, ok := a.FindController(sessionID); ok {
		c.Cancel()
	}
	return nil
}

func (a *App) DeleteMessage(sessionID, messageID string) (int, error) {
	c, ok := a.FindController(sessionID)
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
	c, ok := a.FindController(sessionID)
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
	sess, ok := a.FindSession(sessionID)
	if !ok {
		return fmt.Errorf("session not found: %s", sessionID)
	}
	return sess.EditUserMessage(messageID, content)
}

// FindController 按会话 ID 取 Controller（Restore/Create 保证索引总是全的）。
func (a *App) FindController(id string) (*Controller, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	c, ok := a.ctrls[id]
	return c, ok
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

	// MCP 服务器配置不在此流：由 mcp.Manager 自管（skillservice.go 的
	// Upsert/Remove/SetEnabled 即改即存，与技能同生命周期）。
	return nil
}
