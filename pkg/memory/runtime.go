package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"tars/pkg/schema"
)

// WorkspaceDir 是 Runtime 所需的会话工作区读取面（session.Manager
// 天然满足）。
type WorkspaceDir interface {
	GetWorkspaceDir() string
}

// Runtime 是记忆模块的会话级视图（P1：项目指令记忆 AGENTS.md）。
// 每轮渲染时现读 workspace 根——与状态栏 cwd 行同模式：零缓存失效
// 问题，SetWorkspaceDir（零消息窗口）切换自然覆盖，8KB 读盘成本可忽略。
//
// 写入路径不在此：AGENTS.md 用普通文件工具写（write_file/edit_file），
// 特权在"注入"不在"写"。
type Runtime struct {
	ws       WorkspaceDir
	maxBytes int
}

// NewRuntime 创建会话级记忆运行时。P2 引入 Manager（进程级事实记忆
// 权威）后将改为 Manager.NewRuntime 工厂，与 skill/mcp 三层模式对齐。
func NewRuntime(ws WorkspaceDir) *Runtime {
	return &Runtime{ws: ws, maxBytes: DefaultMaxBytes}
}

func (r *Runtime) Startup() error  { return nil }
func (r *Runtime) Shutdown() error { return nil }

// RenderMemoryBlock 渲染项目指令记忆为独立 user 消息（注入位置：
// History 之后、状态栏之前——不进 system 前缀是缓存冻结纪律，user
// 角色是低权威位共识）。文件不存在/为空返回 nil（块省略）；
// 超上限按 UTF-8 安全边界截断并附 read_file 指针。
func (r *Runtime) RenderMemoryBlock() *schema.Message {
	if r.ws == nil {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(r.ws.GetWorkspaceDir(), AgentsFile))
	if err != nil {
		return nil
	}
	content := strings.TrimSpace(string(raw))
	if content == "" {
		return nil
	}
	if len(content) > r.maxBytes {
		cut := strings.ToValidUTF8(content[:r.maxBytes], "")
		content = strings.TrimSpace(cut) +
			fmt.Sprintf("\n\n…（已截断：%s 超过 %d 字节上限；完整内容请用 read_file 查看）", AgentsFile, r.maxBytes)
	}
	return &schema.Message{
		Role:    schema.RoleUser,
		Content: "<project_memory source=\"AGENTS.md\">\n" + content + "\n</project_memory>",
	}
}
