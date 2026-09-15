package boot

import (
	"cmp"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"tars/internal/project"

	"tars/internal/config"
	"tars/internal/session"
	"tars/pkg/ask"
	"tars/pkg/event"
	"tars/pkg/llm"
	"tars/pkg/mcp"
	"tars/pkg/memory"
	"tars/pkg/skill"
	"tars/pkg/trace"

	"github.com/google/uuid"
)

// ProjectManager 管理全部运行时 Project：磁盘真相委托 project.Manager
// （store），本结构维护运行时索引（projects 按项目 ID；bySession 按会话
// ID 反查所属项目）并负责组装 Project 与 Controller。
// App 对项目/会话/Controller 的全部操作只经过这里。
type ProjectManager struct {
	cfg       *config.AppConfig
	sink      event.Sink
	llmMgr    *llm.Manager
	skillMgr  *skill.Manager
	mcpMgr    *mcp.Manager
	memMgr    *memory.Manager
	askMgr    *ask.Manager
	mu        sync.RWMutex
	projects  map[string]*Project
	bySession map[string]*Project
}

func NewProjectManager(cfg *config.AppConfig, sink event.Sink, llmMgr *llm.Manager, skillMgr *skill.Manager, mcpMgr *mcp.Manager, memMgr *memory.Manager, askMgr *ask.Manager) *ProjectManager {
	return &ProjectManager{
		cfg:       cfg,
		sink:      sink,
		llmMgr:    llmMgr,
		skillMgr:  skillMgr,
		mcpMgr:    mcpMgr,
		memMgr:    memMgr,
		askMgr:    askMgr,
		mu:        sync.RWMutex{},
		projects:  make(map[string]*Project),
		bySession: make(map[string]*Project),
	}
}

func (m *ProjectManager) Startup() error {
	// 初始化项目存储管理器
	project.InitStoreManager(m.cfg.WorkDir)
	err := project.GetStoreManager().Startup()
	if err != nil {
		return fmt.Errorf("boot: project store manager: %w", err)
	}

	// 初始化会话存储管理器（无状态，目录参数化）
	session.InitStoreManager()
	err = session.GetStoreManager().Startup()
	if err != nil {
		return fmt.Errorf("boot: session store manager: %w", err)
	}

	// 从磁盘恢复全部项目及会话（含各会话的 Controller）
	return m.Restore()
}

func (m *ProjectManager) Shutdown() error {
	m.CancelAll() // 先取消所有运行中的会话

	err := session.GetStoreManager().Shutdown()
	if err != nil {
		slog.Error("Failed to shutdown session store manager", "error", err)
		return err
	}

	err = project.GetStoreManager().Shutdown()
	if err != nil {
		slog.Error("Failed to shutdown project store manager", "error", err)
		return err
	}

	return nil
}

// Register 登记运行时项目及其会话反查索引（Restore/Create 路径）。
// 调用方需已持 m.mu 写锁，或处于启动单线程阶段。
func (m *ProjectManager) Register(p *Project) {
	m.projects[p.GetID()] = p
	for _, sid := range p.GetSessionIDs() {
		m.bySession[sid] = p
	}
}

// Create 创建新项目（含一个默认会话）：project.json 落盘 + 默认工作区
// 急式创建由 store 封装，这里负责运行时索引与创建事件。
func (m *ProjectManager) Create() (*Project, *session.Data, error) {
	meta := project.NewMetaData(m.cfg.WorkDir, uuid.NewString())
	// meta 必须先落盘：StoreManager.List 以目录扫描为真相，跳过无 meta 的
	// 目录——不落盘的项目重启后连同会话一起不可恢复。
	if err := project.GetStoreManager().Save(meta); err != nil {
		return nil, nil, err
	}
	p := NewProject(meta)
	m.mu.Lock()
	m.projects[p.GetID()] = p
	m.mu.Unlock()

	sess, err := m.CreateSession(p.GetID())
	if err != nil {
		return nil, nil, err
	}
	return p, sess, nil
}

// Get 按项目 ID 取运行时项目。
func (m *ProjectManager) Get(id string) (*Project, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.projects[id]
	return p, ok
}

// CreateSession 在既有项目中创建新会话（单项目多会话：会话 Tab）。
func (m *ProjectManager) CreateSession(projectID string) (*session.Data, error) {
	p, ok := m.Get(projectID)
	if !ok {
		return nil, fmt.Errorf("project not found: %s", projectID)
	}

	sess, sessionDir, err := session.GetStoreManager().CreateSession(p.GetProjectDir(), p.GetID())
	if err != nil {
		return nil, err
	}
	ctrl := NewController(m.cfg, p.meta, sessionDir, sess, m.sink, m.llmMgr, m.skillMgr, m.mcpMgr, m.memMgr, m.askMgr, p.GetTodoMgr())
	if _, err := p.AddController(ctrl); err != nil {
		return nil, err
	}
	trace.LogSessionCreated(sess.ID, sess.Title)

	m.mu.Lock()
	m.bySession[sess.ID] = p
	m.mu.Unlock()
	return sess, nil
}

// CloseSession 关闭会话 Tab（仅视图标记：Controller 保留在内存，
// 会话数据保留在磁盘；重开用 OpenSession，真删除用 DeleteSession）。
func (m *ProjectManager) CloseSession(sessionID string) error {
	ctrl, ok := m.FindController(sessionID)
	if !ok {
		return fmt.Errorf("session not found: %s", sessionID)
	}
	ctrl.Cancel() // Tab 已不可见，进行中的轮失去观察者，终止
	return ctrl.SetClosed(true)
}

// OpenSession 重新打开已关闭的会话 Tab（清除关闭标记并写穿 meta）。
func (m *ProjectManager) OpenSession(sessionID string) error {
	ctrl, ok := m.FindController(sessionID)
	if !ok {
		return fmt.Errorf("session not found: %s", sessionID)
	}
	return ctrl.SetClosed(false)
}

// DeleteSession 删除项目内的单个会话（真删除：摘除 Controller 并清除
// 磁盘数据）；级联删除用 Delete。
func (m *ProjectManager) DeleteSession(sessionID string) error {
	m.mu.RLock()
	p, ok := m.bySession[sessionID]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("session not found: %s", sessionID)
	}

	ctrl, ok := p.RemoveController(sessionID)
	if !ok {
		return fmt.Errorf("session not found: %s", sessionID)
	}
	m.mu.Lock()
	delete(m.bySession, sessionID)
	m.mu.Unlock()

	ctrl.Cancel()
	if err := ctrl.Shutdown(); err != nil {
		slog.Error("Failed to shutdown controller", "session", sessionID, "error", err)
	}
	return session.GetStoreManager().DeleteSession(p.GetProjectDir(), sessionID)
}

// List 按创建时间升序列出全部项目的展示视图（含各自会话，会话按创建
// 时间升序）。运行时索引即真相（Restore 已将磁盘项目全量登记）——
// 零会话项目也可见（空项目态）。
func (m *ProjectManager) List() []*ProjectView {
	m.mu.RLock()
	out := make([]*ProjectView, 0, len(m.projects))
	for _, p := range m.projects {
		out = append(out, &ProjectView{Metadata: p.GetMetadata(), Sessions: p.GetSessions()})
	}
	m.mu.RUnlock()
	slices.SortFunc(out, func(x, y *ProjectView) int { return cmp.Compare(x.CreatedAt, y.CreatedAt) })
	return out
}

// GetWorkspaceDir 返回项目的生效工作区路径（自定义或默认目录）。
func (m *ProjectManager) GetWorkspaceDir(projectID string) (string, error) {
	p, ok := m.Get(projectID)
	if !ok {
		return "", fmt.Errorf("project not found: %s", projectID)
	}
	return p.GetMetadata().GetWorkspaceDir(), nil
}

// GetProjectDir 返回项目目录（projects/<pid>）。
func (m *ProjectManager) GetProjectDir(projectID string) (string, error) {
	p, ok := m.Get(projectID)
	if !ok {
		return "", fmt.Errorf("project not found: %s", projectID)
	}
	return p.GetProjectDir(), nil
}

// Rename 显式重命名项目（此后标题不再跟随会话自动命名；写穿磁盘 +
// project:renamed 事件，前端列表即时刷新）。
func (m *ProjectManager) Rename(id, title string) error {
	p, ok := m.Get(id)
	if !ok {
		return fmt.Errorf("project not found: %s", id)
	}
	if err := p.SetTitle(title); err != nil {
		return err
	}
	m.sink.Emit(event.Event{
		Kind:           event.KindProjectRenamed,
		ProjectRenamed: &event.ProjectRenamedEvent{ProjectID: id, Title: title},
	})
	return nil
}

// FollowSessionTitle 项目自动命名（跟随会话）：会话被首条消息自动命名后
// 由 SubmitMessage 路径调用；仅默认标题的项目跟随（规则见
// Project.FollowSessionTitle），跟随成功发 project:renamed 事件。
func (m *ProjectManager) FollowSessionTitle(sessionID string) {
	m.mu.RLock()
	p, ok := m.bySession[sessionID]
	m.mu.RUnlock()
	if !ok {
		return
	}
	title, followed := p.FollowSessionTitle(sessionID)
	if !followed {
		return
	}
	m.sink.Emit(event.Event{
		Kind:           event.KindProjectRenamed,
		ProjectRenamed: &event.ProjectRenamedEvent{ProjectID: p.GetID(), Title: title},
	})
}

// Delete 删除项目：取消运行中的轮、关闭并移除其全部会话的 Controller，
// 最后删除磁盘数据（级联会话与默认工作区；自定义工作区在用户文件系统
// 上，不动）。
func (m *ProjectManager) Delete(id string) error {
	m.mu.Lock()
	p, ok := m.projects[id]
	if ok {
		delete(m.projects, id)
		for _, sid := range p.GetSessionIDs() {
			delete(m.bySession, sid)
		}
	}
	m.mu.Unlock()

	if ok {
		p.Shutdown()
	}
	return project.GetStoreManager().Delete(id)
}

// SetSessionWorkspace 设置会话所属项目的工作区（守卫见 Project.SetWorkspace）。
func (m *ProjectManager) SetSessionWorkspace(sessionID, dir string) error {
	m.mu.RLock()
	p, ok := m.bySession[sessionID]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("session not found: %s", sessionID)
	}
	return p.SetWorkspaceDir(dir)
}

// FindController 按会话 ID 取 Controller（Restore/Create 保证索引总是全的）。
func (m *ProjectManager) FindController(sessionID string) (*Controller, bool) {
	m.mu.RLock()
	p, ok := m.bySession[sessionID]
	m.mu.RUnlock()
	if !ok {
		return nil, false
	}
	return p.FindController(sessionID)
}

// FindSession 按 ID 取会话。
func (m *ProjectManager) FindSession(id string) (*session.Data, bool) {
	ctrl, ok := m.FindController(id)
	if !ok {
		return nil, false
	}
	return ctrl.GetSessionMgr().GetData(), true
}

// HasSession 报告会话是否存在。
func (m *ProjectManager) HasSession(id string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.bySession[id]
	return ok
}

// CancelAll 取消所有项目所有会话的运行轮（退出前调用）。
func (m *ProjectManager) CancelAll() {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, p := range m.projects {
		p.CancelAll()
	}
}

// Restore 从磁盘恢复全部项目及其会话并依次建 Controller。
// 进程启动时调用一次；恢复不是创建，不产生 session.created span。
func (m *ProjectManager) Restore() error {
	metas, err := project.GetStoreManager().List()
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	for _, meta := range metas {
		p := NewProject(meta)
		infos, err := session.GetStoreManager().LoadProjectSessionData(p.GetProjectDir())
		if err != nil {
			slog.Error("Failed to load project sessions", "project", p.GetID(), "error", err)
			m.Register(p) // 项目本体仍在：登记为（暂时的）零会话项目，保持可见
			continue
		}

		for _, sess := range infos {
			sessionDir := session.GetSessionDir(p.GetProjectDir(), sess.ID)
			ctrl := NewController(m.cfg, p.meta, sessionDir, sess, m.sink, m.llmMgr, m.skillMgr, m.mcpMgr, m.memMgr, m.askMgr, p.GetTodoMgr())
			if _, err := p.AddController(ctrl); err != nil {
				slog.Error("Failed to startup controller", "session", sess.ID, "error", err)
				continue
			}
		}
		m.Register(p)
	}
	return nil
}
