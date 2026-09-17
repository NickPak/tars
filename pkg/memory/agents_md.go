package memory

import (
	"fmt"
	"os"
	"path/filepath"
)

// AgentsMdTemplate 是"创建"按钮写入的骨架模板。
const AgentsMdTemplate = `# AGENTS.md

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

func GetAgentsMdFilePath(projectDir string) string {
	return filepath.Join(projectDir, AgentsFile)
}

func GetAgentsMdStatus(projectDir string) (string, error) {
	agentsMdFilePath := GetAgentsMdFilePath(projectDir)
	_, err := os.Stat(agentsMdFilePath)
	if err != nil {
		return agentsMdFilePath, fmt.Errorf("AGENTS.md not found: %s", agentsMdFilePath)
	}
	return agentsMdFilePath, nil
}

func CreateAgentsMd(projectDir string) error {
	agentsMdFilePath := GetAgentsMdFilePath(projectDir)
	_, err := os.Stat(agentsMdFilePath)
	if err == nil {
		return fmt.Errorf("AGENTS.md already exists: %s", agentsMdFilePath)
	}

	err = os.WriteFile(agentsMdFilePath, []byte(AgentsMdTemplate), 0644)
	if err != nil {
		return fmt.Errorf("create AGENTS.md: %w", err)
	}
	return nil
}
