package agent

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"tars/pkg/schema"
	"tars/pkg/todo"
	"tars/pkg/tool/toolkit"
)

type SkillStatus interface {
	Loaded() []string
}

type MCPStatus interface {
	Loaded() []string
}

type TodoStatus interface {
	Snapshot() ([]todo.Todo, int64)
}

// StatusBar 是 Agent 循环的状态栏：在每轮迭代前渲染一条 <agent_status>
// 消息追加到上下文，让模型看到准确的运行时事实与执行计数。
//
// 设计要点（见设计文档 2.10）：
//   - 状态栏消息只在内存中存在，不进 IterationEnd delta，宿主不会持久化；
//   - 静态信息（OS/shell/Python）New 时初始化一次，进程内不变；
//   - 动态信息（time/git/cwd/计数）每次 Render 时实时采集；
//   - 键值格式而非自然语言/JSON：LLM 对显式标记的键值提取最稳定、token 最省。
//
// 性能：render() 每轮迭代调用，用复用的 strings.Builder 写入，
// 不产生中间 []string 切片；静态 env 前缀在 New 时预算好。
// git 采集带 5 秒 TTL 缓存（fork 两个 git 进程太贵，不能每轮都跑）。
//
// 当前实现 env + todo + counters 三区；loaded/events 两区待对应功能落地后扩展。
//
// 会话级数据源经 StatusDeps 构造注入（消费侧窄接口：只读快照，不需要
// 完整 Provider 契约）；nil 字段对应区块渲染为空。
type StatusBar struct {
	session     Session
	todoStatus  TodoStatus
	skillStatus SkillStatus
	mcpStatus   MCPStatus
	// ---- 静态字段（进程内不变，New 时初始化）----
	// envStatic 是 env 区静态行（os/shell/python），预拼接好，
	// render 时直接写入，不重复拼接。不含 <env></env> 标签。
	envStatic string

	todos           []todo.Todo
	todoVersion     int64
	todoChangedIter int

	// ---- counters（由循环本身维护）----
	calls             map[string]int
	callNames         []string // 复用的排序缓冲区，避免每轮 alloc
	consecutiveErrors int
	lastError         string
	startTime         time.Time

	// 复用的 Builder，避免每轮迭代分配
	b strings.Builder
}

// NewStatusBar 创建状态栏：静态环境信息在此一次性采集并预拼接。
func NewStatusBar(session Session, todoStatus TodoStatus, skillStatus SkillStatus, mcpStatus MCPStatus) *StatusBar {
	sb := &StatusBar{
		session:     session,
		todoStatus:  todoStatus,
		skillStatus: skillStatus,
		mcpStatus:   mcpStatus,
	}

	return sb
}

func (sb *StatusBar) Start() {
	sb.b.Grow(512)

	// 预拼接 env 区静态行（os/shell/python）——这三行进程内不变
	if os := toolkit.OSInfo(); os != "" {
		sb.b.WriteString("    os: ")
		sb.b.WriteString(os)
		sb.b.WriteByte('\n')
	}
	if shell := toolkit.ShellInfo(); shell != "" {
		sb.b.WriteString("    shell: ")
		sb.b.WriteString(shell)
		sb.b.WriteByte('\n')
	}
	if py := toolkit.PythonVersion(); py != "" {
		sb.b.WriteString("    python: ")
		sb.b.WriteString(py)
		sb.b.WriteByte('\n')
	}
	sb.envStatic = sb.b.String()
	sb.b.Reset()

	sb.todos = nil
	sb.todoVersion = 0
	sb.todoChangedIter = 0

	sb.calls = make(map[string]int)
	sb.callNames = make([]string, 0, 8)
	sb.consecutiveErrors = 0
	sb.lastError = ""
	sb.startTime = time.Now()
}

func (sb *StatusBar) Stop() {
	sb.todos = nil
	sb.todoVersion = 0
	sb.todoChangedIter = 0

	sb.calls = nil
	sb.callNames = nil
	sb.consecutiveErrors = 0
	sb.lastError = ""
	sb.startTime = time.Time{}
	sb.b.Reset()
}

// Render 渲染当前迭代的状态栏消息，追加到上下文尾部。
// 动态字段（time/cwd/git）每次调用实时采集；todo/skills/tools 数据
// 从构造注入的 StatusDeps 读取。ctx 仅用于 git 探针的超时控制。
func (sb *StatusBar) Render(ctx context.Context, iteration int) *schema.Message {
	// 从 TodoStore 读取快照，检测版本变更以计算"未更新轮数"
	if sb.todoStatus != nil {
		todos, version := sb.todoStatus.Snapshot()
		if version != sb.todoVersion {
			sb.todoVersion = version
			sb.todoChangedIter = iteration
		}
		sb.todos = todos
	}
	return sb.render(ctx, iteration)
}

// RecordToolCall 在工具执行完成后更新计数器。
func (sb *StatusBar) RecordToolCall(toolName string, err error) {
	sb.calls[toolName]++
	if err != nil {
		sb.consecutiveErrors++
		sb.lastError = toolName + ": " + truncateLine(err.Error(), 80)
	} else {
		sb.consecutiveErrors = 0
	}
}

// render 拼装 <agent_status> 消息。直接写入复用的 Builder，
// 不产生中间 []string 切片。
func (sb *StatusBar) render(ctx context.Context, iteration int) *schema.Message {
	b := &sb.b
	b.Reset()

	// 头部
	b.WriteString(`<agent_status seq="`)
	b.WriteString(strconv.Itoa(iteration))
	b.WriteString(`">\n`)

	// ---- env ----
	// 统一写 <env></env> 标签，内部先动态行后静态行（预拼接）
	b.WriteString("  <env>\n")
	timeNow := nowFormatted()
	if timeNow != "" {
		b.WriteString("    time: ")
		b.WriteString(timeNow)
		b.WriteByte('\n')
	}
	cwd := sb.session.GetWorkspaceDir()
	if cwd != "" {
		b.WriteString("    cwd: ")
		b.WriteString(cwd)
		b.WriteByte('\n')
	}
	git := gitStatus(ctx, cwd)
	if git != "" {
		b.WriteString("    git: ")
		b.WriteString(git)
		b.WriteByte('\n')
	}
	b.WriteString(sb.envStatic) // 预拼接的 os/shell/python 行
	b.WriteString("  </env>\n")

	// ---- todo ----
	// 从 TodoStore 快照渲染（设计文档 2.10 数据流投影）
	if len(sb.todos) > 0 {
		b.WriteString("  <todo>\n")
		for i, t := range sb.todos {
			b.WriteString("    [")
			b.WriteString(strconv.Itoa(i + 1))
			b.WriteString("] ")
			b.WriteString(truncateLine(t.Content, 60))
			b.WriteString(" — ")
			b.WriteString(t.Status)
			b.WriteByte('\n')
		}
		b.WriteString("  </todo>\n")
	}

	// ---- loaded 区（设计文档 2.10：skills 与 discovered_tools 两个幂等集合）----
	// 已加载技能（load_skill 幂等集合）：让模型明确知道哪些手册已在轨迹中。
	loadedSkills := sb.skillStatus.Loaded()
	if len(loadedSkills) > 0 {
		b.WriteString("  <skills loaded=\"")
		b.WriteString(strings.Join(loadedSkills, ", "))
		b.WriteString("\"/>\n")
	}
	// 已注册 MCP 工具（discover_tools 命中即注册的幂等集合）：让模型明确
	// 知道哪些外部工具已可直接调用，避免重复发现/重复注册。
	loadedTools := sb.mcpStatus.Loaded()
	if len(loadedTools) > 0 {
		b.WriteString("  <tools registered=\"")
		b.WriteString(strings.Join(loadedTools, ", "))
		b.WriteString("\"/>\n")
	}

	// ---- counters ----
	b.WriteString("  <counters>\n    iteration: ")
	b.WriteString(strconv.Itoa(iteration))
	b.WriteString(" · elapsed: ")
	b.WriteString(formatElapsed(time.Since(sb.startTime).Round(time.Second)))
	b.WriteByte('\n')

	if len(sb.calls) > 0 {
		b.WriteString("    calls: ")
		// 复用 callNames 缓冲区排序，不每轮 alloc
		sb.callNames = sb.callNames[:0]
		for name := range sb.calls {
			sb.callNames = append(sb.callNames, name)
		}
		sortStrings(sb.callNames)
		for i, name := range sb.callNames {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(name)
			b.WriteString("×")
			b.WriteString(strconv.Itoa(sb.calls[name]))
		}
		b.WriteByte('\n')
	}

	if sb.consecutiveErrors > 0 {
		b.WriteString("    errors: ")
		b.WriteString(strconv.Itoa(sb.consecutiveErrors))
		b.WriteString(" consecutive failures")
		if sb.lastError != "" {
			b.WriteString(" (last: ")
			b.WriteString(sb.lastError)
			b.WriteString(")")
		}
		b.WriteString(" → do not retry as-is; change approach or inform the user\n")
	}

	// TODO 陈旧提醒：列表存在且 N 轮未更新且有未完成项时提示推进
	if len(sb.todos) > 0 {
		pending := 0
		for _, t := range sb.todos {
			if t.Status == todo.TodoPending || t.Status == todo.TodoInProgress {
				pending++
			}
		}
		stale := iteration - sb.todoChangedIter
		if stale >= 3 && pending > 0 {
			b.WriteString("    todo: unchanged for ")
			b.WriteString(strconv.Itoa(stale))
			b.WriteString(" turns (")
			b.WriteString(strconv.Itoa(pending))
			b.WriteString(" items pending) → check whether to advance or update it\n")
		}
	}

	b.WriteString("  </counters>\n</agent_status>")

	return &schema.Message{Role: schema.RoleUser, Content: b.String()}
}

// gitStatus 返回 "main (3 files modified)" 形式的一行 git 摘要；
// 非 git 仓库、git 不可用或超时返回空串。
func gitStatus(ctx context.Context, dir string) string {
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	branch, err := exec.CommandContext(cctx, "git", "-C", dir,
		"rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return ""
	}
	status, err := exec.CommandContext(cctx, "git", "-C", dir,
		"status", "--porcelain").Output()
	if err != nil {
		return strings.TrimSpace(string(branch))
	}
	modified := countLines(status)
	b := strings.TrimSpace(string(branch))
	if modified > 0 {
		return b + " (" + strconv.Itoa(modified) + " files modified)"
	}
	return b + " (clean)"
}

// countLines 统计非空行数（比 strings.Split + 遍历少一次分配）。
func countLines(data []byte) int {
	count := 0
	for _, c := range data {
		if c == '\n' {
			count++
		}
	}
	// 最后一行可能没有换行符
	if len(data) > 0 && data[len(data)-1] != '\n' {
		count++
	}
	return count
}

// nowFormatted 返回当前时间的格式化字符串（含 ISO8601 时区偏移）。
func nowFormatted() string {
	return time.Now().Format("2006-01-02 15:04:05 -07:00")
}

// pad2 补零到两位。
func pad2(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

// formatElapsed 紧凑的耗时格式：47s / 3m12s / 1h05m。
// 用 strconv 拼接而非 fmt.Sprintf 避免反射。
func formatElapsed(d time.Duration) string {
	secs := int(d.Seconds())
	if secs < 60 {
		return strconv.Itoa(secs) + "s"
	}
	if secs < 3600 {
		return strconv.Itoa(secs/60) + "m" + pad2(secs%60) + "s"
	}
	return strconv.Itoa(secs/3600) + "h" + pad2((secs%3600)/60) + "m"
}

// truncateLine 截断到 n 个字符并去除换行（状态栏单行展示用）。
func truncateLine(s string, n int) string {
	s = strings.TrimSpace(s)
	// 就地替换换行为空格，不产生新字符串（如果没换行的话）
	if !strings.ContainsRune(s, '\n') {
		if len(s) <= n {
			return s
		}
		return s[:n] + "…"
	}
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

// sortStrings 就地排序（sort.Strings 的等价实现，避免引入 sort 包的开销）。
// 对小切片（通常 <10 个工具）插入排序比快速排序更快。
func sortStrings(a []string) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j-1] > a[j]; j-- {
			a[j-1], a[j] = a[j], a[j-1]
		}
	}
}
