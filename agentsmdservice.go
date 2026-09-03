package main

import (
	"fmt"
	"os"
	"path/filepath"

	"tars/pkg/memory"
)

// AgentsMdStatus 是会话工作区的 AGENTS.md 发现状态（项目指令记忆的可发现性入口）。
type AgentsMdStatus struct {
	Exists bool   `json:"exists"`
	// Path 是 AGENTS.md 的完整路径（未找到时为预期路径，供 tooltip 展示）。
	Path string `json:"path"`
}

// GetAgentsMdStatus 报告会话工作区根是否存在 AGENTS.md。
func (s *AgentService) GetAgentsMdStatus(sessionID string) (*AgentsMdStatus, error) {
	ctrl, ok := s.app.FindController(sessionID)
	if !ok {
		return nil, fmt.Errorf("session not found: %s", sessionID)
	}
	path := filepath.Join(ctrl.GetSessionMgr().GetWorkspaceDir(), memory.AgentsFile)
	_, err := os.Stat(path)
	return &AgentsMdStatus{Exists: err == nil, Path: path}, nil
}

// agentsMdTemplate 是"创建"按钮写入的骨架模板。
const agentsMdTemplate = `# AGENTS.md

本文件为 AI Agent 提供项目级指令：每次会话自动注入上下文（位于项目根目录，建议随 git 提交）。

## 项目简介

<!-- 一句话说明这个项目是什么 -->

## 构建与测试

<!-- 例如：go build ./...；go test ./... -->

## 代码约定

<!-- 例如：提交前 gofmt -w；错误处理一律用 %w 包装 -->

## 注意事项

<!-- 例如：不要手改 generated/ 目录 -->
`

// CreateAgentsMd 在工作区根写入 AGENTS.md 骨架模板；已存在时拒绝
// （防覆盖用户内容——创建动作必须显式且幂等失败可见）。
func (s *AgentService) CreateAgentsMd(sessionID string) error {
	ctrl, ok := s.app.FindController(sessionID)
	if !ok {
		return fmt.Errorf("session not found: %s", sessionID)
	}
	path := filepath.Join(ctrl.GetSessionMgr().GetWorkspaceDir(), memory.AgentsFile)
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("AGENTS.md already exists: %s", path)
	}
	if err := os.WriteFile(path, []byte(agentsMdTemplate), 0644); err != nil {
		return fmt.Errorf("create AGENTS.md: %w", err)
	}
	return nil
}
