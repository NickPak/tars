package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"tars/internal/agent"
	"tars/pkg/tools"
	"tars/pkg/trace"

	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
	"github.com/wailsapp/wails/v3/pkg/application"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// agentHooks implements agent.Hooks for AgentService: bridges loop events to
// Wails frontend events, jsonl persistence, and OpenTelemetry tracing.
type agentHooks struct {
	svc            *AgentService
	conversationID string
	assistantID    string
	assistantIndex int
	tracer         *trace.Tracer
	modelID        string
	basePrompt     string
	toolSchemas    []string
	turnCtx        context.Context // turn span ctx, for tool spans

	llmSpan      oteltrace.Span
	toolSpans    map[string]oteltrace.Span
	turnSpan     oteltrace.Span
	usage        *UsageInfo
	finalOutput  strings.Builder
	iterMessages [][]*schema.Message // assistant msg per iteration, for trace
}

func (h *agentHooks) IterationStart(ctx context.Context, iter int) {
	// Start an LLM span for this iteration. Messages for the trace payload are
	// assembled lazily by the caller; we only need prompt + tools here.
	_, span := h.tracer.StartLLMCall(h.turnCtx, h.conversationID, h.modelID,
		h.basePrompt, iter-1, nil, h.toolSchemas)
	h.llmSpan = span
}

// IterationEnd fires after one full iteration (LLM call + any tool runs).
// delta[0] is this round's assistant message; delta[1:] are tool results.
// It fires every round — including the final plain-text round — so all
// assistant content is durably persisted here.
func (h *agentHooks) IterationEnd(ctx context.Context, iter int, full []*schema.Message, delta []*schema.Message) {
	if len(delta) == 0 {
		return
	}

	assistantMsg := delta[0]
	h.finalOutput.WriteString(assistantMsg.Content)

	// End the LLM span for this iteration.
	h.tracer.EndLLMCall(h.llmSpan, nil, assistantMsg.Content, assistantMsg.ReasoningContent,
		schemaToolCallsToTrace(assistantMsg.ToolCalls), "stop", usageToTraceUsage(h.usage))

	// Sync assistant content/tool_calls to the in-memory conversation and
	// persist a snapshot (crash-safe: partial progress is kept).
	h.svc.mu.Lock()
	if c, ok := h.svc.convs[h.conversationID]; ok && h.assistantIndex < len(c.Messages) {
		c.Messages[h.assistantIndex].Content += assistantMsg.Content
		for _, tc := range assistantMsg.ToolCalls {
			c.Messages[h.assistantIndex].ToolCalls = append(
				c.Messages[h.assistantIndex].ToolCalls,
				ToolCall{ID: tc.ID, Name: tc.Function.Name, Args: tc.Function.Arguments},
			)
		}
		c.UpdatedAt = time.Now().UnixMilli()
	}
	h.svc.mu.Unlock()
	h.svc.persistAssistantSnapshot(h.conversationID, h.assistantIndex)

	// Append and persist tool result messages.
	for _, m := range delta[1:] {
		if m.Role != schema.Tool {
			continue
		}
		toolMsg := Message{
			ID:         uuid.NewString(),
			Role:       RoleTool,
			Content:    m.Content,
			ToolCallID: m.ToolCallID,
			CreatedAt:  time.Now().UnixMilli(),
		}
		h.svc.mu.Lock()
		if c, ok := h.svc.convs[h.conversationID]; ok {
			c.Messages = append(c.Messages, toolMsg)
		}
		h.svc.mu.Unlock()
		h.svc.persistMessage(h.conversationID, toolMsg)
	}
}

func (h *agentHooks) StreamChunk(ctx context.Context, iter int, chunk *schema.Message) {
	// Capture usage from the final frame.
	if chunk.ResponseMeta != nil && chunk.ResponseMeta.Usage != nil {
		h.usage = &UsageInfo{
			PromptTokens:     chunk.ResponseMeta.Usage.PromptTokens,
			CompletionTokens: chunk.ResponseMeta.Usage.CompletionTokens,
			TotalTokens:      chunk.ResponseMeta.Usage.TotalTokens,
			CachedTokens:     chunk.ResponseMeta.Usage.PromptTokenDetails.CachedTokens,
		}
	}

	if chunk.ReasoningContent != "" {
		application.Get().Event.Emit("agent:reasoning", ReasoningEvent{
			ConversationID: h.conversationID,
			MessageID:      h.assistantID,
			Content:        chunk.ReasoningContent,
		})
	}
	if chunk.Content != "" {
		application.Get().Event.Emit("agent:chunk", StreamChunk{
			ConversationID: h.conversationID,
			MessageID:      h.assistantID,
			Chunk:          chunk.Content,
		})
	}
}

func (h *agentHooks) ToolsStart(ctx context.Context, calls []schema.ToolCall) {
	h.toolSpans = make(map[string]oteltrace.Span, len(calls))
	for _, c := range calls {
		_, span := h.tracer.StartToolCall(h.turnCtx, h.conversationID, c.ID, c.Function.Name, c.Function.Arguments)
		h.toolSpans[c.ID] = span
		application.Get().Event.Emit("agent:tool", ToolEvent{
			ConversationID: h.conversationID,
			MessageID:      h.assistantID,
			ToolCallID:     c.ID,
			ToolName:       c.Function.Name,
			Args:           c.Function.Arguments,
		})
	}
}

func (h *agentHooks) ToolResult(ctx context.Context, r tools.ToolResult) {
	if span, ok := h.toolSpans[r.ID]; ok {
		h.tracer.EndToolCall(span, r.Output)
	}
	application.Get().Event.Emit("agent:tool_result", ToolResultEvent{
		ConversationID: h.conversationID,
		MessageID:      h.assistantID,
		ToolCallID:     r.ID,
		Output:         r.Output,
	})
}

// ToolsEnd currently has nothing to do — per-tool spans are ended in
// ToolResult, and round-level persistence happens in IterationEnd. It exists
// to keep the hook lifecycle symmetric and as an extension point (e.g. a
// future "all tools finished" batch event for the frontend).
func (h *agentHooks) ToolsEnd(ctx context.Context, results []tools.ToolResult) {}

// OnError reports a failed model call. Policy: never auto-retry — close the
// iteration's LLM span with the error and abort, letting runAgentLoop emit
// agent:error so the frontend can surface the failure and the user decides
// whether to retry (via RetryMessage).
//
// Rationale: when streamedChunks > 0 the partial output has already been
// rendered, and an in-place retry would duplicate it; when streamedChunks ==
// 0 a retry would silently extend an already long wait (provider congestion
// is the common case). Either way the user should decide.
func (h *agentHooks) OnError(ctx context.Context, iter, attempt, streamedChunks int, err error) (bool, time.Duration) {
	slog.Warn("Model call failed",
		"conv", h.conversationID,
		"iteration", iter,
		"attempt", attempt,
		"streamedChunks", streamedChunks,
		"err", err,
	)
	// Close this iteration's LLM span with the error (IterationEnd will not
	// fire for the failed round).
	h.tracer.EndLLMCall(h.llmSpan, err, "", "", nil, "", nil)
	return false, 0
}


// runAgentLoop runs the ReAct loop via internal/agent, wiring all side
// effects (events, persistence, tracing) through agentHooks.
func (s *AgentService) runAgentLoop(ctx context.Context, conversationID, assistantID string, assistantIndex int, messages []*schema.Message, userText string) {
	tracer := s.getTracer(conversationID)
	startTime := time.Now()

	ctx, turnSpan := tracer.StartTurn(ctx, conversationID, assistantID, userText)
	var turnErr error

	chatModel := s.llmClient.ChatModel()
	modelWithTools, err := chatModel.WithTools(s.toolMgr.ToolInfos())
	if err != nil {
		turnErr = fmt.Errorf("failed to bind tools: %w", err)
		application.Get().Event.Emit("agent:error", StreamError{
			ConversationID: conversationID, MessageID: assistantID, Error: turnErr.Error(),
		})
		tracer.EndTurn(turnSpan, turnErr, time.Since(startTime).Milliseconds(), "")
		s.unregisterCancel(conversationID)
		return
	}

	hooks := &agentHooks{
		svc:            s,
		conversationID: conversationID,
		assistantID:    assistantID,
		assistantIndex: assistantIndex,
		tracer:         tracer,
		modelID:        s.appConfig.LLM.ModelId,
		basePrompt:     s.basePrompt,
		toolSchemas:    s.toolMgr.ToolSchemasJSON(),
		turnCtx:        ctx,
		turnSpan:       turnSpan,
	}

	ag := agent.New(modelWithTools, s.toolMgr, s.appConfig.MaxIterationsOrDefault())
	// Generous per-iteration deadline: busy providers may queue requests.
	ag.IterationTimeout = iterationTimeout

	result, err := ag.Run(ctx, messages, hooks)

	var finalOutput string
	if result != nil && result.FinalMessage != nil {
		finalOutput = result.FinalMessage.Content
	} else {
		finalOutput = hooks.finalOutput.String()
	}

	elapsedMs := time.Since(startTime).Milliseconds()

	if err != nil {
		// Cancel is treated as a clean stop, not an error.
		if ctx.Err() != nil {
			application.Get().Event.Emit("agent:done", StreamDone{
				ConversationID: conversationID, MessageID: assistantID,
				ElapsedMs: elapsedMs,
			})
		} else {
			turnErr = err
			s.setModelHealthy(false)
			// Classify iteration timeouts so the frontend can show a
			// targeted hint ("provider congested, retry?") instead of a
			// generic failure.
			kind := "error"
			if errors.Is(err, context.DeadlineExceeded) {
				kind = "timeout"
			}
			application.Get().Event.Emit("agent:error", StreamError{
				ConversationID: conversationID, MessageID: assistantID, Error: err.Error(), Kind: kind,
			})
		}
	} else {
		s.setModelHealthy(true)
		application.Get().Event.Emit("agent:done", StreamDone{
			ConversationID: conversationID, MessageID: assistantID, Usage: hooks.usage,
			ElapsedMs: elapsedMs,
		})
	}

	// 把本轮的 token 统计与总耗时写入内存会话并持久化，
	// 历史会话重新打开后每条消息的用量信息才能恢复。
	s.mu.Lock()
	if c, ok := s.convs[conversationID]; ok && assistantIndex < len(c.Messages) {
		c.Messages[assistantIndex].Usage = hooks.usage
		c.Messages[assistantIndex].ElapsedMs = elapsedMs
	}
	s.mu.Unlock()
	s.persistAssistantSnapshot(conversationID, assistantIndex)

	tracer.EndTurn(turnSpan, turnErr, elapsedMs, finalOutput)
	s.unregisterCancel(conversationID)
}
