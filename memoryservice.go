package main

import (
	"fmt"

	"tars/internal/boot"
	"tars/internal/project"
	"tars/pkg/memory"
)

// MemoryService —— 记忆面板 API（设置页记忆页签：事实/候选/审计）。
type MemoryService struct{}

// ProjectMemoryFacts 是一个项目级记忆的展示分组。
type ProjectMemoryFacts struct {
	ProjectID string         `json:"projectId"`
	Title     string         `json:"title"`
	Facts     []*memory.Fact `json:"facts"`
	// Candidates 是压缩联动产出的待采纳建议（采纳制：不生效，用户决定）。
	Candidates []*memory.Fact `json:"candidates"`
}

// MemoryFactsView 是设置页记忆面板的完整视图：全局记忆 + 各项目记忆。
type MemoryFactsView struct {
	Global   []*memory.Fact       `json:"global"`
	Projects []ProjectMemoryFacts `json:"projects"`
}

// ListMemoryFacts 列出全部记忆事实（全局 + 有事实的项目；含过期项——
// 面板是审计界面，过期项由前端灰显标注）。
func (s *MemoryService) ListMemoryFacts() (*MemoryFactsView, error) {
	memMgr := boot.Current().GetMemoryMgr()
	view := &MemoryFactsView{}

	global, err := memory.ListFacts(memMgr.GetGlobalMemoryDir())
	if err != nil {
		return nil, err
	}
	view.Global = global

	for _, pv := range boot.Current().ListProjects() {
		facts, err := memory.ListFacts(memMgr.GetProjectMemoryDir(pv.GetProjectDir()))
		if err != nil {
			continue
		}
		// 新项目的标题在创建时即填充（project.DefaultProjectTitle）；
		// 空标题仅存在于旧数据：退化用最近会话标题（与侧边栏推导同规则）。
		title := pv.Title
		if title == "" {
			if len(pv.Sessions) > 0 {
				title = pv.Sessions[len(pv.Sessions)-1].Title
			} else {
				title = project.DefaultProjectTitle
			}
		}
		candidates, err := memory.ListCandidates(memMgr.GetProjectMemoryDir(pv.GetProjectDir()))
		if err != nil {
			continue
		}
		if len(facts) == 0 && len(candidates) == 0 {
			continue
		}
		view.Projects = append(view.Projects, ProjectMemoryFacts{
			ProjectID:  pv.ID,
			Title:      title,
			Facts:      facts,
			Candidates: candidates,
		})
	}
	return view, nil
}

// ForgetMemoryFact 删除一条记忆（归档留痕，可恢复）。
func (s *MemoryService) ForgetMemoryFact(scope, projectID, subject string) error {
	memDir, err := boot.Current().GetMemoryDir(scope, projectID)
	if err != nil {
		return err
	}
	return memory.ForgetFact(memDir, subject)
}

// UpdateMemoryFact 编辑一条记忆的正文（旧值归档；敏感模式校验在存储层）。
func (s *MemoryService) UpdateMemoryFact(scope, projectID, subject, body string) error {
	memDir, err := boot.Current().GetMemoryDir(scope, projectID)
	if err != nil {
		return err
	}
	if err := memory.UpdateFact(memDir, subject, body); err != nil {
		return fmt.Errorf("update memory: %w", err)
	}
	return nil
}

// AdoptMemoryCandidate 采纳一条记忆候选：转为正式事实并移出候选区。
func (s *MemoryService) AdoptMemoryCandidate(projectID, subject string) error {
	memDir, err := boot.Current().GetMemoryDir(string(memory.ScopeProject), projectID)
	if err != nil {
		return err
	}
	return memory.AdoptCandidate(memDir, subject)
}

// RejectMemoryCandidate 拒绝一条记忆候选：移入拒绝审计（不再重复提议）。
func (s *MemoryService) RejectMemoryCandidate(projectID, subject string) error {
	memDir, err := boot.Current().GetMemoryDir(string(memory.ScopeProject), projectID)
	if err != nil {
		return err
	}
	return memory.RejectCandidate(memDir, subject)
}

// AdoptAllMemoryCandidates 批量采纳一个项目的全部候选（单条失败不阻断，
// 返回首个错误——面板刷新后可见剩余项）。
func (s *MemoryService) AdoptAllMemoryCandidates(projectID string) error {
	memDir, err := boot.Current().GetMemoryDir(string(memory.ScopeProject), projectID)
	if err != nil {
		return err
	}
	candidates, err := memory.ListCandidates(memDir)
	if err != nil {
		return err
	}
	for _, c := range candidates {
		if err := memory.AdoptCandidate(memDir, c.Subject); err != nil {
			return err
		}
	}
	return nil
}

// RejectAllMemoryCandidates 批量拒绝一个项目的全部候选。
func (s *MemoryService) RejectAllMemoryCandidates(projectID string) error {
	memDir, err := boot.Current().GetMemoryDir(string(memory.ScopeProject), projectID)
	if err != nil {
		return err
	}
	candidates, err := memory.ListCandidates(memDir)
	if err != nil {
		return err
	}
	for _, c := range candidates {
		if err := memory.RejectCandidate(memDir, c.Subject); err != nil {
			return err
		}
	}
	return nil
}

// MemoryAuditView 是记忆审计视图：归档（forget/覆盖旧值）+ 拒绝（候选审计）。
type MemoryAuditView struct {
	Archived []*memory.Fact `json:"archived"`
	Rejected []*memory.Fact `json:"rejected"`
}

// ListMemoryAudit 返回一个记忆根的审计视图（留痕不真删的可恢复性证明）。
func (s *MemoryService) ListMemoryAudit(scope, projectID string) (*MemoryAuditView, error) {
	root, err := boot.Current().GetMemoryDir(scope, projectID)
	if err != nil {
		return nil, err
	}
	archived, err := memory.ListArchived(root)
	if err != nil {
		return nil, err
	}
	rejected, err := memory.ListRejected(root)
	if err != nil {
		return nil, err
	}
	return &MemoryAuditView{Archived: archived, Rejected: rejected}, nil
}
