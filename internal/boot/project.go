package boot

import (
	"fmt"
	"log/slog"
	"os"
	"sync"

	"tars/internal/project"
	"tars/internal/session"
)

// ProjectView 是项目的展示视图：项目元信息 + 其下会话。
type ProjectView struct {
	*project.Metadata
	Sessions []*session.Data `json:"sessions"`
}

// Project 是项目的运行时形态：项目实体（project.Project，元数据与磁盘
// 存储的载体）+ 其下全部会话的 Controller 索引（键 = 会话 ID）。
// 一个 Project 即一组会话 Tab 的宿主；Controller 的创建/关闭/取消都
// 只经由它和 ProjectManager，App 不直接持有 Controller。
type Project struct {
	meta  *project.Metadata
	mu    sync.RWMutex
	ctrls map[string]*Controller
}

func NewProject(meta *project.Metadata) *Project {
	return &Project{
		meta:  meta,
		ctrls: make(map[string]*Controller),
	}
}

func (p *Project) GetID() string { return p.meta.GetID() }

// GetProjectDir 返回项目目录（projects/<pid>）。
func (p *Project) GetProjectDir() string { return p.meta.GetProjectDir() }

// AddController 为会话建 Controller 并登记（Startup 失败不入册）。
func (p *Project) AddController(ctrl *Controller) (*Controller, error) {
	if err := ctrl.Startup(); err != nil {
		return nil, err
	}
	p.mu.Lock()
	p.ctrls[ctrl.sessionMgr.GetID()] = ctrl
	p.mu.Unlock()
	return ctrl, nil
}

// RemoveController 摘除会话的 Controller（调用方负责 Cancel/Shutdown）。
func (p *Project) RemoveController(id string) (*Controller, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	ctrl, ok := p.ctrls[id]
	if ok {
		delete(p.ctrls, id)
	}
	return ctrl, ok
}

// FindController 按会话 ID 取本项目内的 Controller。
func (p *Project) FindController(id string) (*Controller, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	ctrl, ok := p.ctrls[id]
	return ctrl, ok
}

func (p *Project) GetMetadata() *project.Metadata {
	return p.meta
}

// GetSessions 列出项目内全部会话（按创建时间升序）。
func (p *Project) GetSessions() []*session.Data {
	p.mu.RLock()
	out := make([]*session.Data, 0, len(p.ctrls))
	for _, c := range p.ctrls {
		out = append(out, c.GetSessionMgr().GetData())
	}
	p.mu.RUnlock()
	session.SortByCreatedAt(out)
	return out
}

func (p *Project) GetSessionIDs() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	ids := make([]string, 0, len(p.ctrls))
	for id := range p.ctrls {
		ids = append(ids, id)
	}
	return ids
}

// CancelAll 取消项目内所有会话的运行轮。
func (p *Project) CancelAll() {
	p.mu.RLock()
	defer p.mu.RUnlock()
	for _, c := range p.ctrls {
		c.Cancel()
	}
}

// Shutdown 取消并关闭项目内全部会话（删项目/进程退出路径）。
func (p *Project) Shutdown() {
	p.CancelAll()
	p.mu.RLock()
	ctrls := make([]*Controller, 0, len(p.ctrls))
	for _, c := range p.ctrls {
		ctrls = append(ctrls, c)
	}
	p.mu.RUnlock()
	for _, c := range ctrls {
		if err := c.Shutdown(); err != nil {
			slog.Error("Failed to shutdown controller", "session", c.GetSessionMgr().GetID(), "error", err)
		}
	}
}

// SetWorkspaceDir 项目工作区换绑（项目级：同项目全部会话共享）。
// 守卫：项目内所有会话均为零消息且无运行中的轮——历史消息里含旧根路径，
// 改目录后模型照着旧路径操作全错而无从察觉，故锁定不迁移。
func (p *Project) SetWorkspaceDir(dir string) error {
	p.mu.RLock()
	ctrls := make([]*Controller, 0, len(p.ctrls))
	for _, c := range p.ctrls {
		ctrls = append(ctrls, c)
	}
	p.mu.RUnlock()

	for _, c := range ctrls {
		if c.IsRunning() {
			return fmt.Errorf("turn in progress, cancel it first")
		}
		if len(c.GetSessionMgr().GetData().Messages) > 0 {
			return fmt.Errorf("项目已有对话记录，工作区已锁定；如需在新目录下工作，请新建项目")
		}
	}

	// 自定义目录必须已存在（Save 会 MkdirAll 工作区——不校验就会把用户
	// 输错的路径静默创建出来）。
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("project: workspace dir not found: %s", dir)
	}

	p.meta.SetWorkspaceDir(dir)
	if err := project.GetStoreManager().Save(p.meta); err != nil {
		return err
	}

	for _, c := range ctrls {
		c.SyncWorkspaceRoot()
	}
	slog.Info("Workspace changed", "project", p.GetID(), "dir", dir)
	return nil
}

func (p *Project) SetTitle(title string) error {
	p.meta.SetTitle(title)
	err := project.GetStoreManager().Save(p.meta)
	if err != nil {
		return err
	}
	return nil
}

// FollowSessionTitle 项目自动命名（跟随会话）：会话被首条消息自动命名后，
// 若项目标题仍是创建时的默认值，则跟随该会话标题（写穿磁盘）。
// 显式命名过的项目不跟随（同 session.UpdateTitle 的"仅默认标题可自动改"）。
// 返回生效的标题与是否跟随成功（事件发射由 ProjectManager 负责）。
func (p *Project) FollowSessionTitle(sessionID string) (string, bool) {
	if p.meta.GetTitle() != project.DefaultProjectTitle {
		return "", false
	}
	ctrl, ok := p.FindController(sessionID)
	if !ok {
		return "", false
	}
	title := ctrl.GetSessionMgr().GetData().Title
	if title == "" || title == session.DefaultSessionTitle {
		return "", false
	}
	if err := p.SetTitle(title); err != nil {
		slog.Warn("Failed to follow session title", "project", p.GetID(), "error", err)
		return "", false
	}
	return title, true
}
