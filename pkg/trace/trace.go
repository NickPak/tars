// Package trace provides OpenTelemetry-based tracing for agent conversations.
//
// Each conversation gets its own Tracer backed by a dedicated TracerProvider
// whose file exporter appends every completed span as one JSON line to the
// conversation's .logs/trace.jsonl. An optional OTLP/HTTP exporter mirrors
// spans to a collector (Jaeger, Tempo, ...) when an endpoint is configured.
//
// Span tree for one SendMessage turn (ReAct loop):
//
//	agent.turn                     (root, one per user message)
//	├── gen_ai.chat <model>        (LLM request, iteration 0)
//	│   ├── gen_ai.execute_tool read_file
//	│   └── gen_ai.execute_tool search_replace
//	├── gen_ai.chat <model>        (LLM request, iteration 1)
//	└── gen_ai.chat <model>        (final answer, no tool calls)
package trace

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// Span names following OpenTelemetry GenAI semantic conventions.
const (
	SpanAgentTurn   = "agent.turn"
	SpanLLMChat     = "gen_ai.chat"
	SpanToolExecute = "gen_ai.execute_tool"
	SpanConvCreated = "conversation.created"
)

// Usage holds token usage for one LLM call.
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

// ChatMessage is a provider-agnostic chat message for tracing.
// Converted to OpenInference llm.input_messages.N.message.* attributes.
type ChatMessage struct {
	Role       string        // "system" | "user" | "assistant" | "tool"
	Content    string        // text content
	Reasoning  string        // thinking/reasoning content (assistant only)
	ToolCallID string        // set on role=tool messages
	ToolCalls  []ToolCallRef // set on role=assistant messages that requested tools
}

// ToolCallRef is one tool invocation requested by the model.
type ToolCallRef struct {
	ID   string
	Name string
	Args string
}

// Tracer emits OpenTelemetry spans for one conversation.
// All methods are nil-safe: calling them on a nil Tracer is a no-op,
// and start methods return the incoming ctx with a no-op span.
type Tracer struct {
	tp     *sdktrace.TracerProvider
	tracer oteltrace.Tracer
	path   string
}

// NewTracer creates a Tracer writing spans to {logDir}/trace.jsonl.
// Spans are additionally mirrored to OTLP collectors when endpoints are
// given: httpEndpoint for OTLP/HTTP (e.g. "localhost:4318", Jaeger) and
// grpcEndpoint for OTLP/gRPC (e.g. "localhost:4317", Arize Phoenix).
// Both can be enabled at the same time. Returns nil Tracer when logDir is empty.
func NewTracer(logDir, httpEndpoint, grpcEndpoint string) (*Tracer, error) {
	if logDir == "" {
		return nil, nil
	}
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil, err
	}
	path := filepath.Join(logDir, "trace.jsonl")
	fileExp, err := newFileExporter(path)
	if err != nil {
		return nil, err
	}

	res, err := resource.New(context.Background(),
		resource.WithAttributes(attribute.String("service.name", "tars")),
	)
	if err != nil {
		res = resource.Default()
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		// Keep full message/tool content in attributes (debug traces).
		sdktrace.WithRawSpanLimits(sdktrace.SpanLimits{
			AttributeValueLengthLimit: -1,
			AttributeCountLimit:       -1,
		}),
	)
	// Simple processor for the file: spans written immediately on End, in order.
	tp.RegisterSpanProcessor(sdktrace.NewSimpleSpanProcessor(fileExp))

	if httpEndpoint != "" {
		httpExp, err := otlptracehttp.New(context.Background(),
			otlptracehttp.WithEndpoint(httpEndpoint),
			otlptracehttp.WithInsecure(),
		)
		if err != nil {
			slog.Warn("trace: OTLP/HTTP exporter init failed, continuing without it",
				"endpoint", httpEndpoint, "error", err)
		} else {
			tp.RegisterSpanProcessor(sdktrace.NewBatchSpanProcessor(httpExp))
		}
	}

	if grpcEndpoint != "" {
		grpcExp, err := otlptracegrpc.New(context.Background(),
			otlptracegrpc.WithEndpoint(grpcEndpoint),
			otlptracegrpc.WithInsecure(),
		)
		if err != nil {
			slog.Warn("trace: OTLP/gRPC exporter init failed, continuing without it",
				"endpoint", grpcEndpoint, "error", err)
		} else {
			tp.RegisterSpanProcessor(sdktrace.NewBatchSpanProcessor(grpcExp))
		}
	}

	return &Tracer{
		tp:     tp,
		tracer: tp.Tracer("tars"),
		path:   path,
	}, nil
}

// Path returns the trace file path.
func (t *Tracer) Path() string {
	if t == nil {
		return ""
	}
	return t.path
}

// Close flushes and shuts down the tracer provider.
func (t *Tracer) Close() error {
	if t == nil || t.tp == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return t.tp.Shutdown(ctx)
}

// noopSpan returns a no-op span for nil tracers (safe to call End/SetAttributes on).
func noopSpan(ctx context.Context) oteltrace.Span {
	return oteltrace.SpanFromContext(ctx)
}

// StartTurn begins the root span for one SendMessage turn.
func (t *Tracer) StartTurn(ctx context.Context, convID, msgID, userText string) (context.Context, oteltrace.Span) {
	if t == nil {
		return ctx, noopSpan(ctx)
	}
	return t.tracer.Start(ctx, SpanAgentTurn,
		oteltrace.WithAttributes(
			attribute.String("openinference.span.kind", "AGENT"),
			attribute.String("session.id", convID),
			attribute.String("conversation.id", convID),
			attribute.String("message.id", msgID),
			attribute.String("user.text", userText),
			attribute.String("input.value", userText),
			attribute.String("input.mime_type", "text/plain"),
		),
	)
}

// EndTurn records elapsed time and ends the root span.
// output is the final assistant reply of the turn — Phoenix Sessions renders
// it as the turn's OUTPUT (paired with input.value). A non-nil err marks
// the span status as Error.
func (t *Tracer) EndTurn(span oteltrace.Span, err error, elapsedMs int64, output string) {
	if t == nil {
		return
	}
	attrs := []attribute.KeyValue{attribute.Int64("agent.elapsed_ms", elapsedMs)}
	if output != "" {
		attrs = append(attrs,
			attribute.String("output.value", output),
			attribute.String("output.mime_type", "text/plain"),
		)
	}
	span.SetAttributes(attrs...)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	} else {
		span.SetStatus(codes.Ok, "")
	}
	span.End()
}

// StartLLMCall begins a gen_ai.chat span for one LLM request.
// The returned ctx carries the span (pass it to the model call for propagation).
// messages are flattened into OpenInference llm.input_messages.N.message.*
// attributes (Phoenix renders them as a chat view); toolDefs are flattened
// into llm.tools.N.tool.json_schema.
func (t *Tracer) StartLLMCall(ctx context.Context, convID, model, system string, iteration int, messages []ChatMessage, toolDefs []string) (context.Context, oteltrace.Span) {
	if t == nil {
		return ctx, noopSpan(ctx)
	}
	attrs := []attribute.KeyValue{
		attribute.String("openinference.span.kind", "LLM"),
		attribute.String("session.id", convID),
		attribute.String("llm.model_name", model),
		attribute.String("gen_ai.system", "gemini"),
		attribute.String("gen_ai.request.model", model),
		attribute.String("gen_ai.system_instructions", system),
		attribute.Int("agent.iteration", iteration),
		attribute.Int("gen_ai.request.tool_count", len(toolDefs)),
	}
	for i, def := range toolDefs {
		attrs = append(attrs, attribute.String(
			fmt.Sprintf("llm.tools.%d.tool.json_schema", i), def))
	}
	attrs = append(attrs, flattenInputMessages(messages)...)
	return t.tracer.Start(ctx, fmt.Sprintf("%s %s", SpanLLMChat, model),
		oteltrace.WithAttributes(attrs...),
	)
}

// flattenInputMessages converts chat messages into OpenInference
// llm.input_messages.N.message.* flattened attributes.
// System message content is truncated hard (full text lives in
// gen_ai.system_instructions); all other roles are kept intact.
func flattenInputMessages(messages []ChatMessage) []attribute.KeyValue {
	var attrs []attribute.KeyValue
	for i, m := range messages {
		prefix := fmt.Sprintf("llm.input_messages.%d.message.", i)
		attrs = append(attrs, attribute.String(prefix+"role", m.Role))
		attrs = append(attrs, flattenMessageContent(prefix, m.Role, m.Content, m.Reasoning)...)
		if m.ToolCallID != "" {
			attrs = append(attrs, attribute.String(prefix+"tool_call_id", m.ToolCallID))
		}
		for j, tc := range m.ToolCalls {
			tcp := fmt.Sprintf("%stool_calls.%d.tool_call.", prefix, j)
			attrs = append(attrs,
				attribute.String(tcp+"id", tc.ID),
				attribute.String(tcp+"function.name", tc.Name),
				attribute.String(tcp+"function.arguments", tc.Args),
			)
		}
	}
	return attrs
}

// flattenMessageContent emits message content attributes for one message.
// With reasoning, content is emitted as a multimodal contents list
// (reasoning + text parts per the OpenInference spec); otherwise as the
// plain message.content shorthand.
func flattenMessageContent(prefix, role, content, reasoning string) []attribute.KeyValue {
	// System prompt: keep only a short marker, full text is in
	// gen_ai.system_instructions — avoids duplicating KBs per span.
	if role == "system" && len(content) > 80 {
		content = fmt.Sprintf("%s... [%d more chars]", content[:80], len(content)-80)
	}
	if reasoning == "" {
		if content == "" {
			return nil
		}
		return []attribute.KeyValue{attribute.String(prefix+"content", content)}
	}
	attrs := []attribute.KeyValue{
		attribute.String(prefix+"contents.0.message_content.type", "reasoning"),
		attribute.String(prefix+"contents.0.message_content.text", reasoning),
	}
	if content != "" {
		attrs = append(attrs,
			attribute.String(prefix+"contents.1.message_content.type", "text"),
			attribute.String(prefix+"contents.1.message_content.text", content),
		)
	}
	return attrs
}

// EndLLMCall records the response and ends the LLM span.
// The assistant reply is flattened into OpenInference
// llm.output_messages.0.message.* attributes (chat view in Phoenix).
// A non-nil err marks the span as failed (response fields are ignored).
func (t *Tracer) EndLLMCall(span oteltrace.Span, err error, content, reasoning string, toolCalls []ToolCallRef, finishReason string, usage *Usage) {
	if t == nil {
		return
	}
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		span.End()
		return
	}
	attrs := []attribute.KeyValue{
		attribute.String("gen_ai.response.content", content),
		attribute.Int("gen_ai.response.tool_call_count", len(toolCalls)),
		// OpenInference LLM attributes: all scalars, rendered as
		// dedicated output/token sections in Phoenix.
		attribute.String("output.value", content),
		attribute.String("output.mime_type", "text/plain"),
		attribute.String("llm.output_messages.0.message.role", "assistant"),
	}
	attrs = append(attrs, flattenMessageContent(
		"llm.output_messages.0.message.", "assistant", content, reasoning)...)
	for j, tc := range toolCalls {
		tcp := fmt.Sprintf("llm.output_messages.0.message.tool_calls.%d.tool_call.", j)
		attrs = append(attrs,
			attribute.String(tcp+"id", tc.ID),
			attribute.String(tcp+"function.name", tc.Name),
			attribute.String(tcp+"function.arguments", tc.Args),
		)
	}
	if reasoning != "" {
		attrs = append(attrs, attribute.String("gen_ai.response.reasoning", reasoning))
	}
	if finishReason != "" {
		attrs = append(attrs, attribute.StringSlice("gen_ai.response.finish_reasons", []string{finishReason}))
	}
	if usage != nil {
		attrs = append(attrs,
			attribute.Int("gen_ai.usage.input_tokens", usage.PromptTokens),
			attribute.Int("gen_ai.usage.output_tokens", usage.CompletionTokens),
			attribute.Int("gen_ai.usage.total_tokens", usage.TotalTokens),
			attribute.Int("llm.token_count.prompt", usage.PromptTokens),
			attribute.Int("llm.token_count.completion", usage.CompletionTokens),
			attribute.Int("llm.token_count.total", usage.TotalTokens),
		)
	}
	span.SetAttributes(attrs...)
	span.SetStatus(codes.Ok, "")
	span.End()
}

// StartToolCall begins a gen_ai.execute_tool span for one tool invocation.
//
// Attributes follow the OpenInference TOOL span conventions
// (https://github.com/Arize-ai/openinference): arguments are emitted as
// input.value with input.mime_type=application/json so Phoenix renders them
// as formatted JSON. Note: gen_ai.tool.call.arguments is intentionally NOT
// emitted — Phoenix parses it into an object under tool.parameters, which
// crashes its TOOL span view (React error #31).
func (t *Tracer) StartToolCall(ctx context.Context, convID, toolCallID, name, args string) (context.Context, oteltrace.Span) {
	if t == nil {
		return ctx, noopSpan(ctx)
	}
	return t.tracer.Start(ctx, fmt.Sprintf("%s %s", SpanToolExecute, name),
		oteltrace.WithAttributes(
			attribute.String("openinference.span.kind", "TOOL"),
			attribute.String("session.id", convID),
			attribute.String("gen_ai.tool.call.id", toolCallID),
			attribute.String("gen_ai.tool.name", name),
			attribute.String("tool.name", name),
			attribute.String("tool.id", toolCallID),
			attribute.String("input.value", args),
			attribute.String("input.mime_type", "application/json"),
		),
	)
}

// EndToolCall records the tool output and ends the span.
func (t *Tracer) EndToolCall(span oteltrace.Span, output string) {
	if t == nil {
		return
	}
	span.SetAttributes(
		attribute.String("output.value", output),
		attribute.String("output.mime_type", "text/plain"),
	)
	span.SetStatus(codes.Ok, "")
	span.End()
}

// LogConversationCreated records a standalone instant span.
func (t *Tracer) LogConversationCreated(convID, title string) {
	if t == nil {
		return
	}
	_, span := t.tracer.Start(context.Background(), SpanConvCreated,
		oteltrace.WithAttributes(
			attribute.String("conversation.id", convID),
			attribute.String("conversation.title", title),
		),
	)
	span.End()
}
