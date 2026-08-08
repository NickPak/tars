package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"tars/internal/config"
	"tars/pkg/llm"
	"tars/pkg/prompt"
	"tars/pkg/store"
	"tars/pkg/tools"
	"tars/pkg/trace"

	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
	"github.com/wailsapp/wails/v3/pkg/application"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// Message roles.
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

// defaultConversationTitle 新会话默认标题，首次发消息时替换为用户输入的截断。
const defaultConversationTitle = "新会话"

// agentLoopMaxIterations 防止模型陷入死循环烧 token。
const agentLoopMaxIterations = 25

// Message is a single chat message within a conversation.
type Message struct {
	ID         string     `json:"id"`
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"toolCalls,omitempty"`
	ToolCallID string     `json:"toolCallId,omitempty"`
	CreatedAt  int64      `json:"createdAt"`
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
}

// StreamError is the payload of the "agent:error" event.
type StreamError struct {
	ConversationID string `json:"conversationId"`
	MessageID      string `json:"messageId"`
	Error          string `json:"error"`
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

// AgentService exposes the agent backend to the frontend.
type AgentService struct {
	appConfig    *config.AppConfig
	llmClient    *llm.Client
	toolMgr      *tools.Manager
	workDir      string
	convsDir     string // root dir for conversation storage
	basePrompt   string // stable base prompt (without env context)
	envSuffix    string // env context that doesn't change per-conversation (OS, tools)
	otlpHTTPEndpoint string // optional OTLP/HTTP collector endpoint (Jaeger)
	otlpGrpcEndpoint string // optional OTLP/gRPC collector endpoint (Phoenix)
	store        *store.Store

	mu      sync.RWMutex
	convs   map[string]*Conversation
	cancels map[string]context.CancelFunc
	tracers map[string]*trace.Tracer
}

func (s *AgentService) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
	appConfig, err := config.LoadAppConfig("config/config.yaml")
	if err != nil {
		slog.Error("Failed loading app config", "error", err)
		return err
	}
	s.appConfig = appConfig
	s.otlpHTTPEndpoint, s.otlpGrpcEndpoint = appConfig.OTLPEndpoints()
	if s.otlpHTTPEndpoint != "" {
		slog.Info("OTLP/HTTP trace export enabled", "endpoint", s.otlpHTTPEndpoint)
	}
	if s.otlpGrpcEndpoint != "" {
		slog.Info("OTLP/gRPC trace export enabled", "endpoint", s.otlpGrpcEndpoint)
	}

	llmClient, err := llm.NewClient(appConfig.LLM)
	if err != nil {
		slog.Error("Failed to create LLM client", "error", err)
		return err
	}
	s.llmClient = llmClient

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
	s.toolMgr.Register(tools.ReadFile(workDir))
	s.toolMgr.Register(tools.SearchReplace(workDir))
	s.toolMgr.Register(tools.ListDir(workDir))
	s.toolMgr.Register(tools.SearchText(workDir))
	s.toolMgr.Register(tools.RunCommand(workDir))

	// base prompt 只含方法论部分（不含 env），env 在 buildLLMMessages 时动态拼接
	s.basePrompt = prompt.BasePrompt()
	s.envSuffix = prompt.RenderEnvContext(prompt.EnvironmentContext{
		OS:       runtime.GOOS,
		Platform: runtime.GOARCH,
		Tools:    s.toolMgr.ToolNames(),
	})

	s.mu.Lock()
	defer s.mu.Unlock()
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
			s.convs[conv.ID] = conv

			// 确保恢复的会话有 workspace 目录（旧版数据可能没有）
			if err := os.MkdirAll(s.store.WorkspaceDir(conv.ID), 0755); err != nil {
				slog.Warn("Failed to create workspace dir for restored conv", "id", conv.ID, "error", err)
			}

			// 为恢复的会话创建 tracer
			if t, err := trace.NewTracer(s.store.LogsDir(conv.ID), s.otlpHTTPEndpoint, s.otlpGrpcEndpoint); err == nil {
				s.tracers[conv.ID] = t
			}
		}
		slog.Info("Loaded conversations from store", "count", len(s.convs))
	}

	return nil
}

func (s *AgentService) ServiceShutdown() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, cancel := range s.cancels {
		cancel()
	}
	for id, t := range s.tracers {
		t.Close()
		delete(s.tracers, id)
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
	s.mu.Lock()
	s.convs[conv.ID] = conv
	if t, err := trace.NewTracer(s.store.LogsDir(id), s.otlpHTTPEndpoint, s.otlpGrpcEndpoint); err == nil {
		s.tracers[id] = t
	}
	s.mu.Unlock()
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
	s.mu.Lock()
	// 若该会话正在流式生成，先取消，避免 runAgentLoop 继续写文件
	if cancel, ok := s.cancels[id]; ok {
		cancel()
		delete(s.cancels, id)
	}
	// 关闭 tracer，释放 .logs/trace.jsonl 的文件句柄（Windows 下未关闭会导致 RemoveAll 失败）
	if t, ok := s.tracers[id]; ok {
		if err := t.Close(); err != nil {
			slog.Warn("Failed to close tracer before delete", "id", id, "error", err)
		}
		delete(s.tracers, id)
	}
	delete(s.convs, id)
	s.mu.Unlock()

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

	ctx, cancel := context.WithCancel(context.Background())
	s.cancels[conversationID] = cancel
	s.mu.Unlock()

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
	for _, m := range conv.Messages {
		switch m.Role {
		case RoleUser:
			msgs = append(msgs, schema.UserMessage(m.Content))
		case RoleAssistant:
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
				msgs = append(msgs, schema.AssistantMessage(m.Content, toolCalls))
			} else if m.Content != "" {
				msgs = append(msgs, schema.AssistantMessage(m.Content, nil))
			}
		case RoleTool:
			msgs = append(msgs, schema.ToolMessage(m.Content, m.ToolCallID))
		}
	}
	return msgs
}

// runAgentLoop 是核心 ReAct 循环：流式请求 → 实时推送 → 检测 tool_calls → 执行 → 回填 → 再请求。
// 使用 Eino 的 Stream API，让 reasoning 和 content 都能实时推送给前端。
func (s *AgentService) runAgentLoop(ctx context.Context, conversationID, assistantID string, assistantIndex int, messages []*schema.Message, userText string) {
	tracer := s.getTracer(conversationID)
	startTime := time.Now()

	// 开启本轮 agent turn 的根 span（OpenTelemetry）
	ctx, turnSpan := tracer.StartTurn(ctx, conversationID, assistantID, userText)
	var turnErr error
	// 累积本轮所有迭代的回复内容，作为根 span 的 output.value
	//（Phoenix Sessions 的 Turns 视图用它显示 turn 输出）
	var finalOutput strings.Builder

	defer func() {
		tracer.EndTurn(turnSpan, turnErr, time.Since(startTime).Milliseconds(), finalOutput.String())
		s.mu.Lock()
		delete(s.cancels, conversationID)
		s.mu.Unlock()
	}()

	emitError := func(err error) {
		turnErr = err
		application.Get().Event.Emit("agent:error", StreamError{
			ConversationID: conversationID, MessageID: assistantID, Error: err.Error(),
		})
	}
	emitDone := func(usage *UsageInfo) {
		application.Get().Event.Emit("agent:done", StreamDone{
			ConversationID: conversationID, MessageID: assistantID, Usage: usage,
			ElapsedMs: time.Since(startTime).Milliseconds(),
		})
	}

	toolInfos := s.toolMgr.ToolInfos()
	toolSchemas := s.toolMgr.ToolSchemasJSON()

	chatModel := s.llmClient.ChatModel()
	modelWithTools, err := chatModel.WithTools(toolInfos)
	if err != nil {
		emitError(fmt.Errorf("failed to bind tools: %w", err))
		return
	}

	for iter := 0; iter < agentLoopMaxIterations; iter++ {
		// 用流式 API，实时推送 reasoning 和 content
		llmCtx, llmSpan := tracer.StartLLMCall(ctx, conversationID, s.appConfig.LLM.ModelId,
			s.basePrompt, iter, schemaToTraceMessages(messages), toolSchemas)

		reader, err := modelWithTools.Stream(llmCtx, messages)
		if err != nil {
			tracer.EndLLMCall(llmSpan, err, "", "", nil, "", nil)
			if errors.Is(err, context.Canceled) {
				emitDone(nil)
				return
			}
			emitError(err)
			return
		}

		// 收集完整的 assistant 消息（用于回放到历史）
		var fullContent strings.Builder
		var fullReasoning strings.Builder
		var toolCalls []schema.ToolCall
		var usage *UsageInfo

		for {
			msg, err := reader.Recv()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				tracer.EndLLMCall(llmSpan, err, "", "", nil, "", nil)
				if errors.Is(err, context.Canceled) {
					emitDone(nil)
					return
				}
				emitError(err)
				return
			}

			// 收集 usage（最后一帧才有）
			if msg.ResponseMeta != nil && msg.ResponseMeta.Usage != nil {
				usage = &UsageInfo{
					PromptTokens:     msg.ResponseMeta.Usage.PromptTokens,
					CompletionTokens: msg.ResponseMeta.Usage.CompletionTokens,
					TotalTokens:      msg.ResponseMeta.Usage.TotalTokens,
				}
			}

			// 实时推送 reasoning
			if msg.ReasoningContent != "" {
				fullReasoning.WriteString(msg.ReasoningContent)
				application.Get().Event.Emit("agent:reasoning", ReasoningEvent{
					ConversationID: conversationID,
					MessageID:      assistantID,
					Content:        msg.ReasoningContent,
				})
			}

			// 实时推送 content
			if msg.Content != "" {
				fullContent.WriteString(msg.Content)
				application.Get().Event.Emit("agent:chunk", StreamChunk{
					ConversationID: conversationID,
					MessageID:      assistantID,
					Chunk:          msg.Content,
				})
			}

			// 收集 tool_calls
			if len(msg.ToolCalls) > 0 {
				toolCalls = append(toolCalls, msg.ToolCalls...)
			}
		}
		reader.Close()

		// 构造完整的 assistant 消息追加到历史
		resp := &schema.Message{
			Role:             schema.Assistant,
			Content:          fullContent.String(),
			ReasoningContent: fullReasoning.String(),
			ToolCalls:        toolCalls,
		}
		messages = append(messages, resp)
		finalOutput.WriteString(resp.Content)

		// 同步到会话存储
		s.mu.Lock()
		if c, ok := s.convs[conversationID]; ok && assistantIndex < len(c.Messages) {
			c.Messages[assistantIndex].Content += resp.Content
			if len(resp.ToolCalls) > 0 {
				for _, tc := range resp.ToolCalls {
					c.Messages[assistantIndex].ToolCalls = append(
						c.Messages[assistantIndex].ToolCalls,
						ToolCall{ID: tc.ID, Name: tc.Function.Name, Args: tc.Function.Arguments},
					)
				}
			}
			c.UpdatedAt = time.Now().UnixMilli()
		}
		s.mu.Unlock()

		// 持久化 assistant 消息当前状态（每轮迭代后写入，确保中断不丢数据）
		s.persistAssistantSnapshot(conversationID, assistantIndex)

		// 记录本轮 LLM 响应并结束 span
		tracer.EndLLMCall(llmSpan, nil, resp.Content, resp.ReasoningContent,
			schemaToolCallsToTrace(resp.ToolCalls), "stop", usageToTraceUsage(usage))

		// 没有工具调用 → 最终回复完成
		if len(resp.ToolCalls) == 0 {
			emitDone(usage)
			return
		}

		// 有工具调用 → 先推送工具调用事件并开始 tool span
		toolSpans := make(map[string]oteltrace.Span, len(resp.ToolCalls))
		for _, tc := range resp.ToolCalls {
			_, tspan := tracer.StartToolCall(ctx, conversationID, tc.ID, tc.Function.Name, tc.Function.Arguments)
			toolSpans[tc.ID] = tspan
			application.Get().Event.Emit("agent:tool", ToolEvent{
				ConversationID: conversationID,
				MessageID:      assistantID,
				ToolCallID:     tc.ID,
				ToolName:       tc.Function.Name,
				Args:           tc.Function.Arguments,
			})
		}

		// 并行执行工具
		results := s.toolMgr.ExecuteWithResults(ctx, resp.ToolCalls, func(r tools.ToolResult) {
			tracer.EndToolCall(toolSpans[r.ID], r.Output)
			application.Get().Event.Emit("agent:tool_result", ToolResultEvent{
				ConversationID: conversationID,
				MessageID:      assistantID,
				ToolCallID:     r.ID,
				Output:         r.Output,
			})
		})

		// 工具结果追加到历史和存储
		for _, r := range results {
			toolMsg := schema.ToolMessage(r.Output, r.ID)
			messages = append(messages, toolMsg)

			now := time.Now().UnixMilli()
			agentToolMsg := Message{
				ID:         uuid.NewString(),
				Role:       RoleTool,
				Content:    r.Output,
				ToolCallID: r.ID,
				CreatedAt:  now,
			}
			s.mu.Lock()
			if c, ok := s.convs[conversationID]; ok {
				c.Messages = append(c.Messages, agentToolMsg)
			}
			s.mu.Unlock()

			// 持久化工具结果
			s.persistMessage(conversationID, agentToolMsg)
		}
	}

	emitError(fmt.Errorf("agent loop exceeded %d iterations", agentLoopMaxIterations))
}

func (s *AgentService) CancelMessage(conversationID string) error {
	s.mu.Lock()
	cancel, ok := s.cancels[conversationID]
	s.mu.Unlock()
	if ok {
		cancel()
	}
	return nil
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
	t, err := trace.NewTracer(s.store.LogsDir(convID), s.otlpHTTPEndpoint, s.otlpGrpcEndpoint)
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
