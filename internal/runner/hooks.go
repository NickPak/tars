package runner

import (
	"context"
	"log/slog"
	"time"

	"tars/internal/event"
	"tars/pkg/store"
	"tars/pkg/tools"
	"tars/pkg/trace"

	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// agent.Hooks 实现：把轮内循环事件经 event.Sink 发往前端、
// jsonl 持久化与 OpenTelemetry 追踪。

func (t *turn) IterationStart(ctx context.Context, iter int, messages []*schema.Message) {
	// 传入完整上下文（system + 历史 user/assistant/tool + 状态栏），
	// Phoenix 据此渲染 LLM span 的 input messages 视图。
	_, span := trace.StartLLMCall(t.turnCtx, t.sess.ID, t.modelID,
		t.deps.SysMsg.Content, iter-1, messages, t.toolSchemas)
	t.llmSpan = span
}

func (t *turn) IterationEnd(ctx context.Context, iter int, full []*schema.Message, delta []*schema.Message) {
	if len(delta) == 0 {
		return
	}

	assistantMsg := delta[0]
	t.finalOutput.WriteString(assistantMsg.Content)

	// End the LLM span for this iteration.
	promptTokens, completionTokens, totalTokens := 0, 0, 0
	if t.usage != nil {
		promptTokens, completionTokens, totalTokens = t.usage.PromptTokens, t.usage.CompletionTokens, t.usage.TotalTokens
	}
	trace.EndLLMCall(t.llmSpan, nil, assistantMsg.Content, assistantMsg.ReasoningContent,
		assistantMsg.ToolCalls, "stop", promptTokens, completionTokens, totalTokens)

	// 同步 assistant 增量到内存锚点（聚合字段 + 按迭代的 Parts），
	// 然后追加快照到 jsonl——快照日志按消息 ID 去重，崩溃安全：部分进度不丢。
	sess := t.sess
	if t.assistantIndex < len(sess.Messages) {
		m := sess.Messages[t.assistantIndex]
		m.Content += assistantMsg.Content
		m.Reasoning += assistantMsg.ReasoningContent
		part := store.MessagePart{Content: assistantMsg.Content}
		for _, tc := range assistantMsg.ToolCalls {
			stc := store.ToolCall{ID: tc.ID, Name: tc.Function.Name, Args: tc.Function.Arguments}
			m.ToolCalls = append(m.ToolCalls, stc)
			part.ToolCalls = append(part.ToolCalls, stc)
		}
		m.Parts = append(m.Parts, part)
		sess.UpdatedAt = time.Now().UnixMilli()
		if err := t.deps.Store.AppendMessage(sess.ID, m); err != nil {
			slog.Warn("Failed to snapshot assistant message", "id", sess.ID, "error", err)
		}
	}

	// 工具结果消息：追加内存并持久化。
	for _, m := range delta[1:] {
		if m.Role != schema.Tool {
			continue
		}
		now := time.Now().UnixMilli()
		sess.AppendMessage(now, &store.Message{
			ID:         uuid.NewString(),
			Role:       schema.Tool,
			Content:    m.Content,
			ToolCallID: m.ToolCallID,
			CreatedAt:  now,
		})
	}
}

func (t *turn) StreamChunk(ctx context.Context, iter int, chunk *schema.Message) {
	// Capture usage from the final frame.
	if chunk.ResponseMeta != nil && chunk.ResponseMeta.Usage != nil {
		t.usage = &store.UsageInfo{
			PromptTokens:     chunk.ResponseMeta.Usage.PromptTokens,
			CompletionTokens: chunk.ResponseMeta.Usage.CompletionTokens,
			TotalTokens:      chunk.ResponseMeta.Usage.TotalTokens,
			CachedTokens:     chunk.ResponseMeta.Usage.PromptTokenDetails.CachedTokens,
			ModelEntry:       t.modelEntry,
		}
	}

	if chunk.ReasoningContent != "" {
		t.sink.Reasoning(event.ReasoningEvent{
			SessionID: t.sess.ID,
			MessageID: t.assistantID,
			Content:   chunk.ReasoningContent,
		})
	}
	if chunk.Content != "" {
		t.sink.Chunk(event.StreamChunk{
			SessionID: t.sess.ID,
			MessageID: t.assistantID,
			Chunk:     chunk.Content,
		})
	}
}

func (t *turn) ToolsStart(ctx context.Context, calls []schema.ToolCall) {
	t.toolSpans = make(map[string]oteltrace.Span, len(calls))
	for _, c := range calls {
		_, span := trace.StartToolCall(t.turnCtx, t.sess.ID, c.ID, c.Function.Name, c.Function.Arguments)
		t.toolSpans[c.ID] = span
		t.sink.Tool(event.ToolEvent{
			SessionID:  t.sess.ID,
			MessageID:  t.assistantID,
			ToolCallID: c.ID,
			ToolName:   c.Function.Name,
			Args:       c.Function.Arguments,
		})
	}
}

func (t *turn) ToolResult(ctx context.Context, r tools.ToolResult) {
	if span, ok := t.toolSpans[r.ID]; ok {
		trace.EndToolCall(span, r.Output)
	}
	t.sink.ToolResult(event.ToolResultEvent{
		SessionID:  t.sess.ID,
		MessageID:  t.assistantID,
		ToolCallID: r.ID,
		Output:     r.Output,
	})
}

func (t *turn) ToolsEnd(ctx context.Context, results []tools.ToolResult) {}

func (t *turn) OnError(ctx context.Context, iter, attempt, streamedChunks int, err error) (bool, time.Duration) {
	slog.Warn("Model call failed",
		"session", t.sess.ID,
		"iteration", iter,
		"attempt", attempt,
		"streamedChunks", streamedChunks,
		"err", err,
	)
	// Close this iteration's LLM span with the error (IterationEnd will not
	// fire for the failed round).
	trace.EndLLMCall(t.llmSpan, err, "", "", nil, "", 0, 0, 0)
	return false, 0
}
