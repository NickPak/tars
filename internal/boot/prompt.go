package boot

import (
	"tars/pkg/mcp"
	"tars/pkg/prompt"
	"tars/pkg/schema"
	"tars/pkg/skill"
	"tars/pkg/tool/kernel"
)

type PromptCompose struct {
	baseMsg *schema.Message
	toolReg *kernel.Registry
	skillPv *skill.Runtime
	mcpPv   *mcp.Runtime
}

func NewPromptCompose(toolReg *kernel.Registry, skillPv *skill.Runtime, mcpPv *mcp.Runtime) *PromptCompose {
	return &PromptCompose{
		baseMsg: nil,
		toolReg: toolReg,
		skillPv: skillPv,
		mcpPv:   mcpPv,
	}
}

func (c *PromptCompose) Startup() error {
	c.baseMsg = prompt.BuildSystemMessage(c.toolReg.ToolNames())
	return nil
}

func (c *PromptCompose) Shutdown() error {
	return nil
}

// GetSystemMessage 构建本轮的 system 消息列表：静态提示词 + 技能索引 +
// MCP 服务器索引（后两者动态）。
// 纯函数、每轮重建，无缓存即无失效同步问题；装/卸技能、启停 MCP 服务器
// 对下一轮立即生效。顺序即缓存前缀顺序：静态提示词 → skills 索引 → MCP 索引
// （最稳定的排最前）。
func (c *PromptCompose) GetSystemMessage() []*schema.Message {
	var sys []*schema.Message
	if sm := c.baseMsg; sm != nil {
		sys = append(sys, sm)
	}

	skillSys := c.skillPv.GetSystemMessage()
	if skillSys != nil {
		sys = append(sys, skillSys)
	}
	mcpSys := c.mcpPv.GetSystemMessage()
	if mcpSys != nil {
		sys = append(sys, mcpSys)
	}
	return sys
}

func (c *PromptCompose) GetBaseMessage() *schema.Message {
	return c.baseMsg
}
