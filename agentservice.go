package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"tars/internal/config"
	"tars/internal/session"
	"tars/pkg/llm"
	"tars/pkg/prompt"
	"tars/pkg/store"
	"tars/pkg/tools"
	"tars/pkg/trace"

	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// ============================================================================
// Types re-exported for backward compatibility（Wails 绑定生成器只扫描
// main 包；其他 main 内文件与前端 bindings 仍引用这些名字）。
// ============================================================================

type ToolCall = store.ToolCall
type UsageInfo = store.UsageInfo
type Message = store.Message
type Session = session.SessionState

// ============================================================================
// Stream event payloads（Wails 事件系统）
// ============================================================================

type StreamChunk struct {
	SessionID string `json:"sessionId"`
	MessageID string `json:"messageId"`
	Chunk     string `json:"chunk"`
}

type StreamDone struct {
	SessionID string     `json:"sessionId"`
	MessageID string     `json:"messageId"`
	Usage     *UsageInfo `json:"usage,omitempty"`
	ElapsedMs int64      `json:"elapsedMs"`
}

type StreamError struct {
	SessionID string `json:"sessionId"`
	MessageID string `json:"messageId"`
	Error     string `json:"error"`
	Kind      string `json:"kind,omitempty"`
}

type ToolEvent struct {
	SessionID  string `json:"sessionId"`
	MessageID  string `json:"messageId"`
	ToolCallID string `json:"toolCallId"`
	ToolName   string `json:"toolName"`
	Args       string `json:"args"`
}

type ToolResultEvent struct {
	SessionID  string `json:"sessionId"`
	MessageID  string `json:"messageId"`
	ToolCallID string `json:"toolCallId"`
	Output     string `json:"output"`
}

type ReasoningEvent struct {
	SessionID string `json:"sessionId"`
	MessageID string `json:"messageId"`
	Content   string `json:"content"`
}

type SessionRenamedEvent struct {
	SessionID string `json:"sessionId"`
	Title     string `json:"title"`
}

// ============================================================================
// AgentService —— 对前端暴露的 API 层。
// 会话运行态由 internal/session.Manager 全局单例承载，本服务只做
// 参数校验、委托与事件桥接。
// ============================================================================

type AgentService struct{}

// resolveWorkDir resolves the effective workspace directory for a session,
// preferring the user's custom directory when one is set.
func (s *AgentService) resolveWorkDir(sessionID string) string {
	if meta, err := store.GetSessionStore().LoadMeta(sessionID); err == nil && meta != nil && meta.CustomWorkDir != "" {
		return meta.CustomWorkDir
	}
	return store.GetSessionStore().WorkspaceDir(sessionID)
}

func (s *AgentService) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
	if err := config.LoadAppConfig(); err != nil {
		slog.Error("Failed loading app config", "error", err)
		return err
	}
	appConfig := config.Get()

	// 注册表启动期容错：配置问题不阻塞启动，首次对话时才暴露，
	// 用户可通过设置界面修复。
	llm.InitRegistry(appConfig.LLM)

	if !appConfig.Trace.Enabled {
		slog.Info("Tracing disabled (trace.enabled is not set)")
	} else {
		if appConfig.Trace.OTLPHTTPEndpoint != "" {
			slog.Info("OTLP/HTTP trace export enabled", "endpoint", appConfig.Trace.OTLPHTTPEndpoint)
		}
		if appConfig.Trace.OTLPGrpcEndpoint != "" {
			slog.Info("OTLP/gRPC trace export enabled", "endpoint", appConfig.Trace.OTLPGrpcEndpoint)
		}
	}

	// 工作目录
	workDir := appConfig.WorkDir
	if err := os.MkdirAll(workDir, 0755); err != nil {
		slog.Error("Failed to create work directory", "dir", workDir, "error", err)
		return err
	}
	slog.Info("Agent work directory", "path", workDir)

	// 初始化全局会话存储单例
	if err := store.InitSessionStore(workDir); err != nil {
		slog.Error("Failed to init session store", "error", err)
		return err
	}

	// 初始化全局工具管理器单例并注册内置工具
	tools.InitManager(workDir)

	// 初始化全局追踪器单例（OTLP 连接池 + 批量导出器）
	trace.InitTrace(appConfig.Trace)

	// 构建全局系统提示词单例（base + 静态 env），必须在 InitManager 之后
	prompt.InitSystemPrompt(tools.DefaultManager().ToolNames())

	// 初始化会话管理器单例并从磁盘恢复全部会话
	sessionMgr := session.InitManager()
	if err := sessionMgr.Restore(); err != nil {
		return err
	}

	return nil
}

func (s *AgentService) ServiceShutdown() error {
	sessionMgr := session.GetManager()
	if sessionMgr != nil {
		sessionMgr.CancelAll() // 先取消所有运行中的会话
	}
	trace.Shutdown() // 关闭全局 OTLP 导出器
	return nil
}

// --- Session management API ---

func (s *AgentService) CreateSession() (*Session, error) {
	return session.GetManager().Create()
}

func (s *AgentService) ListSessions() ([]Session, error) {
	return session.GetManager().List(), nil
}

func (s *AgentService) DeleteSession(id string) error {
	return session.GetManager().Delete(id)
}

// --- Session queries ---

func (s *AgentService) GetSession(id string) (*Session, error) {
	sess, ok := session.GetManager().Get(id)
	if !ok {
		return nil, fmt.Errorf("session not found: %s", id)
	}
	return sess, nil
}

// --- Message operations ---

// SendMessage sends a user message and starts the agent loop.
func (s *AgentService) SendMessage(sessionID, content string) error {
	sessionMgr := session.GetManager()
	sess, ok := sessionMgr.Get(sessionID)
	if !ok {
		return fmt.Errorf("session not found: %s", sessionID)
	}

	now := time.Now().UnixMilli()

	userMsg := Message{
		ID:        uuid.NewString(),
		Role:      schema.User,
		Content:   content,
		CreatedAt: now,
	}
	assistantMsg := Message{
		ID:        uuid.NewString(),
		Role:      schema.Assistant,
		CreatedAt: now,
	}

	sessionMgr.WithSession(sessionID, func(s *session.SessionState) {
		s.Messages = append(s.Messages, userMsg, assistantMsg)
		s.UpdatedAt = now
		if s.Title == "新对话" {
			title := content
			if len(title) > 50 {
				title = title[:50]
			}
			s.Title = title
			if err := store.GetSessionStore().SaveMeta(sessionID, &store.SessionMeta{
				Title:     s.Title,
				CreatedAt: s.CreatedAt,
				UpdatedAt: s.UpdatedAt,
			}); err != nil {
				slog.Warn("Failed to save session meta", "id", sessionID, "error", err)
			}
		}
	})

	if err := s.persistMessage(sessionID, userMsg); err != nil {
		return err
	}
	if err := s.persistMessage(sessionID, assistantMsg); err != nil {
		return err
	}

	assistantIndex := len(sess.Messages) - 1

	messages := s.buildLLMMessages(sess)

	ctx, cancel := context.WithCancel(context.Background())
	sessionMgr.RegisterCancel(sessionID, cancel)

	go s.runAgentLoop(ctx, sessionID, assistantMsg.ID, assistantIndex, messages, content)
	return nil
}

// CancelMessage cancels an in-flight SendMessage turn.
func (s *AgentService) CancelMessage(sessionID string) error {
	sessionMgr := session.GetManager()
	if !sessionMgr.Has(sessionID) {
		return fmt.Errorf("session not found: %s", sessionID)
	}
	sessionMgr.Cancel(sessionID)
	return nil
}

// DeleteMessage deletes a message by ID and returns its index.
func (s *AgentService) DeleteMessage(sessionID, messageID string) (int, error) {
	sessionMgr := session.GetManager()
	sess, ok := sessionMgr.Get(sessionID)
	if !ok {
		return -1, fmt.Errorf("session not found: %s", sessionID)
	}

	index := -1
	sessionMgr.WithSession(sessionID, func(s *session.SessionState) {
		for i, m := range s.Messages {
			if m.ID == messageID {
				index = i
				s.Messages = append(s.Messages[:i], s.Messages[i+1:]...)
				break
			}
		}
	})
	if index == -1 {
		return -1, fmt.Errorf("message not found: %s", messageID)
	}

	if err := s.rewriteMessages(sessionID, sess.Messages); err != nil {
		return -1, err
	}
	return index, nil
}

// RetryMessage retries the last turn (or the turn containing the given
// assistant message). It regenerates the assistant response for that turn.
func (s *AgentService) RetryMessage(sessionID string, messageID string) error {
	sessionMgr := session.GetManager()
	sess, ok := sessionMgr.Get(sessionID)
	if !ok {
		return fmt.Errorf("session not found: %s", sessionID)
	}

	if len(sess.Messages) == 0 {
		return fmt.Errorf("no messages to retry")
	}

	var userIndex = -1
	sessionMgr.WithSession(sessionID, func(s *session.SessionState) {
		targetAssistant := -1
		if messageID == "" {
			for i := len(s.Messages) - 1; i >= 0; i-- {
				if s.Messages[i].Role == schema.Assistant {
					targetAssistant = i
					break
				}
			}
		} else {
			for i, m := range s.Messages {
				if m.ID == messageID && m.Role == schema.Assistant {
					targetAssistant = i
					break
				}
			}
		}
		if targetAssistant == -1 {
			return
		}

		for i := targetAssistant - 1; i >= 0; i-- {
			if s.Messages[i].Role == schema.User {
				userIndex = i
				break
			}
		}
		if userIndex == -1 {
			return
		}

		s.Messages = s.Messages[:userIndex+1]

		now := time.Now().UnixMilli()
		s.Messages = append(s.Messages, Message{
			ID:        uuid.NewString(),
			Role:      schema.Assistant,
			CreatedAt: now,
		})
		s.UpdatedAt = now
	})

	if userIndex == -1 {
		return fmt.Errorf("no previous user message found to retry")
	}

	if err := s.rewriteMessages(sessionID, sess.Messages); err != nil {
		return err
	}

	assistantIndex := len(sess.Messages) - 1
	messages := s.buildLLMMessages(sess)

	userText := ""
	for i := userIndex; i >= 0; i-- {
		if sess.Messages[i].Role == schema.User {
			userText = sess.Messages[i].Content
			break
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	sessionMgr.RegisterCancel(sessionID, cancel)

	go s.runAgentLoop(ctx, sessionID, sess.Messages[assistantIndex].ID, assistantIndex, messages, userText)
	return nil
}

// --- Message editing ---

// EditMessage edits a user message in-place (no regeneration).
func (s *AgentService) EditMessage(sessionID, messageID, content string) error {
	sessionMgr := session.GetManager()
	sess, ok := sessionMgr.Get(sessionID)
	if !ok {
		return fmt.Errorf("session not found: %s", sessionID)
	}

	index := -1
	sessionMgr.WithSession(sessionID, func(s *session.SessionState) {
		for i, m := range s.Messages {
			if m.ID == messageID {
				if m.Role != schema.User {
					return
				}
				s.Messages[i].Content = content
				s.UpdatedAt = time.Now().UnixMilli()
				index = i
				break
			}
		}
	})
	if index == -1 {
		return fmt.Errorf("message not found or not editable: %s", messageID)
	}

	return s.rewriteMessages(sessionID, sess.Messages)
}

// --- Session rename ---

func (s *AgentService) RenameSession(id, title string) error {
	sessionMgr := session.GetManager()
	sess, ok := sessionMgr.Get(id)
	if !ok {
		return fmt.Errorf("session not found: %s", id)
	}

	sessionMgr.WithSession(id, func(s *session.SessionState) {
		s.Title = title
		s.UpdatedAt = time.Now().UnixMilli()
	})

	if err := store.GetSessionStore().SaveMeta(id, &store.SessionMeta{
		Title:     sess.Title,
		CreatedAt: sess.CreatedAt,
		UpdatedAt: sess.UpdatedAt,
	}); err != nil {
		return err
	}

	application.Get().Event.Emit("session:renamed", SessionRenamedEvent{
		SessionID: id,
		Title:     title,
	})
	return nil
}

// --- LLM message construction ---

// buildLLMMessages 把会话存储里的消息转成 Eino schema.Message 格式。
// 系统提示词 = base + 静态 env（OS/tools），不含会话工作目录——
// 该信息由 StatusBar 的 cwd 字段每轮迭代注入。
func (s *AgentService) buildLLMMessages(sess *session.SessionState) []*schema.Message {
	msgs := make([]*schema.Message, 0, len(sess.Messages)+1)
	msgs = append(msgs, schema.SystemMessage(prompt.SystemPrompt()))

	policy := s.reasoningPolicy()
	for _, m := range sess.Messages {
		if m.Role == schema.Assistant && m.Reasoning != "" {
			m2 := m
			applyReasoningPolicy(&m2, policy)
			msgs = append(msgs, m2.ToSchemaMessage())
			continue
		}
		msgs = append(msgs, m.ToSchemaMessage())
	}
	return msgs
}

// applyReasoningPolicy 按供应商策略调整 assistant 消息的 reasoning 回放。
func applyReasoningPolicy(m *Message, policy string) {
	switch policy {
	case llm.ReasoningReplay:
		// 透传，不做调整
	case llm.ReasoningStrip:
		m.Reasoning = ""
	case llm.ReasoningKeep:
		// 保持原样
	}
}

// reasoningPolicy 返回当前激活模型供应商的 reasoning 回放策略
// （replay/strip/keep）。配置缺失时保守取 keep（透传）。
func (s *AgentService) reasoningPolicy() string {
	cfg := llm.GetRegistry().Config()
	m := cfg.ActiveModel()
	if m == nil {
		return llm.ReasoningKeep
	}
	p := cfg.FindProvider(m.Provider)
	if p == nil {
		return llm.ReasoningKeep
	}
	return p.ReasoningReplayMode()
}

// --- Persistence helpers（委托给 session.Manager） ---

func (s *AgentService) persistMessage(sessionID string, msg Message) error {
	return session.GetManager().AppendMessage(sessionID, msg)
}

func (s *AgentService) rewriteMessages(sessionID string, msgs []Message) error {
	return session.GetManager().RewriteMessages(sessionID, msgs)
}
