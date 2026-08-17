// Package runner 是运行器：持有共享依赖与会话注册表，驱动一轮轮对话。
// 依赖方向：runner → session / agent / skills / tools / llm / store。
// runner 是长命对象（由装配层 wire 创建一次、常驻）；每轮对话通过
// Submit/Retry 启动一个短命 turn（见 turn.go），两者职责分离。
package runner

import (
	"context"
	"fmt"
	"log/slog"

	"tars/internal/event"
	"tars/internal/session"
	"tars/internal/skills"
	"tars/pkg/llm"
	"tars/pkg/store"
	"tars/pkg/tools"

	"github.com/cloudwego/eino/schema"
)

// Deps 是运行所需的共享依赖，由装配层（wire）创建并注入。
// runner 不通过全局单例取用这些对象。
type Deps struct {
	Store  *store.SessionStore
	Tools  *tools.Manager
	Skills *skills.Manager
	LLM    *llm.Registry
	SysMsg *schema.Message // 系统提示词消息（含工具列表等静态环境上下文）
	Sink   event.Sink      // 事件输出端口（前端实现，内核不 import Wails）
}

// Runner 是长命运行器：持有共享依赖、会话注册表与询问答复通道注册表。
// 由装配层创建一次、常驻；服务层持有并委托给它。
type Runner struct {
	deps     Deps
	sessions *session.Manager
	asks     *askRegistry
}

// New 创建运行器。deps 与 sessions 由装配层创建并注入。
func New(deps Deps, sessions *session.Manager) *Runner {
	return &Runner{
		deps:     deps,
		sessions: sessions,
		asks:     newAskRegistry(),
	}
}

// Sessions 暴露会话注册表，供服务层做会话 CRUD（列表/查找/删除/改名等）。
func (r *Runner) Sessions() *session.Manager { return r.sessions }

// Submit 对指定会话发起一轮对话。
func (r *Runner) Submit(sessionID, content string) error {
	sess, ok := r.sessions.Find(sessionID)
	if !ok {
		return fmt.Errorf("session not found: %s", sessionID)
	}
	// 轮运行中禁止再次发送：会覆盖运行中轮的 cancel，
	// 且旧 goroutine 注销时会误删新轮的标记。
	if sess.IsRunning() {
		return fmt.Errorf("turn in progress, cancel it first")
	}

	sess.AppendUserTurn(content) // 消息准备：尾部留下本轮空 assistant 锚点
	r.start(sess, content)
	return nil
}

// Retry 对指定会话的某条 assistant 消息发起重试。
func (r *Runner) Retry(sessionID, messageID string) error {
	sess, ok := r.sessions.Find(sessionID)
	if !ok {
		return fmt.Errorf("session not found: %s", sessionID)
	}
	if sess.IsRunning() {
		return fmt.Errorf("turn in progress, cancel it first")
	}
	userText, err := sess.PrepareRetry(messageID)
	if err != nil {
		return err
	}
	r.start(sess, userText)
	return nil
}

// Cancel 取消指定会话运行中的轮。
func (r *Runner) Cancel(sessionID string) error {
	if !r.sessions.Has(sessionID) {
		return fmt.Errorf("session not found: %s", sessionID)
	}
	r.sessions.Cancel(sessionID)
	return nil
}

// ResolveAsk 回写一次询问/审批答复（服务层入口）。
func (r *Runner) ResolveAsk(toolCallID string, ans *tools.Answer) bool {
	return r.asks.resolve(toolCallID, ans)
}

// start 启动一轮对话的短命 turn。约定调用方已完成消息准备——
// 尾部是本轮的空 assistant 锚点（Info.AppendUserTurn / Info.PrepareRetry）。
// userText 仅用于 trace 展示。调用方需保证当前无运行中的轮。
func (r *Runner) start(sess *session.Info, userText string) {
	if len(sess.Messages) == 0 {
		slog.Error("runner.start: missing assistant anchor", "session", sess.ID)
		return
	}
	t := &turn{
		sess:           sess,
		deps:           r.deps,
		asks:           r.asks,
		sink:           r.deps.Sink,
		assistantIndex: len(sess.Messages) - 1,
		assistantID:    sess.Messages[len(sess.Messages)-1].ID,
		userText:       userText,
	}
	t.ctx, sess.Cancel = context.WithCancel(context.Background())
	go t.run()
}
