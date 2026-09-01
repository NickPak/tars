// Package sandbox 是执行环境抽象（见 plan/tools/04-sandbox.md）：
// 模型能触达的文件面与命令面，可替换实现（native/docker/vm/远程）。
//
// 本包零项目依赖：不认识工具、策略、会话。工具经 Carrier 构造注入
// 本包接口（FileTools 消费 FileSystem，Shell 消费 Executor，
// code_interpreter 消费组合 Sandbox），Registry 对沙箱零感知。
//
// 路径语义：FileSystem 的路径是工具视角——workspace 根相对路径
// （或根内绝对路径），边界约束由实现保证；Executor 的 Dir 是环境
// 原生路径（空 = 实现的默认工作根）。
package sandbox

import (
	"context"
	"time"
)

// FileSystem 是执行环境的文件操作面。
type FileSystem interface {
	ReadFile(path string) ([]byte, error)
	WriteFile(path string, data []byte) error
	MkdirAll(path string) error
	// ReadDir 返回目录项（不递归；顺序由实现定义）。
	ReadDir(path string) ([]DirEntry, error)
	Stat(path string) (FileInfo, error)
	Remove(path string) error
	// Root 返回 workspace 根的环境原生路径（展示/拼接命令参数用）。
	Root() string
}

// DirEntry 是一个目录项。
type DirEntry struct {
	Name  string
	IsDir bool
	Size  int64
}

// FileInfo 是单个路径的状态。
type FileInfo struct {
	IsDir bool
	Size  int64
}

// ShellKind 是执行环境的 shell 方言（持久终端的状态转储语法依赖它）。
type ShellKind string

const (
	// ShellSh 是 POSIX sh 方言（macOS/Linux）。
	ShellSh ShellKind = "sh"
	// ShellCmd 是 Windows cmd 方言。
	ShellCmd ShellKind = "cmd"
	// ShellPwsh 是 PowerShell 7+（pwsh，github.com/PowerShell/PowerShell）。
	// Windows 上装了 pwsh 即优先于 cmd：cmd 表达能力太弱。
	// 不回退 Windows 自带的 powershell.exe 5.1——&& 链式运算符是 PS 7.0
	// 才支持的，而 run_command 的工具描述承诺了 && 语法。
	ShellPwsh ShellKind = "pwsh"
)

// Executor 是执行环境的命令执行面。无状态：每次调用独立执行。
type Executor interface {
	Exec(ctx context.Context, req ExecRequest) (*ExecResult, error)
	ShellKind() ShellKind
	// Root 返回默认工作根（环境原生路径；native 为宿主路径、
	// docker 为容器内挂载点）。Shell 用它初始化持久终端的初始 cwd；
	// 工作根恒定（仅会话零消息窗口内可变），不存在按 root 分桶的需求。
	Root() string
}

// ExecRequest 是一次命令执行。
type ExecRequest struct {
	Command string
	// Dir 工作目录（环境原生路径；空 = 实现的默认工作根）。
	Dir string
	// Env 完整环境快照（非增量）；nil = 继承实现的默认环境。
	Env []string
	// Timeout 执行超时；<=0 表示不额外限制（跟随 ctx）。
	Timeout time.Duration
}

// ExecResult 是一次命令执行的结果。
type ExecResult struct {
	// Output 是 stdout+stderr 按到达顺序合流的输出——工具面向模型
	// 呈现的就是合流文本，分离会丢失交错时序（对调试至关重要）。
	Output   string
	ExitCode int  // 非零表示命令失败；进程被信号终止时为 -1
	TimedOut bool // 因 Timeout 被杀
}

// SandboxProvider 是组合执行环境（code_interpreter 消费；也是装配层的构造单位）。
type SandboxProvider interface {
	Startup() error
	Shutdown() error
	FileSystem
	Executor
	Close() error // 会话销毁时回收；native 为 no-op
}
