package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"tars/internal/agent"
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
	oteltrace "go.opentelemetry.io/otel/trace"
)

// agentHooks implements agent.Hooks for AgentService: bridges loop events to
// Wails frontend events, jsonl persistence, and OpenTelemetry tracing.
type agentHooks struct {
	sessionID      string
	assistantID    string
	assistantIndex int
	modelID        string // 真实模型名（trace 展示用）
	modelEntry     string // 配置条目 ID（usage 费用核算用）
	toolSchemas    []string
	turnCtx        context.Context // turn span ctx, for tool spans

	llmSpan      oteltrace.Span
	toolSpans    map[string]oteltrace.Span
	turnSpan     oteltrace.Span
	usage        *UsageInfo
	finalOutput  strings.Builder
	iterMessages [][]*schema.Message // assistant msg per iteration, for trace
}

func (h *agentHooks) IterationStart(ctx context.Context, iter int, messages []*schema.Message) {
	// 直接传入 schema.Message——trace 包内部按需提取字段并展平。
	// 传入完整上下文（system + 历史 user/assistant/tool + 状态栏），
	// Phoenix 据此渲染 LLM span 的 input messages 视图。
	_, span := trace.StartLLMCall(h.turnCtx, h.sessionID, h.modelID,
		prompt.SystemPrompt(), iter-1, messages, h.toolSchemas)
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
	promptTokens, completionTokens, totalTokens := 0, 0, 0
	if h.usage != nil {
		promptTokens, completionTokens, totalTokens = h.usage.PromptTokens, h.usage.CompletionTokens, h.usage.TotalTokens
	}
	trace.EndLLMCall(h.llmSpan, nil, assistantMsg.Content, assistantMsg.ReasoningContent,
		assistantMsg.ToolCalls, "stop", promptTokens, completionTokens, totalTokens)

	// Sync assistant content/reasoning/tool_calls to the in-memory
	// session and persist a snapshot (crash-safe: partial progress
	// is kept). Reasoning Accumulates across iterations, same as content.
	sessionMgr := session.GetManager()
	sessionMgr.WithSession(h.sessionID, func(c *session.SessionState) {
		if h.assistantIndex < len(c.Messages) {
			c.Messages[h.assistantIndex].Content += assistantMsg.Content
			c.Messages[h.assistantIndex].Reasoning += assistantMsg.ReasoningContent
			for _, tc := range assistantMsg.ToolCalls {
				c.Messages[h.assistantIndex].ToolCalls = append(
					c.Messages[h.assistantIndex].ToolCalls,
					store.ToolCall{ID: tc.ID, Name: tc.Function.Name, Args: tc.Function.Arguments},
				)
			}
			c.UpdatedAt = time.Now().UnixMilli()
		}
	})
	sessionMgr.AppendAssistantSnapshot(h.sessionID, h.assistantIndex)

	// Append and persist tool result messages.
	for _, m := range delta[1:] {
		if m.Role != schema.Tool {
			continue
		}
		toolMsg := store.Message{
			ID:         uuid.NewString(),
			Role:       schema.Tool,
			Content:    m.Content,
			ToolCallID: m.ToolCallID,
			CreatedAt:  time.Now().UnixMilli(),
		}
		sessionMgr.WithSession(h.sessionID, func(c *session.SessionState) {
			c.Messages = append(c.Messages, toolMsg)
		})
		sessionMgr.AppendMessage(h.sessionID, toolMsg)
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
			ModelEntry:       h.modelEntry,
		}
	}

	if chunk.ReasoningContent != "" {
		application.Get().Event.Emit("agent:reasoning", ReasoningEvent{
			SessionID: h.sessionID,
			MessageID: h.assistantID,
			Content:   chunk.ReasoningContent,
		})
	}
	if chunk.Content != "" {
		application.Get().Event.Emit("agent:chunk", StreamChunk{
			SessionID: h.sessionID,
			MessageID: h.assistantID,
			Chunk:     chunk.Content,
		})
	}
}

func (h *agentHooks) ToolsStart(ctx context.Context, calls []schema.ToolCall) {
	h.toolSpans = make(map[string]oteltrace.Span, len(calls))
	for _, c := range calls {
		_, span := trace.StartToolCall(h.turnCtx, h.sessionID, c.ID, c.Function.Name, c.Function.Arguments)
		h.toolSpans[c.ID] = span
		application.Get().Event.Emit("agent:tool", ToolEvent{
			SessionID:  h.sessionID,
			MessageID:  h.assistantID,
			ToolCallID: c.ID,
			ToolName:   c.Function.Name,
			Args:       c.Function.Arguments,
		})
	}
}

func (h *agentHooks) ToolResult(ctx context.Context, r tools.ToolResult) {
	if span, ok := h.toolSpans[r.ID]; ok {
		trace.EndToolCall(span, r.Output)
	}
	application.Get().Event.Emit("agent:tool_result", ToolResultEvent{
		SessionID:  h.sessionID,
		MessageID:  h.assistantID,
		ToolCallID: r.ID,
		Output:     r.Output,
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
		"session", h.sessionID,
		"iteration", iter,
		"attempt", attempt,
		"streamedChunks", streamedChunks,
		"err", err,
	)
	// Close this iteration's LLM span with the error (IterationEnd will not
	// fire for the failed round).
	trace.EndLLMCall(h.llmSpan, err, "", "", nil, "", 0, 0, 0)
	return false, 0
}

// runAgentLoop runs the ReAct loop via internal/agent, wiring all side
// effects (events, persistence, tracing) through agentHooks.
func (s *AgentService) runAgentLoop(ctx context.Context, sessionID, assistantID string, assistantIndex int, messages []*schema.Message, userText string) {
	startTime := time.Now()

	ctx, turnSpan := trace.StartTurn(ctx, sessionID, assistantID, userText)
	var turnErr error

	chatModel, modelCfg, err := llm.GetRegistry().Active()
	if err != nil {
		turnErr = err
		application.Get().Event.Emit("agent:error", StreamError{
			SessionID: sessionID, MessageID: assistantID, Error: turnErr.Error(),
		})
		trace.EndTurn(turnSpan, turnErr, time.Since(startTime).Milliseconds(), "")
		session.GetManager().UnregisterCancel(sessionID)
		return
	}
	modelWithTools, err := chatModel.WithTools(tools.DefaultManager().ToolInfos())
	if err != nil {
		turnErr = fmt.Errorf("failed to bind tools: %w", err)
		application.Get().Event.Emit("agent:error", StreamError{
			SessionID: sessionID, MessageID: assistantID, Error: turnErr.Error(),
		})
		trace.EndTurn(turnSpan, turnErr, time.Since(startTime).Milliseconds(), "")
		session.GetManager().UnregisterCancel(sessionID)
		return
	}

	cfg := config.Get()
	hooks := &agentHooks{
		sessionID:      sessionID,
		assistantID:    assistantID,
		assistantIndex: assistantIndex,
		modelID:        modelCfg.ModelId, // 真实模型名，用于 trace 展示
		modelEntry:     modelCfg.ID,      // 配置条目 ID，用于费用核算
		toolSchemas:    tools.DefaultManager().ToolSchemasJSON(),
		turnCtx:        ctx,
		turnSpan:       turnSpan,
	}

	ag := agent.New(sessionID, cfg.Agent.MaxIterations, cfg.Agent.IterationTimeout, tools.DefaultManager(), modelWithTools)

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
				SessionID: sessionID, MessageID: assistantID,
				ElapsedMs: elapsedMs,
			})
		} else {

			turnErr = err
			llm.GetRegistry().SetHealthy(modelCfg.ID, false)
			// Classify iteration timeouts so the frontend can show a
			// targeted hint ("provider congested, retry?") instead of a
			// generic failure.
			kind := "error"
			if errors.Is(err, context.DeadlineExceeded) {
				kind = "timeout"
			}
			application.Get().Event.Emit("agent:error", StreamError{
				SessionID: sessionID, MessageID: assistantID, Error: err.Error(), Kind: kind,
			})
		}
	} else {
		llm.GetRegistry().SetHealthy(modelCfg.ID, true)
		application.Get().Event.Emit("agent:done", StreamDone{
			SessionID: sessionID, MessageID: assistantID, Usage: hooks.usage,
			ElapsedMs: elapsedMs,
		})
	}

	// 把本轮的 token 统计与总耗时写入内存会话并持久化，
	// 历史会话重新打开后每条消息的用量信息才能恢复。
	sessionMgr := session.GetManager()
	sessionMgr.WithSession(sessionID, func(c *session.SessionState) {
		if assistantIndex < len(c.Messages) {
			c.Messages[assistantIndex].Usage = hooks.usage
			c.Messages[assistantIndex].ElapsedMs = elapsedMs
		}
	})
	sessionMgr.AppendAssistantSnapshot(sessionID, assistantIndex)

	trace.EndTurn(turnSpan, turnErr, elapsedMs, finalOutput)
	sessionMgr.UnregisterCancel(sessionID)
}
