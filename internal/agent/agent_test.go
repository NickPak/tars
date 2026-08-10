package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"tars/pkg/tools"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// stubModel streams canned responses, one per call (cycled). If errs[i] is
// non-nil, the i-th Stream call fails instead of streaming.
type stubModel struct {
	responses []*schema.Message
	errs      []error
	calls     int
}

func (m *stubModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	m.calls++
	idx := (m.calls - 1) % len(m.responses)
	return m.responses[idx], nil
}

func (m *stubModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	m.calls++
	idx := (m.calls - 1) % len(m.responses)
	if m.errs != nil && idx < len(m.errs) && m.errs[idx] != nil {
		return nil, m.errs[idx]
	}
	msg := m.responses[idx]
	sr, sw := schema.Pipe[*schema.Message](1)
	go func() {
		defer sw.Close()
		sw.Send(msg, nil)
	}()
	return sr, nil
}

func (m *stubModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}

// stuckModel never yields any chunk; it only closes the stream (with the ctx
// error) when the context is done — simulating a hung connection that can
// only be broken by a deadline or cancellation.
type stuckModel struct{}

func (m *stuckModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (m *stuckModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	sr, sw := schema.Pipe[*schema.Message](1)
	go func() {
		<-ctx.Done()
		sw.Send(nil, ctx.Err())
		sw.Close()
	}()
	return sr, nil
}

func (m *stuckModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}

// newTestManager builds a real tools.Manager with the given handlers registered
// (Manager.Execute already provides parallel execution, panic recovery, and
// unknown-tool handling — so tests exercise the production path).
func newTestManager(handlers map[string]tools.Handler) *tools.Manager {
	mgr := tools.NewManager()
	for name, h := range handlers {
		mgr.Register(&tools.Definition{
			Name:       name,
			Parameters: map[string]any{"type": "object"},
			Handler:   h,
		})
	}
	return mgr
}

// recordingHooks collects hook invocations for assertions.
type recordingHooks struct {
	iterStarts   []int
	iterEnds     []int
	iterDeltas   [][]*schema.Message
	chunks       int
	toolStarts   []string
	toolResults  []string
	toolsEndRuns int

	// onError, when set, decides retry behavior; onErrorCalls records every
	// OnError invocation for assertions.
	onError      func(iteration, attempt, streamedChunks int, err error) (bool, time.Duration)
	onErrorCalls []error
}

func (h *recordingHooks) IterationStart(_ context.Context, i int, _ []*schema.Message) {
	h.iterStarts = append(h.iterStarts, i)
}

func (h *recordingHooks) IterationEnd(_ context.Context, i int, _ []*schema.Message, delta []*schema.Message) {
	h.iterEnds = append(h.iterEnds, i)
	h.iterDeltas = append(h.iterDeltas, delta)
}

func (h *recordingHooks) StreamChunk(_ context.Context, _ int, _ *schema.Message) {
	h.chunks++
}

func (h *recordingHooks) ToolsStart(_ context.Context, calls []schema.ToolCall) {
	for _, c := range calls {
		h.toolStarts = append(h.toolStarts, c.Function.Name)
	}
}

func (h *recordingHooks) ToolResult(_ context.Context, r tools.ToolResult) {
	h.toolResults = append(h.toolResults, r.Output)
}

func (h *recordingHooks) ToolsEnd(_ context.Context, _ []tools.ToolResult) {
	h.toolsEndRuns++
}

func (h *recordingHooks) OnError(_ context.Context, iter, attempt, chunks int, err error) (bool, time.Duration) {
	h.onErrorCalls = append(h.onErrorCalls, err)
	if h.onError != nil {
		return h.onError(iter, attempt, chunks, err)
	}
	return false, 0
}

func TestRun_PlainTextAnswer(t *testing.T) {
	m := &stubModel{responses: []*schema.Message{
		{Role: schema.Assistant, Content: "hello"},
	}}
	a := New(m, newTestManager(nil), 5)

	h := &recordingHooks{}
	res, err := a.Run(context.Background(), []*schema.Message{{Role: schema.User, Content: "hi"}}, h)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.FinalMessage.Content != "hello" {
		t.Errorf("final = %q, want %q", res.FinalMessage.Content, "hello")
	}
	if len(h.iterStarts) != 1 {
		t.Errorf("iterStarts = %v, want 1 round", h.iterStarts)
	}
	// IterationEnd must fire for the final plain-text round too, carrying the
	// assistant message as delta — hosts persist it there.
	if len(h.iterEnds) != 1 {
		t.Fatalf("iterEnds = %v, want 1", h.iterEnds)
	}
	if len(h.iterDeltas[0]) != 1 || h.iterDeltas[0][0].Content != "hello" {
		t.Errorf("delta = %v, want [assistant(hello)]", h.iterDeltas[0])
	}
	if h.toolsEndRuns != 0 {
		t.Errorf("toolsEndRuns = %d, want 0 (no tool calls)", h.toolsEndRuns)
	}
}

func TestRun_OneToolRound(t *testing.T) {
	m := &stubModel{responses: []*schema.Message{
		{Role: schema.Assistant, ToolCalls: []schema.ToolCall{
			{ID: "call_1", Function: schema.FunctionCall{Name: "echo", Arguments: `{"text":"abc"}`}},
		}},
		{Role: schema.Assistant, Content: "done"},
	}}
	mgr := newTestManager(map[string]tools.Handler{
		"echo": func(ctx context.Context, args json.RawMessage) (string, error) { return "abc", nil },
	})
	a := New(m, mgr, 5)

	h := &recordingHooks{}
	res, err := a.Run(context.Background(), []*schema.Message{{Role: schema.User, Content: "go"}}, h)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.FinalMessage.Content != "done" {
		t.Errorf("final = %q, want done", res.FinalMessage.Content)
	}
	if len(h.iterEnds) != 2 {
		t.Fatalf("iterEnds = %v, want 2 rounds", h.iterEnds)
	}
	// Round 1 delta: assistant + tool result; round 2 delta: assistant only.
	if len(h.iterDeltas[0]) != 2 {
		t.Errorf("round1 delta len = %d, want 2 (assistant + tool)", len(h.iterDeltas[0]))
	}
	if len(h.iterDeltas[1]) != 1 {
		t.Errorf("round2 delta len = %d, want 1 (assistant)", len(h.iterDeltas[1]))
	}
	if len(h.toolStarts) != 1 || h.toolStarts[0] != "echo" {
		t.Errorf("toolStarts = %v", h.toolStarts)
	}
	if len(h.toolResults) != 1 || h.toolResults[0] != "abc" {
		t.Errorf("toolResults = %v", h.toolResults)
	}
	if h.toolsEndRuns != 1 {
		t.Errorf("toolsEndRuns = %d, want 1", h.toolsEndRuns)
	}
}

func TestRun_UnknownTool(t *testing.T) {
	m := &stubModel{responses: []*schema.Message{
		{Role: schema.Assistant, ToolCalls: []schema.ToolCall{
			{ID: "call_1", Function: schema.FunctionCall{Name: "missing", Arguments: "{}"}},
		}},
		{Role: schema.Assistant, Content: "recovered"},
	}}
	a := New(m, newTestManager(nil), 5)

	h := &recordingHooks{}
	_, err := a.Run(context.Background(), []*schema.Message{{Role: schema.User, Content: "go"}}, h)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(h.toolResults) != 1 || !strings.Contains(h.toolResults[0], "unknown tool") {
		t.Errorf("expected unknown-tool error result, got %v", h.toolResults)
	}
}

func TestRun_ToolPanicRecovered(t *testing.T) {
	m := &stubModel{responses: []*schema.Message{
		{Role: schema.Assistant, ToolCalls: []schema.ToolCall{
			{ID: "call_1", Function: schema.FunctionCall{Name: "boom", Arguments: "{}"}},
		}},
		{Role: schema.Assistant, Content: "ok"},
	}}
	mgr := newTestManager(map[string]tools.Handler{
		"boom": func(ctx context.Context, args json.RawMessage) (string, error) { panic("exploded") },
	})
	a := New(m, mgr, 5)

	h := &recordingHooks{}
	_, err := a.Run(context.Background(), []*schema.Message{{Role: schema.User, Content: "go"}}, h)
	if err != nil {
		t.Fatalf("Run should not return error on tool panic: %v", err)
	}
	if len(h.toolResults) == 0 || !strings.Contains(h.toolResults[0], "panicked") {
		t.Fatalf("expected panic error result, got %v", h.toolResults)
	}
}

func TestRun_MaxIterationsExceeded(t *testing.T) {
	tcMsg := &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{
		{ID: "call_1", Function: schema.FunctionCall{Name: "echo", Arguments: "{}"}},
	}}
	m := &stubModel{responses: []*schema.Message{tcMsg}}
	mgr := newTestManager(map[string]tools.Handler{
		"echo": func(ctx context.Context, args json.RawMessage) (string, error) { return "x", nil },
	})
	a := New(m, mgr, 2)

	_, err := a.Run(context.Background(), []*schema.Message{{Role: schema.User, Content: "go"}}, &recordingHooks{})
	if err == nil {
		t.Fatal("expected max-iterations error")
	}
}

func TestRun_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	m := &stubModel{responses: []*schema.Message{{Role: schema.Assistant, Content: "x"}}}
	a := New(m, newTestManager(nil), 5)

	_, err := a.Run(ctx, []*schema.Message{{Role: schema.User, Content: "go"}}, &recordingHooks{})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestRun_NilHooks(t *testing.T) {
	m := &stubModel{responses: []*schema.Message{{Role: schema.Assistant, Content: "hi"}}}
	a := New(m, newTestManager(nil), 1)

	_, err := a.Run(context.Background(), []*schema.Message{{Role: schema.User, Content: "go"}}, nil)
	if err != nil {
		t.Fatalf("Run with nil hooks: %v", err)
	}
}

func TestRun_OnErrorRetryThenSuccess(t *testing.T) {
	// First call fails, second succeeds.
	m := &stubModel{
		responses: []*schema.Message{
			{Role: schema.Assistant, Content: "recovered"},
			{Role: schema.Assistant, Content: "recovered"},
		},
		errs: []error{errors.New("provider 503"), nil},
	}
	a := New(m, newTestManager(nil), 5)

	h := &recordingHooks{
		onError: func(iter, attempt, chunks int, err error) (bool, time.Duration) {
			if attempt > 1 {
				return false, 0
			}
			return true, 0 // retry immediately
		},
	}
	res, err := a.Run(context.Background(), []*schema.Message{{Role: schema.User, Content: "go"}}, h)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.FinalMessage.Content != "recovered" {
		t.Errorf("final = %q", res.FinalMessage.Content)
	}
	if len(h.onErrorCalls) != 1 {
		t.Errorf("onErrorCalls = %d, want 1", len(h.onErrorCalls))
	}
}

func TestRun_OnErrorDeclineAborts(t *testing.T) {
	m := &stubModel{
		responses: []*schema.Message{{Role: schema.Assistant, Content: "x"}},
		errs:      []error{errors.New("provider down")},
	}
	a := New(m, newTestManager(nil), 5)

	h := &recordingHooks{} // default: no retry
	_, err := a.Run(context.Background(), []*schema.Message{{Role: schema.User, Content: "go"}}, h)
	if err == nil {
		t.Fatal("expected error when host declines retry")
	}
	if len(h.onErrorCalls) != 1 {
		t.Errorf("onErrorCalls = %d, want 1", len(h.onErrorCalls))
	}
}

func TestRun_CancelDoesNotTriggerOnError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	m := &stubModel{
		responses: []*schema.Message{{Role: schema.Assistant, Content: "x"}},
		errs:      []error{context.Canceled}, // model call fails because ctx died
	}
	a := New(m, newTestManager(nil), 5)
	cancel() // parent ctx already done

	h := &recordingHooks{}
	_, err := a.Run(ctx, []*schema.Message{{Role: schema.User, Content: "go"}}, h)
	if err == nil {
		t.Fatal("expected error on cancelled ctx")
	}
	if len(h.onErrorCalls) != 0 {
		t.Errorf("OnError must not fire on user cancellation, got %d calls", len(h.onErrorCalls))
	}
}

func TestRun_IterationTimeoutTriggersOnError(t *testing.T) {
	a := New(&stuckModel{}, newTestManager(nil), 5)
	a.IterationTimeout = 50 * time.Millisecond

	var gotChunks int = -1
	h := &recordingHooks{
		onError: func(iter, attempt, chunks int, err error) (bool, time.Duration) {
			gotChunks = chunks
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("err = %v, want DeadlineExceeded", err)
			}
			return false, 0
		},
	}

	start := time.Now()
	_, err := a.Run(context.Background(), []*schema.Message{{Role: schema.User, Content: "go"}}, h)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("iteration timeout did not fire promptly")
	}
	if len(h.onErrorCalls) != 1 {
		t.Fatalf("onErrorCalls = %d, want 1", len(h.onErrorCalls))
	}
	if gotChunks != 0 {
		t.Errorf("streamedChunks = %d, want 0 (stream hung before any chunk)", gotChunks)
	}
}
