package boot

import (
	"context"
	"errors"

	"tars/pkg/event"
	"tars/pkg/trace"

	oteltrace "go.opentelemetry.io/otel/trace"
)

// traceSink 把内核事件流转换为 OpenTelemetry span 树
// （agent.turn → gen_ai.chat + tool.call），是 event.Sink 的订阅者实现，
// 由装配层经 FanOut 与 UI sink 并列挂载。它是纯事件驱动的零构造参数
// 订阅者：轮级状态（turn span/LLM span/工具 span）随事件建立与闭合，
// 轮级元信息（模型/系统提示词/工具描述）经 KindTurnStarted 载荷传入。
// 每个会话一个实例（同会话不并发轮），无需锁。
type traceSink struct {
	ctx      context.Context // turn span 所在的 ctx（LLM/工具 span 的父）
	turnSpan oteltrace.Span
	modelID  string
	system   string
	schemas  []string

	llmSpan   oteltrace.Span
	toolSpans map[string]oteltrace.Span
	// compressionSpan 进行中的压缩 span（压缩在轮内同步执行，随
	// KindCompressionDone/Failed 闭合；轮终止时防御性收尾）。
	compressionSpan oteltrace.Span
}

// NewTraceSink 创建 trace 订阅者（无参数：一切状态由事件流驱动）。
func NewTraceSink() event.Sink {
	return &traceSink{}
}

// Emit 实现 event.Sink。工具结果事件可能并发到达（工具并行执行），
// 此处对 toolSpans 只读不写（写入只发生在循环串行段的 ToolDispatch），
// 并发安全。
func (s *traceSink) Emit(e event.Event) {
	switch e.Kind {
	case event.KindTurnStarted:
		// 开启 turn 根 span。父 ctx 用 Background：span 父子关系靠
		// 该 ctx 在订阅者内部传递，不需要取消语义（取消由 agent 的
		// 运行 ctx 负责，span 只是观测）。
		s.modelID = e.Turn.ModelID
		s.system = e.Turn.System
		s.schemas = e.Turn.ToolSchemas
		s.ctx, s.turnSpan = trace.StartTurn(context.Background(),
			e.Turn.SessionID, e.Turn.MessageID, e.Turn.UserText)

	case event.KindIterationStart:
		if s.ctx == nil {
			return
		}
		_, span := trace.StartLLMCall(s.ctx, e.Iteration.SessionID, s.modelID, s.system,
			e.Iteration.Iteration-1, e.Iteration.Messages, s.schemas)
		s.llmSpan = span

	case event.KindIterationEnd:
		if s.llmSpan == nil {
			return
		}
		m := e.Iteration.Assistant
		promptTokens, completionTokens, totalTokens := 0, 0, 0
		if m.Usage != nil {
			promptTokens = m.Usage.PromptTokens
			completionTokens = m.Usage.CompletionTokens
			totalTokens = m.Usage.TotalTokens
		}
		trace.EndLLMCall(s.llmSpan, nil, m.Content, m.Reasoning, m.ToolCalls, "stop",
			promptTokens, completionTokens, totalTokens)
		s.llmSpan = nil

	case event.KindToolDispatch:
		if s.ctx == nil {
			return
		}
		if s.toolSpans == nil {
			s.toolSpans = make(map[string]oteltrace.Span)
		}
		_, span := trace.StartToolCall(s.ctx, e.Tool.SessionID, e.Tool.ToolCallID, e.Tool.ToolName, e.Tool.Args)
		s.toolSpans[e.Tool.ToolCallID] = span

	case event.KindToolResult:
		if span, ok := s.toolSpans[e.ToolResult.ToolCallID]; ok {
			trace.EndToolCall(span, e.ToolResult.Output)
		}

	case event.KindCompressionStarted:
		if s.ctx == nil {
			return
		}
		_, s.compressionSpan = trace.StartCompression(s.ctx, e.CompressionStarted.SessionID,
			e.CompressionStarted.TriggerTokens, e.CompressionStarted.Budget)

	case event.KindCompressionDone:
		if s.compressionSpan == nil {
			return
		}
		d := e.CompressionDone
		trace.EndCompression(s.compressionSpan, nil, d.BeforeTokens, d.AfterTokens,
			d.NewEntries, d.TotalEntries, d.DurationMs)
		s.compressionSpan = nil

	case event.KindCompressionFailed:
		if s.compressionSpan == nil {
			return
		}
		trace.EndCompression(s.compressionSpan, errors.New(e.CompressionFailed.Error), 0, 0, 0, 0, 0)
		s.compressionSpan = nil

	case event.KindTurnEnded:
		// 轮正常结束（含用户取消的干净停止）：收尾未闭合的 LLM span
		// （取消发生在 LLM 调用中途时不会触发 IterationEnd），闭合 turn span。
		if s.compressionSpan != nil {
			trace.EndCompression(s.compressionSpan, context.Canceled, 0, 0, 0, 0, 0)
			s.compressionSpan = nil
		}
		if s.llmSpan != nil {
			trace.EndLLMCall(s.llmSpan, context.Canceled, "", "", nil, "", 0, 0, 0)
			s.llmSpan = nil
		}
		if s.turnSpan != nil {
			trace.EndTurn(s.turnSpan, nil, e.Done.ElapsedMs, e.Done.FinalOutput)
			s.turnSpan = nil
			s.ctx = nil
		}

	case event.KindError:
		// LLM 调用失败：关闭未收尾的 LLM span（失败轮不会触发 IterationEnd）。
		if s.llmSpan != nil {
			trace.EndLLMCall(s.llmSpan, errors.New(e.Error.Error), "", "", nil, "", 0, 0, 0)
			s.llmSpan = nil
		}
		if s.turnSpan != nil {
			trace.EndTurn(s.turnSpan, errors.New(e.Error.Error), e.Error.ElapsedMs, "")
			s.turnSpan = nil
			s.ctx = nil
		}
	}
}
