// Package boot 负责应用的组装与启动：App 是全局唯一入口，持有进程级
// 共享依赖（模型注册表/技能/持久化/工具目录），并以 Controller 为单位的
// 唯一会话索引——Controller 即会话的完整封装（见 controller.go）。
package boot

import (
	"fmt"
	"runtime"
	"sync"

	"tars/internal/event"
	"tars/internal/session"
	"tars/pkg/skills"
	"tars/pkg/llm"
	"tars/pkg/prompt"
	"tars/pkg/schema"
	"tars/pkg/tools"
	"tars/pkg/trace"
)

// Options 是 NewApp 的装配参数（字段来自应用配置 config.AppConfig）。
type Options struct {
	// WorkDir 是根目录（workspace/skills/sessions 等子目录的父）。
	WorkDir string
	// LLM 是模型配置（多条目 + 激活标记）。
	LLM *llm.Config
	// Skills 是技能配置。
	Skills *skills.Config
	// Sink 后端事件出口（Wails 适配器）；nil 时静默。
	Sink event.Sink
}

// App 是应用的全局唯一入口：进程级共享依赖 + 多会话 Controller。
// 普通对象（非单例），由服务层创建并注入。
type App struct {
	sessionStore *session.Store
	skills       *skills.Manager
	llmReg       *llm.Registry
	sysMsg       *schema.Message
	sink         event.Sink
	asks         *askRegistry

	// ctrls 是唯一会话索引：Controller 持有各自的 session.Info，
	// 不存在第二份会话注册表。
	mu    sync.RWMutex
	ctrls map[string]*Controller
}

// NewApp 创建并连接全部共享依赖，返回就绪的 App。
// 它是"new 领域对象"的唯一入口（composition root）。
func NewApp(opts Options) (*App, error) {
	if opts.Sink == nil {
		opts.Sink = event.Discard
	}

	st, err := session.NewStore(opts.WorkDir, opts.Sink)
	if err != nil {
		return nil, fmt.Errorf("boot: session store: %w", err)
	}

	skillsMgr, err := skills.NewManager(opts.WorkDir, opts.Skills)
	if err != nil {
		return nil, fmt.Errorf("boot: skills: %w", err)
	}
	if err := skillsMgr.GenerateIndex(); err != nil {
		return nil, fmt.Errorf("boot: skills index: %w", err)
	}

	llmReg := llm.NewRegistry(opts.LLM)

	sysMsg := prompt.BuildSystemMessage(prompt.EnvironmentContext{
		OS:       runtime.GOOS,
		Platform: runtime.GOARCH,
		Tools:    tools.BuiltinNames(),
	})

	return &App{
		sessionStore: st,
		skills:       skillsMgr,
		llmReg:       llmReg,
		sysMsg:       sysMsg,
		sink:         opts.Sink,
		asks:         newAskRegistry(),
		ctrls:        make(map[string]*Controller),
	}, nil
}

// --- 依赖访问器（服务层使用） ---

// SessionStore 返回会话持久化存储。
func (a *App) SessionStore() *session.Store { return a.sessionStore }

// LLM 返回模型注册表。
func (a *App) LLM() *llm.Registry { return a.llmReg }

// Skills 返回技能管理器。
func (a *App) Skills() *skills.Manager { return a.skills }

// --- 会话生命周期 ---

// RestoreSessions 从磁盘恢复全部会话并依次为每个会话建 Controller
// （Controller 持有各自的 session.Info）。进程启动时调用一次；
// 恢复不是创建，不产生 session.created span。
func (a *App) RestoreSessions() error {
	infos, err := a.sessionStore.RestoreAll()
	if err != nil {
		return err
	}
	a.mu.Lock()
	for _, sess := range infos {
		a.ctrls[sess.ID] = NewController(a.controllerDeps(), sess)
	}
	a.mu.Unlock()
	return nil
}

// CreateSession 创建新会话：持久化由 session.Store 封装，这里负责
// Controller 索引与创建事件。
func (a *App) CreateSession() (*session.Info, error) {
	sess, err := a.sessionStore.Create()
	if err != nil {
		return nil, err
	}
	trace.LogSessionCreated(sess.ID, sess.Title)

	a.mu.Lock()
	a.ctrls[sess.ID] = NewController(a.controllerDeps(), sess)
	a.mu.Unlock()
	return sess, nil
}

// controllerDeps 收集 Controller 所需的进程级共享依赖。
// App → Controller 单向装配：Controller 不持有 App。
// 事件订阅者在此组合：每个会话的事件出口 = FanOut(UI sink + trace sink)。
func (a *App) controllerDeps() Deps {
	return Deps{
		Store: a.sessionStore,
		NewSink: func(sessionID string) event.Sink {
			return event.NewFanOut(a.sink, NewTraceSink())
		},
		LLM:    a.llmReg,
		SysMsg: a.sysMsg,
		Skills: a.skills,
		Asks:   a.asks,
	}
}

// DeleteSession 删除会话：先取消运行中的轮，再移除 Controller 索引与磁盘数据。
func (a *App) DeleteSession(id string) error {
	a.Cancel(id)
	a.mu.Lock()
	delete(a.ctrls, id)
	a.mu.Unlock()
	return a.sessionStore.Delete(id)
}

// FindSession 按 ID 取会话。
func (a *App) FindSession(id string) (*session.Info, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	c, ok := a.ctrls[id]
	if !ok {
		return nil, false
	}
	return c.GetSession(), true
}

// HasSession 报告会话是否存在。
func (a *App) HasSession(id string) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	_, ok := a.ctrls[id]
	return ok
}

// ListSessions 按创建时间升序列出全部会话。
func (a *App) ListSessions() []*session.Info {
	a.mu.RLock()
	out := make([]*session.Info, 0, len(a.ctrls))
	for _, c := range a.ctrls {
		out = append(out, c.GetSession())
	}
	a.mu.RUnlock()
	session.SortByCreatedAt(out)
	return out
}

// CancelAll 取消所有会话的运行轮（退出前调用）。
func (a *App) CancelAll() {
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, c := range a.ctrls {
		c.Cancel()
	}
}

// --- 轮生命周期入口（服务层使用） ---

// Submit 提交一条用户消息并启动一轮对话（委托给会话的 Controller）。
// 返回后端分配的 user/assistant 消息 ID。
func (a *App) Submit(sessionID, content string) (string, string, error) {
	c, ok := a.FindController(sessionID)
	if !ok {
		return "", "", fmt.Errorf("session not found: %s", sessionID)
	}
	return c.Submit(content)
}

// Retry 重试一轮对话（委托给会话的 Controller）。返回新一轮 assistant 消息 ID。
func (a *App) Retry(sessionID, messageID string) (string, error) {
	c, ok := a.FindController(sessionID)
	if !ok {
		return "", fmt.Errorf("session not found: %s", sessionID)
	}
	return c.Retry(messageID)
}

// Cancel 取消会话当前运行的轮（无运行中的轮则无事发生）。
func (a *App) Cancel(sessionID string) {
	if c, ok := a.FindController(sessionID); ok {
		c.Cancel()
	}
}

// ResolveAsk 把用户答复路由到等待中的询问/审批。
func (a *App) ResolveAsk(requestID string, ans *tools.Answer) bool {
	return a.asks.resolve(requestID, ans)
}

// FindController 按会话 ID 取 Controller（Restore/Create 保证索引总是全的）。
func (a *App) FindController(id string) (*Controller, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	c, ok := a.ctrls[id]
	return c, ok
}
