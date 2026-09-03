// Package memory 是跨会话记忆能力包（plan/agent-memory-design-plan.md）。
// 三层架构：第 1 层指令记忆（AGENTS.md，人写）→ 第 2 层会话记忆（压缩
// 归档，已有）→ 第 3 层事实记忆（remember 工具，P2）。
// 当前只实现 P1：项目指令记忆的发现与注入（Runtime 会话级视图）；
// 进程级 Manager（事实记忆磁盘权威 + 工厂）随 P2 引入。
package memory

const (
	// AgentsFile 是项目指令记忆的约定文件名（行业公约，存在于工作区
	// 根即生效——格式上不发明新东西，第三方仓库可携带）。
	AgentsFile = "AGENTS.md"

	// DefaultMaxBytes 是项目指令记忆的注入上限；超出截断并附
	// read_file 指针（防超大文件撑爆视图）。
	DefaultMaxBytes = 8 * 1024
)
