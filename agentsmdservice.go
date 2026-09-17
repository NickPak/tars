package main

import (
	"fmt"
	"tars/internal/boot"
	"tars/pkg/memory"
)

// AgentsMDService —— AGENTS.md 项目指令记忆的可发现性入口（状态查询与骨架创建）。
type AgentsMDService struct{}

// AgentsMdStatus 是项目工作区的 AGENTS.md 发现状态（项目指令记忆的可发现性入口）。
type AgentsMdStatus struct {
	Exists bool `json:"exists"`
	// Path 是 AGENTS.md 的完整路径（未找到时为预期路径，供 tooltip 展示）。
	Path string `json:"path"`
}

// GetAgentsMdStatus 报告项目工作区根是否存在 AGENTS.md。
// 层级表达：sessionID → FindProject 定位所属 Project → GetWorkspaceDir。
func (s *AgentsMDService) GetAgentsMdStatus(sessionID string) (*AgentsMdStatus, error) {
	proj, ok := boot.GetApp().FindProject(sessionID)
	if !ok {
		return nil, fmt.Errorf("session not found: %s", sessionID)
	}
	path, err := memory.GetAgentsMdStatus(proj.GetWorkspaceDir())
	return &AgentsMdStatus{Exists: err == nil, Path: path}, nil
}

// CreateAgentsMd 在项目工作区根写入 AGENTS.md 骨架模板；已存在时拒绝
// （防覆盖用户内容——创建动作必须显式且幂等失败可见）。
func (s *AgentsMDService) CreateAgentsMd(sessionID string) error {
	proj, ok := boot.GetApp().FindProject(sessionID)
	if !ok {
		return fmt.Errorf("session not found: %s", sessionID)
	}
	return memory.CreateAgentsMd(proj.GetWorkspaceDir())
}
