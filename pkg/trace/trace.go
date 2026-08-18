// Package trace provides OpenTelemetry-based tracing for agent sessions.
//
// Each session gets its own Tracer backed by a dedicated TracerProvider
// exporting completed spans to the configured OTLP collectors: OTLP/HTTP
// (Jaeger, Tempo, ...) and/or OTLP/gRPC (Arize Phoenix, ...). Both can be
// enabled at the same time. There is no local file sink.
//
// Span tree for one SubmitMessage turn (ReAct loop):
//
//	agent.turn                     (root, one per user message)
//	├── gen_ai.chat <model>        (LLM request, iteration 0)
//	│   ├── gen_ai.execute_tool read_file
//	│   └── gen_ai.execute_tool edit_file
//	├── gen_ai.chat <model>        (LLM request, iteration 1)
//	└── gen_ai.chat <model>        (final answer, no tool calls)
package trace

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"tars/pkg/schema"

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
	SpanAgentTurn      = "agent.turn"
	SpanLLMChat        = "gen_ai.chat"
	SpanToolExecute    = "gen_ai.execute_tool"
	SpanSessionCreated = "session.created"
)

// --- 全局单例 ---
//
// tp 是进程级 OTLP 导出器（含连接池与批量处理器），所有会话共享。
// tracer 是 tp 的轻量包装，OTel SDK 保证并发安全。
// 两者在 Init 时创建一次，之后由 Rebuild 热替换。

var (
	tp     *sdktrace.TracerProvider
	tracer oteltrace.Tracer
)

// GetTracer 返回全局 tracer（可能为 nil，调用方判空）。
// OTel SDK 保证 Tracer 并发安全，所有会话共享。
func GetTracer() oteltrace.Tracer { return tracer }

// Init 按给定配置创建全局 TracerProvider 单例，并报告当前配置状态。
// 进程启动时调用一次；未启用追踪时 tp 为 nil，所有包级函数安全 no-op。
// （配置保存的热更新走 Rebuild，启动日志不会重复打印。）
func InitTrace(cfg *Config) {
	if cfg == nil || !cfg.Enabled {
		slog.Info("Tracing disabled (trace.enabled is not set)")
		return
	}
	if cfg.OTLPHTTPEndpoint != "" {
		slog.Info("OTLP/HTTP trace export enabled", "endpoint", cfg.OTLPHTTPEndpoint)
	}
	if cfg.OTLPGrpcEndpoint != "" {
		slog.Info("OTLP/gRPC trace export enabled", "endpoint", cfg.OTLPGrpcEndpoint)
	}
	if cfg.OTLPHTTPEndpoint == "" && cfg.OTLPGrpcEndpoint == "" {
		return
	}
	tp, tracer = buildTracer(true, cfg.OTLPHTTPEndpoint, cfg.OTLPGrpcEndpoint)
}

// Rebuild 按给定配置重建全局 TracerProvider（设置界面保存 trace 配置后调用）。
// 旧的 provider 被 Shutdown（冲刷 OTLP 批量队列），新的立即生效。
// 重建失败时保留旧 provider，避免配置错误导致追踪中断。
func Rebuild(cfg *Config) {
	enabled := cfg != nil && cfg.Enabled
	httpEp, grpcEp := "", ""
	if enabled {
		httpEp, grpcEp = cfg.OTLPHTTPEndpoint, cfg.OTLPGrpcEndpoint
	}

	// 尝试创建新的
	newTp, newTracer := buildTracer(enabled, httpEp, grpcEp)
	if newTp == nil && tp != nil {
		// 配置关闭或构建失败：关闭旧的，置 nil
		tp.Shutdown(context.Background())
		tp, tracer = nil, nil
		return
	}
	if newTp == nil {
		return // 本来就没启用，无需动作
	}

	// 替换：先关旧的（冲刷），再切换
	if tp != nil {
		tp.Shutdown(context.Background())
	}
	tp, tracer = newTp, newTracer
}

// Shutdown 关闭并清空全局 TracerProvider（进程退出时调用）。
func Shutdown() {
	if tp != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		tp.Shutdown(ctx)
		tp, tracer = nil, nil
	}
}

// buildTracer 按给定参数创建 TracerProvider + Tracer。
// 任何一步失败返回 (nil, nil)；enabled=false 或无端点也返回 (nil, nil)。
func buildTracer(enabled bool, httpEndpoint, grpcEndpoint string) (*sdktrace.TracerProvider, oteltrace.Tracer) {
	if !enabled || (httpEndpoint == "" && grpcEndpoint == "") {
		return nil, nil
	}

	res, err := resource.New(context.Background(),
		resource.WithAttributes(attribute.String("service.name", "tars")),
	)
	if err != nil {
		res = resource.Default()
	}

	newTp := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		// Keep full message/tool content in attributes (debug traces).
		sdktrace.WithRawSpanLimits(sdktrace.SpanLimits{
			AttributeValueLengthLimit: -1,
			AttributeCountLimit:       -1,
		}),
	)

	if httpEndpoint != "" {
		httpExp, err := otlptracehttp.New(context.Background(),
			otlptracehttp.WithEndpoint(httpEndpoint),
			otlptracehttp.WithInsecure(),
		)
		if err != nil {
			slog.Warn("trace: OTLP/HTTP exporter init failed, continuing without it",
				"endpoint", httpEndpoint, "error", err)
		} else {
			newTp.RegisterSpanProcessor(sdktrace.NewBatchSpanProcessor(httpExp))
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
			newTp.RegisterSpanProcessor(sdktrace.NewBatchSpanProcessor(grpcExp))
		}
	}

	return newTp, newTp.Tracer("tars")
}

// noopSpan returns a no-op span for nil tracer (safe to call End/SetAttributes on).
func noopSpan(ctx context.Context) oteltrace.Span {
	return oteltrace.SpanFromContext(ctx)
}

// StartTurn begins the root span for one SubmitMessage turn.
// sessionID/msgID/userText 由调用方传入（无状态）。
func StartTurn(ctx context.Context, sessionID, msgID, userText string) (context.Context, oteltrace.Span) {
	if tracer == nil {
		return ctx, noopSpan(ctx)
	}
	return tracer.Start(ctx, SpanAgentTurn,
		oteltrace.WithAttributes(
			attribute.String("openinference.span.kind", "AGENT"),
			attribute.String("session.id", sessionID),
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
func EndTurn(span oteltrace.Span, err error, elapsedMs int64, output string) {
	if tracer == nil {
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
func StartLLMCall(ctx context.Context, sessionID, model, system string, iteration int, messages []*schema.Message, toolDefs []string) (context.Context, oteltrace.Span) {
	if tracer == nil {
		return ctx, noopSpan(ctx)
	}
	attrs := []attribute.KeyValue{
		attribute.String("openinference.span.kind", "LLM"),
		attribute.String("session.id", sessionID),
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
	return tracer.Start(ctx, fmt.Sprintf("%s %s", SpanLLMChat, model),
		oteltrace.WithAttributes(attrs...),
	)
}

// flattenInputMessages converts chat messages into OpenInference
// llm.input_messages.N.message.* flattened attributes.
// System message content is truncated hard (full text lives in
// gen_ai.system_instructions); all other roles are kept intact.
func flattenInputMessages(messages []*schema.Message) []attribute.KeyValue {
	var attrs []attribute.KeyValue
	for i, m := range messages {
		prefix := fmt.Sprintf("llm.input_messages.%d.message.", i)
		attrs = append(attrs, attribute.String(prefix+"role", string(m.Role)))
		attrs = append(attrs, flattenMessageContent(prefix, m.Content, m.Reasoning)...)
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
func flattenMessageContent(prefix, content, reasoning string) []attribute.KeyValue {
	// system 消息不截断——调试时需要在 input_messages 里看到完整 system
	// prompt（gen_ai.system_instructions 也保留全文，两处都有方便 Phoenix
	// 不同视图渲染）。
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
func EndLLMCall(span oteltrace.Span, err error, content, reasoning string, toolCalls []schema.ToolCall, finishReason string, promptTokens, completionTokens, totalTokens int) {
	if tracer == nil {
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
		"llm.output_messages.0.message.", content, reasoning)...)
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
	if totalTokens > 0 {
		attrs = append(attrs,
			attribute.Int("gen_ai.usage.input_tokens", promptTokens),
			attribute.Int("gen_ai.usage.output_tokens", completionTokens),
			attribute.Int("gen_ai.usage.total_tokens", totalTokens),
			attribute.Int("llm.token_count.prompt", promptTokens),
			attribute.Int("llm.token_count.completion", completionTokens),
			attribute.Int("llm.token_count.total", totalTokens),
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
func StartToolCall(ctx context.Context, sessionID, toolCallID, name, args string) (context.Context, oteltrace.Span) {
	if tracer == nil {
		return ctx, noopSpan(ctx)
	}
	return tracer.Start(ctx, fmt.Sprintf("%s %s", SpanToolExecute, name),
		oteltrace.WithAttributes(
			attribute.String("openinference.span.kind", "TOOL"),
			attribute.String("session.id", sessionID),
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
func EndToolCall(span oteltrace.Span, output string) {
	if tracer == nil {
		return
	}
	span.SetAttributes(
		attribute.String("output.value", output),
		attribute.String("output.mime_type", "text/plain"),
	)
	span.SetStatus(codes.Ok, "")
	span.End()
}

// LogSessionCreated records a standalone instant span.
func LogSessionCreated(sessionID, title string) {
	if tracer == nil {
		return
	}
	_, span := tracer.Start(context.Background(), SpanSessionCreated,
		oteltrace.WithAttributes(
			attribute.String("session.id", sessionID),
			attribute.String("session.title", title),
		),
	)
	span.End()
}
