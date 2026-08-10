package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"tars/internal/config"
	"tars/pkg/llm"
	"tars/pkg/prompt"
	"tars/pkg/store"
	"tars/pkg/tools"
	"tars/pkg/trace"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// Message roles.
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

// defaultConversationTitle 新会话默认标题，首次发消息时替换为用户输入的截断。
const defaultConversationTitle = "新会话"

// 最大迭代轮次从配置文件读取（agent.maxIterations），
// 未配置时使用 config.DefaultMaxIterations。

// iterationTimeout 单次模型调用的超时上限。设置得较长，因为模型服务商
// 压力大时可能排队较久；超时后前端会提示用户决定是否重试。
const iterationTimeout = 120 * time.Second

// Message is a single chat message within a conversation.
type Message struct {
	ID         string     `json:"id"`
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"toolCalls,omitempty"`
	ToolCallID string     `json:"toolCallId,omitempty"`
	CreatedAt  int64      `json:"createdAt"`
	// Reasoning 仅 assistant 消息有值：模型思考过程。它既用于历史会话
	// 重新展示，也随 buildLLMMessages 回传给模型（Gemini function call
	// 场景要求 thinking 随消息回放）。
	Reasoning string `json:"reasoning,omitempty"`
	// Usage 与 ElapsedMs 仅 assistant 消息有值：记录这一轮的 token 消耗
	// 与总耗时（含所有迭代），供状态栏/消息底部展示与费用估算。
	Usage     *UsageInfo `json:"usage,omitempty"`
	ElapsedMs int64      `json:"elapsedMs,omitempty"`
}

// ToolCall 记录模型请求的一次工具调用。
// Output 不参与持久化与 LLM 上下文构建，仅在 GetConversation 返回时
// 从对应 tool 消息合并填充，供前端展示工具执行结果。
type ToolCall struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Args   string `json:"args"`
	Output string `json:"output,omitempty"`
}

// Conversation is a chat session with an ordered list of messages.
type Conversation struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Messages  []Message `json:"messages"`
	CreatedAt int64     `json:"createdAt"`
	UpdatedAt int64     `json:"updatedAt"`
}

// StreamChunk is the payload of the "agent:chunk" event.
type StreamChunk struct {
	ConversationID string `json:"conversationId"`
	MessageID      string `json:"messageId"`
	Chunk          string `json:"chunk"`
}

// StreamDone is the payload of the "agent:done" event.
type StreamDone struct {
	ConversationID string     `json:"conversationId"`
	MessageID      string     `json:"messageId"`
	Usage          *UsageInfo `json:"usage,omitempty"`
	ElapsedMs      int64      `json:"elapsedMs"`
}

// UsageInfo carries token usage statistics for the completed response.
type UsageInfo struct {
	PromptTokens     int `json:"promptTokens"`
	CompletionTokens int `json:"completionTokens"`
	TotalTokens      int `json:"totalTokens"`
	// CachedTokens 提示词中命中服务端缓存的 token 数（Gemini 隐式缓存），
	// 用于计算缓存命中率 = CachedTokens / PromptTokens。
	CachedTokens int `json:"cachedTokens,omitempty"`
	// ModelEntry 产生该用量的模型条目 ID（配置中的 models[].id），
	// 多模型下用于按条目价格表核算费用。
	ModelEntry string `json:"modelEntry,omitempty"`
}

// StreamError is the payload of the "agent:error" event.
type StreamError struct {
	ConversationID string `json:"conversationId"`
	MessageID      string `json:"messageId"`
	Error          string `json:"error"`
	// Kind classifies the failure for the frontend: "timeout" (model call
	// exceeded the iteration deadline — likely provider congestion) or
	// "error" (anything else). Empty means "error".
	Kind string `json:"kind,omitempty"`
}

// ConversationRenamedEvent is the payload of the "conversation:renamed" event.
type ConversationRenamedEvent struct {
	ConversationID string `json:"conversationId"`
	Title          string `json:"title"`
}

// ToolEvent is the payload of the "agent:tool" event.
type ToolEvent struct {
	ConversationID string `json:"conversationId"`
	MessageID      string `json:"messageId"`
	ToolCallID     string `json:"toolCallId"`
	ToolName       string `json:"toolName"`
	Args           string `json:"args"`
}

// ToolResultEvent is the payload of the "agent:tool_result" event.
type ToolResultEvent struct {
	ConversationID string `json:"conversationId"`
	MessageID      string `json:"messageId"`
	ToolCallID     string `json:"toolCallId"`
	Output         string `json:"output"`
}

// ReasoningEvent is the payload of the "agent:reasoning" event: the model's
// thinking process (reasoning content) for the current turn. Emitted when
// the model returns ReasoningContent alongside its response.
type ReasoningEvent struct {
	ConversationID string `json:"conversationId"`
	MessageID      string `json:"messageId"`
	Content        string `json:"content"`
}

// appConfigPath 是应用配置文件的路径（相对工作目录）。
const appConfigPath = "config/config.yaml"

// AgentService exposes the agent backend to the frontend.
type AgentService struct {
	toolMgr      *tools.Manager
	workDir      string
	convsDir     string // root dir for conversation storage
	basePrompt   string // stable base prompt (without env context)
	envSuffix    string // env context that doesn't change per-conversation (OS, tools)
	store        *store.Store

	// cfgMu 保护 appConfig 与 OTLP 端点：设置界面保存配置时会热替换它们
	cfgMu            sync.RWMutex
	appConfig        *config.AppConfig
	otlpHTTPEndpoint string // optional OTLP/HTTP collector endpoint (Jaeger)
	otlpGrpcEndpoint string // optional OTLP/gRPC collector endpoint (Phoenix)

	// llmReg 模型注册表：多供应商/多模型的工厂与缓存，内部有锁，
	// 切换模型或保存配置通过 UpdateConfig 热更新。
	llmReg *llm.Registry

	mu      sync.RWMutex
	convs   map[string]*Conversation
	cancels map[string]context.CancelFunc
	tracers map[string]*trace.Tracer

	// modelHealthy 追踪最近一次 LLM 调用的成败（状态栏绿/红灯）。
	// 初始乐观置 true；emitDone 成功时 true，emitError 时 false。
	modelHealthy atomic.Bool
}

// currentConfig 返回当前生效的应用配置（设置界面保存后会被热替换）。
func (s *AgentService) currentConfig() *config.AppConfig {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	return s.appConfig
}

// currentModel 返回当前激活模型的 ChatModel 与条目配置（每轮 agent
// 循环开始时调用，模型切换下一轮自然生效）。
func (s *AgentService) currentModel() (model.ToolCallingChatModel, *llm.ModelConfig, error) {
	return s.llmReg.Active()
}

// traceEndpoints 返回当前生效的 OTLP 导出端点（HTTP, gRPC）。
func (s *AgentService) traceEndpoints() (string, string) {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	return s.otlpHTTPEndpoint, s.otlpGrpcEndpoint
}

func (s *AgentService) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
	appConfig, err := config.LoadAppConfig(appConfigPath)
	if err != nil {
		slog.Error("Failed loading app config", "error", err)
		return err
	}
	s.appConfig = appConfig
	s.modelHealthy.Store(true) // 初始乐观假设模型可用
	s.otlpHTTPEndpoint, s.otlpGrpcEndpoint = appConfig.OTLPEndpoints()
	if s.otlpHTTPEndpoint != "" {
		slog.Info("OTLP/HTTP trace export enabled", "endpoint", s.otlpHTTPEndpoint)
	}
	if s.otlpGrpcEndpoint != "" {
		slog.Info("OTLP/gRPC trace export enabled", "endpoint", s.otlpGrpcEndpoint)
	}

	// 注册表启动期容错：配置问题不阻塞启动，首次对话时才暴露，
	// 用户可通过设置界面修复。
	s.llmReg = llm.NewRegistry(appConfig.LLM)

	// 工作目录：优先用配置，否则用平台特定的家目录默认路径
	workDir := appConfig.WorkDir
	if workDir == "" {
		workDir = store.DefaultDataDir()
		migrateLegacyDataDir(workDir) // 兼容改名前的 ~/myapp 数据目录
	}
	if err := os.MkdirAll(workDir, 0755); err != nil {
		slog.Error("Failed to create work directory", "dir", workDir, "error", err)
		return err
	}
	s.workDir = workDir
	slog.Info("Agent work directory", "path", workDir)

	// 会话持久化根目录：{workDir}/conversations/{uuid}/
	s.convsDir = filepath.Join(workDir, "conversations")
	convStore, err := store.NewStore(s.convsDir)
	if err != nil {
		slog.Error("Failed to init conversation store", "error", err)
		return err
	}
	s.store = convStore

	s.toolMgr = tools.NewManager()
	s.toolMgr.Register(tools.CodeInterpreter(workDir))
	s.toolMgr.Register(tools.RunCommand(workDir))
	s.toolMgr.Register(tools.ReadFile(workDir))
	s.toolMgr.Register(tools.WriteFile(workDir))
	s.toolMgr.Register(tools.EditFile(workDir))
	s.toolMgr.Register(tools.GlobFiles(workDir))
	s.toolMgr.Register(tools.GrepFiles(workDir))

	// base prompt 只含方法论部分（不含 env），env 在 buildLLMMessages 时动态拼接
	s.basePrompt = prompt.BasePrompt()
	s.envSuffix = prompt.RenderEnvContext(prompt.EnvironmentContext{
		OS:       runtime.GOOS,
		Platform: runtime.GOARCH,
		Tools:    s.toolMgr.ToolNames(),
	})

	s.convs = make(map[string]*Conversation)
	s.cancels = make(map[string]context.CancelFunc)
	s.tracers = make(map[string]*trace.Tracer)

	// 从磁盘恢复已有会话到内存
	summaries, err := s.store.ListConversations()
	if err != nil {
		slog.Warn("Failed to list conversations from store", "error", err)
	} else {
		for _, summary := range summaries {
			msgs, err := s.store.LoadMessages(summary.ID)
			if err != nil {
				slog.Warn("Failed to load messages for conversation", "id", summary.ID, "error", err)
				continue
			}
			conv := &Conversation{
				ID:        summary.ID,
				Title:     summary.Title,
				Messages:  storeMessagesToAgent(msgs),
				CreatedAt: summary.CreatedAt,
				UpdatedAt: summary.UpdatedAt,
			}
			s.addConversation(conv)

			// 确保恢复的会话有 workspace 目录（旧版数据可能没有）
			if err := os.MkdirAll(s.store.WorkspaceDir(conv.ID), 0755); err != nil {
				slog.Warn("Failed to create workspace dir for restored conv", "id", conv.ID, "error", err)
			}

			// 为恢复的会话创建 tracer
			httpEp, grpcEp := s.traceEndpoints()
			if t, err := trace.NewTracer(s.store.LogsDir(conv.ID), httpEp, grpcEp); err == nil {
				s.setTracer(conv.ID, t)
			}
		}
		slog.Info("Loaded conversations from store", "count", len(s.convs))
	}

	return nil
}

// --- 会话/取消函数/追踪器的并发安全访问 ---
// convs、cancels、tracers 三个 map 的全部读写收敛到以下方法，
// 调用方不再直接操作字段。

func (s *AgentService) addConversation(conv *Conversation) {
	s.mu.Lock()
	s.convs[conv.ID] = conv
	s.mu.Unlock()
}

func (s *AgentService) removeConversation(id string) {
	s.mu.Lock()
	delete(s.convs, id)
	s.mu.Unlock()
}

func (s *AgentService) hasConversation(id string) bool {
	s.mu.RLock()
	_, ok := s.convs[id]
	s.mu.RUnlock()
	return ok
}

// getConversation 返回会话指针。调用方只允许只读访问；
// 修改会话消息请走带锁的专用路径（如 hooks 中的同步逻辑）。
func (s *AgentService) getConversation(id string) (*Conversation, bool) {
	s.mu.RLock()
	conv, ok := s.convs[id]
	s.mu.RUnlock()
	return conv, ok
}

func (s *AgentService) registerCancel(id string, cancel context.CancelFunc) {
	s.mu.Lock()
	s.cancels[id] = cancel
	s.mu.Unlock()
}

func (s *AgentService) unregisterCancel(id string) {
	s.mu.Lock()
	delete(s.cancels, id)
	s.mu.Unlock()
}

// getCancel 返回取消函数但不摘除（循环退出时统一由 unregisterCancel 清理）。
func (s *AgentService) getCancel(id string) (context.CancelFunc, bool) {
	s.mu.RLock()
	cancel, ok := s.cancels[id]
	s.mu.RUnlock()
	return cancel, ok
}

// popCancel 取出并移除取消函数（一次性语义）。
func (s *AgentService) popCancel(id string) (context.CancelFunc, bool) {
	s.mu.Lock()
	cancel, ok := s.cancels[id]
	if ok {
		delete(s.cancels, id)
	}
	s.mu.Unlock()
	return cancel, ok
}

func (s *AgentService) isRunning(id string) bool {
	s.mu.RLock()
	_, ok := s.cancels[id]
	s.mu.RUnlock()
	return ok
}

func (s *AgentService) setTracer(id string, t *trace.Tracer) {
	s.mu.Lock()
	s.tracers[id] = t
	s.mu.Unlock()
}

// removeTracer 关闭并移除会话的 tracer（释放 trace.jsonl 文件句柄）。
func (s *AgentService) removeTracer(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.tracers[id]; ok {
		if err := t.Close(); err != nil {
			slog.Warn("Failed to close tracer", "id", id, "error", err)
		}
		delete(s.tracers, id)
	}
}

func (s *AgentService) ServiceShutdown() error {
	// 分两段：先在锁内取消所有运行中的会话并收集 tracer id，
	// 解锁后再逐个关闭 tracer（removeTracer 自身会加锁）。
	s.mu.Lock()
	for _, cancel := range s.cancels {
		cancel()
	}
	tracerIDs := make([]string, 0, len(s.tracers))
	for id := range s.tracers {
		tracerIDs = append(tracerIDs, id)
	}
	s.mu.Unlock()

	for _, id := range tracerIDs {
		s.removeTracer(id)
	}
	return nil
}

func (s *AgentService) CreateConversation() (*Conversation, error) {
	now := time.Now().UnixMilli()
	id := uuid.NewString()
	if _, err := s.store.CreateConversation(id, defaultConversationTitle, now); err != nil {
		return nil, fmt.Errorf("create conversation: %w", err)
	}
	// 创建会话工作目录（工具文件操作的根目录）
	if err := os.MkdirAll(s.store.WorkspaceDir(id), 0755); err != nil {
		slog.Warn("Failed to create workspace dir", "id", id, "error", err)
	}
	conv := &Conversation{
		ID:        id,
		Title:     defaultConversationTitle,
		Messages:  []Message{},
		CreatedAt: now,
		UpdatedAt: now,
	}
	s.addConversation(conv)
	httpEp, grpcEp := s.traceEndpoints()
	if t, err := trace.NewTracer(s.store.LogsDir(id), httpEp, grpcEp); err == nil {
		s.setTracer(id, t)
	}
	s.getTracer(id).LogConversationCreated(id, defaultConversationTitle)
	return conv, nil
}

func (s *AgentService) ListConversations() ([]Conversation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	list := make([]Conversation, 0, len(s.convs))
	for _, c := range s.convs {
		list = append(list, Conversation{
			ID:        c.ID,
			Title:     c.Title,
			Messages:  []Message{},
			CreatedAt: c.CreatedAt,
			UpdatedAt: c.UpdatedAt,
		})
	}
	return list, nil
}

func (s *AgentService) GetConversation(id string) (*Conversation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	conv, ok := s.convs[id]
	if !ok {
		return nil, fmt.Errorf("conversation not found: %s", id)
	}
	cp := *conv
	cp.Messages = mergeToolOutputs(append([]Message{}, conv.Messages...))
	return &cp, nil
}

// mergeToolOutputs 将 tool 消息的执行结果按 ToolCallID 合并进 assistant
// 消息的 ToolCalls.Output，供前端 ToolCallCard 展示。只在返回的副本上操作，
// 不影响内存原始数据、持久化格式和 LLM 上下文（tool 消息仍保留原样）。
func mergeToolOutputs(msgs []Message) []Message {
	outputs := make(map[string]string)
	for _, m := range msgs {
		if m.Role == RoleTool && m.ToolCallID != "" {
			outputs[m.ToolCallID] = m.Content
		}
	}
	if len(outputs) == 0 {
		return msgs
	}
	for i := range msgs {
		if msgs[i].Role != RoleAssistant || len(msgs[i].ToolCalls) == 0 {
			continue
		}
		merged := make([]ToolCall, len(msgs[i].ToolCalls))
		for j, tc := range msgs[i].ToolCalls {
			merged[j] = tc
			if out, ok := outputs[tc.ID]; ok {
				merged[j].Output = out
			}
		}
		msgs[i].ToolCalls = merged
	}
	return msgs
}

func (s *AgentService) DeleteConversation(id string) error {
	// 若该会话正在流式生成，先取消，避免 runAgentLoop 继续写文件
	if cancel, ok := s.popCancel(id); ok {
		cancel()
	}
	// 关闭 tracer，释放 .logs/trace.jsonl 的文件句柄（Windows 下未关闭会导致 RemoveAll 失败）
	s.removeTracer(id)
	s.removeConversation(id)

	if err := s.store.DeleteConversation(id); err != nil {
		slog.Error("Failed to delete conversation from disk", "id", id, "error", err)
		return fmt.Errorf("delete conversation: %w", err)
	}
	return nil
}

func (s *AgentService) RenameConversation(id string, title string) error {
	s.mu.Lock()
	conv, ok := s.convs[id]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("conversation not found: %s", id)
	}
	conv.Title = title
	s.mu.Unlock()

	// 持久化标题
	if meta, err := s.store.LoadMeta(id); err == nil && meta != nil {
		meta.Title = title
		if saveErr := s.store.SaveMeta(id, meta); saveErr != nil {
			slog.Warn("Failed to save renamed title", "id", id, "error", saveErr)
		}
	}

	application.Get().Event.Emit("conversation:renamed", ConversationRenamedEvent{
		ConversationID: id,
		Title:          title,
	})
	return nil
}

// SendMessage 是 Agent 的入口。
func (s *AgentService) SendMessage(conversationID string, text string) (*Message, error) {
	now := time.Now().UnixMilli()
	userMsg := Message{ID: uuid.NewString(), Role: RoleUser, Content: text, CreatedAt: now}
	assistantMsg := &Message{ID: uuid.NewString(), Role: RoleAssistant, CreatedAt: now}

	s.mu.Lock()
	conv, ok := s.convs[conversationID]
	if !ok {
		s.mu.Unlock()
		return nil, fmt.Errorf("conversation not found: %s", conversationID)
	}
	if conv.Title == "" || conv.Title == defaultConversationTitle {
		conv.Title = truncateRunes(text, 20)
		newTitle := conv.Title
		// 持久化标题
		s.persistTitle(conversationID, newTitle)
		application.Get().Event.Emit("conversation:renamed", ConversationRenamedEvent{
			ConversationID: conversationID,
			Title:          newTitle,
		})
	}
	conv.Messages = append(conv.Messages, userMsg, *assistantMsg)
	conv.UpdatedAt = now
	assistantIndex := len(conv.Messages) - 1

	// 持久化用户消息
	s.persistMessage(conversationID, userMsg)

	messages := s.buildLLMMessages(conv)
	s.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	s.registerCancel(conversationID, cancel)

	// 将会话工作目录注入 ctx，工具通过 WorkDirFromCtx 读取
	convWorkDir := s.store.ResolveWorkDir(conversationID)
	ctx = tools.WithWorkDir(ctx, convWorkDir)

	go s.runAgentLoop(ctx, conversationID, assistantMsg.ID, assistantIndex, messages, text)

	return assistantMsg, nil
}

// buildLLMMessages 把会话存储里的消息转成 Eino schema.Message 格式。
// 必须在持有 s.mu 锁时调用。系统提示词动态拼接当前会话的工作目录。
func (s *AgentService) buildLLMMessages(conv *Conversation) []*schema.Message {
	msgs := make([]*schema.Message, 0, len(conv.Messages)+1)
	// 动态构建 system prompt：base + 会话工作目录 + 静态 env（OS/tools）
	convWorkDir := s.store.ResolveWorkDir(conv.ID)
	sysPrompt := s.basePrompt + "\n- Working directory: `" + convWorkDir + "`\n" + s.envSuffix
	msgs = append(msgs, schema.SystemMessage(sysPrompt))
	// reasoning 回放策略由目标供应商决定（provider 级，内置默认 + 可选覆盖）：
	// Gemini function call 回合必须回放 thinking（维持签名链），
	// DeepSeek/Qwen/ARK 工具回合禁止回传 reasoning（报 400）。
	reasoningPolicy := s.reasoningPolicy()
	for _, m := range conv.Messages {
		switch m.Role {
		case RoleUser:
			msgs = append(msgs, schema.UserMessage(m.Content))
		case RoleAssistant:
			reasoning := m.Reasoning
			if reasoningPolicy == llm.ReasoningStrip {
				reasoning = ""
			}
			if len(m.ToolCalls) > 0 {
				// 带 tool_calls 的 assistant 消息
				toolCalls := make([]schema.ToolCall, len(m.ToolCalls))
				for i, tc := range m.ToolCalls {
					toolCalls[i] = schema.ToolCall{
						ID:   tc.ID,
						Type: "function",
						Function: schema.FunctionCall{
							Name:      tc.Name,
							Arguments: tc.Args,
						},
					}
				}
				msgs = append(msgs, &schema.Message{
					Role:             schema.Assistant,
					Content:          m.Content,
					ReasoningContent: reasoning,
					ToolCalls:        toolCalls,
				})
			} else if m.Content != "" {
				msgs = append(msgs, &schema.Message{
					Role:             schema.Assistant,
					Content:          m.Content,
					ReasoningContent: reasoning,
				})
			}
		case RoleTool:
			msgs = append(msgs, schema.ToolMessage(m.Content, m.ToolCallID))
		}
	}
	return msgs
}

// reasoningPolicy 返回当前激活模型供应商的 reasoning 回放策略
//（replay/strip/keep）。配置缺失时保守取 keep（透传）。
func (s *AgentService) reasoningPolicy() string {
	cfg := s.llmReg.Config()
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

func (s *AgentService) CancelMessage(conversationID string) error {
	// 只触发取消、不摘除条目：cancels 由 runAgentLoop 退出时
	// unregisterCancel 清理，期间 isRunning 仍报告 true，
	// 避免"循环尚未退出就被允许再次发送"的竞态。
	if cancel, ok := s.getCancel(conversationID); ok {
		cancel()
	}
	return nil
}

// RetryMessage 重新生成最后一条 assistant 回复：重置该消息（清空内容与
// 工具调用记录），丢弃其后的工具结果消息，重写 jsonl，然后重跑 Agent 循环。
// 用于模型超时/出错后由用户发起的重试。会话正在流式生成时拒绝调用。
func (s *AgentService) RetryMessage(conversationID string) (*Message, error) {
	s.mu.Lock()

	conv, ok := s.convs[conversationID]
	if !ok {
		s.mu.Unlock()
		return nil, fmt.Errorf("conversation not found: %s", conversationID)
	}
	// 此处已持有 s.mu 写锁，直接读 map 安全（不能用 isRunning，会重复加锁死锁）
	if _, running := s.cancels[conversationID]; running {
		s.mu.Unlock()
		return nil, fmt.Errorf("会话正在生成中，无法重试")
	}

	// 定位最后一条 assistant 消息
	idx := -1
	for i := len(conv.Messages) - 1; i >= 0; i-- {
		if conv.Messages[i].Role == RoleAssistant {
			idx = i
			break
		}
	}
	if idx == -1 {
		s.mu.Unlock()
		return nil, fmt.Errorf("no assistant message to retry")
	}

	// 回撤：截断该消息之后的内容（残留的 tool 消息），并重置该消息本身
	conv.Messages = conv.Messages[:idx+1]
	assistantMsg := &conv.Messages[idx]
	assistantMsg.Content = ""
	assistantMsg.Reasoning = ""
	assistantMsg.ToolCalls = nil
	assistantMsg.Usage = nil
	assistantMsg.ElapsedMs = 0
	conv.UpdatedAt = time.Now().UnixMilli()

	// 重写 jsonl，去掉回撤掉的消息并重置 assistant 内容
	if err := s.rewriteMessagesLocked(conversationID, conv.Messages); err != nil {
		slog.Warn("Failed to rewrite messages on retry", "conv", conversationID, "err", err)
	}

	// 重建 LLM 上下文。重置后的 assistant 消息 content 为空且无 toolCalls，
	// buildLLMMessages 会跳过它 —— 模型将基于该消息之前的历史重新生成。
	messages := s.buildLLMMessages(conv)

	assistantID := assistantMsg.ID
	assistantIndex := idx
	s.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	s.registerCancel(conversationID, cancel)

	// 将会话工作目录注入 ctx，工具通过 WorkDirFromCtx 读取
	ctx = tools.WithWorkDir(ctx, s.store.ResolveWorkDir(conversationID))

	go s.runAgentLoop(ctx, conversationID, assistantID, assistantIndex, messages, "[retry]")

	return assistantMsg, nil
}

// DeleteMessage 删除指定消息及其之后的所有消息（undo to here）。
// 同步更新内存和 session.jsonl。如果会话正在流式生成则拒绝删除。
func (s *AgentService) DeleteMessage(conversationID, messageID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	conv, ok := s.convs[conversationID]
	if !ok {
		return fmt.Errorf("conversation not found: %s", conversationID)
	}

	// 会话正在流式生成时拒绝删除，避免与 runAgentLoop 产生竞态
	//（直接读 map 安全：此处已持有 s.mu 写锁）
	if _, running := s.cancels[conversationID]; running {
		return fmt.Errorf("会话正在生成中，无法删除消息")
	}

	// 找到目标消息的索引
	idx := -1
	for i, m := range conv.Messages {
		if m.ID == messageID {
			idx = i
			break
		}
	}
	if idx == -1 {
		return fmt.Errorf("message not found: %s", messageID)
	}

	// 删除该消息及其后所有消息
	conv.Messages = conv.Messages[:idx]
	conv.UpdatedAt = time.Now().UnixMilli()

	// 重写 session.jsonl（错误返回给前端，不再吞掉）
	if err := s.rewriteMessagesLocked(conversationID, conv.Messages); err != nil {
		slog.Error("Failed to rewrite messages after delete", "conv", conversationID, "msg", messageID, "error", err)
		return fmt.Errorf("删除消息失败（持久化）: %w", err)
	}
	return nil
}

func truncateRunes(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}

// usageToTraceUsage converts UsageInfo to trace.Usage for logging.
func usageToTraceUsage(u *UsageInfo) *trace.Usage {
	if u == nil {
		return nil
	}
	return &trace.Usage{
		PromptTokens:     u.PromptTokens,
		CompletionTokens: u.CompletionTokens,
		TotalTokens:      u.TotalTokens,
	}
}

// usageToStore / usageFromStore 在服务层 UsageInfo 与存储层 store.UsageInfo
// 之间转换（两个结构体同构，分属不同包避免循环依赖）。
func usageToStore(u *UsageInfo) *store.UsageInfo {
	if u == nil {
		return nil
	}
	return &store.UsageInfo{
		PromptTokens:     u.PromptTokens,
		CompletionTokens: u.CompletionTokens,
		TotalTokens:      u.TotalTokens,
		CachedTokens:     u.CachedTokens,
		ModelEntry:       u.ModelEntry,
	}
}

func usageFromStore(u *store.UsageInfo) *UsageInfo {
	if u == nil {
		return nil
	}
	return &UsageInfo{
		PromptTokens:     u.PromptTokens,
		CompletionTokens: u.CompletionTokens,
		TotalTokens:      u.TotalTokens,
		CachedTokens:     u.CachedTokens,
		ModelEntry:       u.ModelEntry,
	}
}

// schemaToTraceMessages converts Eino schema messages into trace.ChatMessage
// for OpenInference llm.input_messages.* attribute flattening.
func schemaToTraceMessages(msgs []*schema.Message) []trace.ChatMessage {
	out := make([]trace.ChatMessage, len(msgs))
	for i, m := range msgs {
		cm := trace.ChatMessage{
			Role:       string(m.Role),
			Content:    m.Content,
			Reasoning:  m.ReasoningContent,
			ToolCallID: m.ToolCallID,
		}
		for _, tc := range m.ToolCalls {
			cm.ToolCalls = append(cm.ToolCalls, trace.ToolCallRef{
				ID:   tc.ID,
				Name: tc.Function.Name,
				Args: tc.Function.Arguments,
			})
		}
		out[i] = cm
	}
	return out
}

// schemaToolCallsToTrace converts Eino tool calls to trace.ToolCallRef.
func schemaToolCallsToTrace(tcs []schema.ToolCall) []trace.ToolCallRef {
	out := make([]trace.ToolCallRef, len(tcs))
	for i, tc := range tcs {
		out[i] = trace.ToolCallRef{
			ID:   tc.ID,
			Name: tc.Function.Name,
			Args: tc.Function.Arguments,
		}
	}
	return out
}

// migrateLegacyDataDir renames the pre-rename data directory (~/myapp) to the
// current default (~/tars) when the latter doesn't exist yet. Best-effort:
// failures are logged but never fatal.
func migrateLegacyDataDir(newDir string) {
	if _, err := os.Stat(newDir); err == nil {
		return // 新目录已存在，无需迁移
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	oldDir := filepath.Join(home, "myapp")
	if _, err := os.Stat(oldDir); err != nil {
		return // 没有旧数据
	}
	if err := os.Rename(oldDir, newDir); err != nil {
		slog.Warn("Failed to migrate legacy data dir", "from", oldDir, "to", newDir, "error", err)
		return
	}
	slog.Info("Migrated legacy data directory", "from", oldDir, "to", newDir)
}

// getTracer returns the tracer for a conversation. If one doesn't exist
// yet (e.g. for a conversation loaded from an older version), it's created
// on demand. Returns nil-safe Tracer (methods are no-ops on nil).
func (s *AgentService) getTracer(convID string) *trace.Tracer {
	s.mu.RLock()
	t, ok := s.tracers[convID]
	s.mu.RUnlock()
	if ok {
		return t
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	// double-check after acquiring write lock
	if t, ok = s.tracers[convID]; ok {
		return t
	}
	httpEp, grpcEp := s.traceEndpoints()
	t, err := trace.NewTracer(s.store.LogsDir(convID), httpEp, grpcEp)
	if err != nil {
		slog.Warn("Failed to create tracer", "conv", convID, "error", err)
		return nil
	}
	s.tracers[convID] = t
	return t
}

// --- Persistence helpers ---

// persistMessage appends a single message to the session JSONL file.
// Errors are logged but not returned; message loss on disk is non-fatal.
func (s *AgentService) persistMessage(convID string, m Message) {
	stMsg := store.Message{
		ID:         m.ID,
		Role:       m.Role,
		Content:    m.Content,
		ToolCallID: m.ToolCallID,
		CreatedAt:  m.CreatedAt,
		Reasoning:  m.Reasoning,
		ElapsedMs:  m.ElapsedMs,
		Usage:      usageToStore(m.Usage),
	}
	for _, tc := range m.ToolCalls {
		stMsg.ToolCalls = append(stMsg.ToolCalls, store.ToolCall{
			ID:   tc.ID,
			Name: tc.Name,
			Args: tc.Args,
		})
	}
	if err := s.store.AppendMessage(convID, stMsg); err != nil {
		slog.Warn("Failed to persist message", "conv", convID, "msg", m.ID, "error", err)
	}
}

// persistAssistantSnapshot reads the current assistant message from memory
// and appends it to session.jsonl. Called after each agent loop iteration.
// Because JSONL is append-only, the same message ID may appear multiple
// times; LoadMessages deduplicates by keeping the last entry per ID.
func (s *AgentService) persistAssistantSnapshot(convID string, assistantIndex int) {
	s.mu.RLock()
	conv, ok := s.convs[convID]
	if !ok || assistantIndex >= len(conv.Messages) {
		s.mu.RUnlock()
		return
	}
	msg := conv.Messages[assistantIndex]
	s.mu.RUnlock()
	s.persistMessage(convID, msg)
}

// rewriteMessagesLocked rewrites session.jsonl with the given messages.
// Must be called while holding s.mu. Returns error if write fails.
func (s *AgentService) rewriteMessagesLocked(convID string, msgs []Message) error {
	stMsgs := make([]store.Message, len(msgs))
	for i, m := range msgs {
		stMsgs[i] = store.Message{
			ID:         m.ID,
			Role:       m.Role,
			Content:    m.Content,
			ToolCallID: m.ToolCallID,
			CreatedAt:  m.CreatedAt,
			Reasoning:  m.Reasoning,
			ElapsedMs:  m.ElapsedMs,
			Usage:      usageToStore(m.Usage),
		}
		for _, tc := range m.ToolCalls {
			stMsgs[i].ToolCalls = append(stMsgs[i].ToolCalls, store.ToolCall{
				ID:   tc.ID,
				Name: tc.Name,
				Args: tc.Args,
			})
		}
	}
	return s.store.RewriteMessages(convID, stMsgs)
}

// persistTitle saves the conversation title to meta.json.
func (s *AgentService) persistTitle(convID, title string) {
	meta, err := s.store.LoadMeta(convID)
	if err != nil || meta == nil {
		return
	}
	meta.Title = title
	if err := s.store.SaveMeta(convID, meta); err != nil {
		slog.Warn("Failed to persist title", "conv", convID, "error", err)
	}
}

// --- Store ↔ Agent type conversions ---

// storeMessagesToAgent converts store.Message slice to agent Message slice.
// Deduplicates by message ID: if the same ID appears multiple times (because
// assistant messages are snapshotted after each loop iteration), only the
// last occurrence is kept. This ensures we restore the most complete state.
func storeMessagesToAgent(msgs []store.Message) []Message {
	// Dedup: keep last occurrence per message ID
	seen := make(map[string]int, len(msgs))
	for i, m := range msgs {
		seen[m.ID] = i // overwrite with later index
	}

	// Sort indices to preserve append order
	indices := make([]int, 0, len(seen))
	for _, idx := range seen {
		indices = append(indices, idx)
	}
	sortInts(indices)

	result := make([]Message, 0, len(indices))
	for _, idx := range indices {
		m := msgs[idx]
		am := Message{
			ID:         m.ID,
			Role:       m.Role,
			Content:    m.Content,
			ToolCallID: m.ToolCallID,
			CreatedAt:  m.CreatedAt,
			Reasoning:  m.Reasoning,
			ElapsedMs:  m.ElapsedMs,
			Usage:      usageFromStore(m.Usage),
		}
		for _, tc := range m.ToolCalls {
			am.ToolCalls = append(am.ToolCalls, ToolCall{
				ID:   tc.ID,
				Name: tc.Name,
				Args: tc.Args,
			})
		}
		result = append(result, am)
	}
	return result
}

// sortInts sorts an int slice in-place.
func sortInts(s []int) {
	// Simple insertion sort for small slices (session messages are typically <1000)
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
