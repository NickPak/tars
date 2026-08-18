package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"tars/internal/event"
	"tars/pkg/llm"
	"tars/pkg/schema"
	"tars/pkg/tools"
)

// stubProvider streams canned responses, one per call (cycled). If errs[i] is
// non-nil, the i-th Stream call fails instead of streaming.
type stubProvider struct {
	responses []*schema.Message
	errs      []error
	calls     int
}

func (m *stubProvider) Stream(ctx context.Context, req *llm.ChatRequest) (llm.Stream, error) {
	m.calls++
	idx := (m.calls - 1) % len(m.responses)
	if m.errs != nil && idx < len(m.errs) && m.errs[idx] != nil {
		return nil, m.errs[idx]
	}
	return &stubStream{msg: m.responses[idx]}, nil
}

type stubStream struct {
	msg  *schema.Message
	sent bool
}

func (s *stubStream) Recv() (*schema.Message, error) {
	if s.sent {
		return nil, io.EOF
	}
	s.sent = true
	return s.msg, nil
}

func (s *stubStream) Final() (*schema.Message, error) { return s.msg, nil }
func (s *stubStream) Close() error                    { return nil }

// stuckProvider never yields any chunk; Recv blocks until the context is done —
// simulating a hung connection that can only be broken by a deadline or cancellation.
type stuckProvider struct{}

func (m *stuckProvider) Stream(ctx context.Context, req *llm.ChatRequest) (llm.Stream, error) {
	return &stuckStream{ctx: ctx}, nil
}

type stuckStream struct{ ctx context.Context }

func (s *stuckStream) Recv() (*schema.Message, error) {
	<-s.ctx.Done()
	return nil, s.ctx.Err()
}
func (s *stuckStream) Final() (*schema.Message, error) { return nil, nil }
func (s *stuckStream) Close() error                    { return nil }

// fakeSession 是 agent.Session 的内存实现：聚合语义对齐 session.Info
// （assistant 按 ID 聚合，工具结果尾部追加）。
type fakeSession struct {
	msgs []*schema.Message
}

func (s *fakeSession) History() []*schema.Message {
	out := make([]*schema.Message, len(s.msgs))
	copy(out, s.msgs)
	return out
}

func (s *fakeSession) UpsertAssistant(id string, delta *schema.Message) {
	for _, m := range s.msgs {
		if m.ID == id {
			m.Content += delta.Content
			m.ToolCalls = append(m.ToolCalls, delta.ToolCalls...)
			return
		}
	}
	s.msgs = append(s.msgs, &schema.Message{
		ID: id, Role: schema.RoleAssistant, Content: delta.Content, ToolCalls: delta.ToolCalls,
	})
}

func (s *fakeSession) AppendMessage(_ int64, msg ...*schema.Message) {
	s.msgs = append(s.msgs, msg...)
}

func (s *fakeSession) byRole(r schema.Role) []*schema.Message {
	var out []*schema.Message
	for _, m := range s.msgs {
		if m.Role == r {
			out = append(out, m)
		}
	}
	return out
}

// newTestRegistry builds a real tools.Registry (builtins + the given custom
// handlers registered into the session view). Registry.Execute already provides
// parallel execution, panic recovery, and unknown-tool handling — so tests
// exercise the production path.
func newTestRegistry(handlers map[string]tools.Handler) *tools.Registry {
	r := tools.NewRegistry(nil, nil)
	for name, h := range handlers {
		r.Register(&tools.Definition{
			Name:       name,
			Parameters: map[string]any{"type": "object"},
			Handler:   h,
		})
	}
	return r
}

// recordingSink collects emitted events for assertions.
type recordingSink struct {
	events []event.Event
}

func (s *recordingSink) Emit(e event.Event) { s.events = append(s.events, e) }

func (s *recordingSink) count(k event.Kind) int {
	n := 0
	for _, e := range s.events {
		if e.Kind == k {
			n++
		}
	}
	return n
}

func (s *recordingSink) toolResults() []string {
	var out []string
	for _, e := range s.events {
		if e.Kind == event.KindToolResult {
			out = append(out, e.ToolResult.Output)
		}
	}
	return out
}

func newTestAgent(reg *tools.Registry, sess *fakeSession, sink event.Sink, maxIter int) *ReActAgent {
	return NewReAct(Options{
		System:    func() []*schema.Message { return nil },
		Registry:  reg,
		Session:   sess,
		Sink:      sink,
		SessionID: "test-session",
		Limits:    func() Limits { return Limits{MaxIterations: maxIter} },
	})
}

// runTurn 以固定 assistantID 跑一轮（provider 作为轮级输入）。
func runTurn(a *ReActAgent, ctx context.Context, userMsg string, p llm.Provider) (*Result, error) {
	return a.Run(ctx, userMsg, "test-assistant", p)
}

func TestRun_PlainTextAnswer(t *testing.T) {
	m := &stubProvider{responses: []*schema.Message{
		{Role: schema.RoleAssistant, Content: "hello"},
	}}
	sess := &fakeSession{}
	sink := &recordingSink{}
	a := newTestAgent(newTestRegistry(nil), sess, sink, 5)

	res, err := runTurn(a, context.Background(), "hi", m)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Content != "hello" {
		t.Errorf("final = %q, want %q", res.Content, "hello")
	}
	if sink.count(event.KindIterationStart) != 1 || sink.count(event.KindIterationEnd) != 1 {
		t.Errorf("iteration events = %d/%d, want 1/1",
			sink.count(event.KindIterationStart), sink.count(event.KindIterationEnd))
	}
	// assistant 消息落会话
	assistants := sess.byRole(schema.RoleAssistant)
	if len(assistants) != 1 || assistants[0].Content != "hello" {
		t.Errorf("session assistants = %v, want [assistant(hello)]", assistants)
	}
	if sink.count(event.KindToolDispatch) != 0 {
		t.Errorf("tool dispatch = %d, want 0 (no tool calls)", sink.count(event.KindToolDispatch))
	}
}

func TestRun_OneToolRound(t *testing.T) {
	m := &stubProvider{responses: []*schema.Message{
		{Role: schema.RoleAssistant, ToolCalls: []schema.ToolCall{
			{ID: "call_1", Name: "echo", Args: `{"text":"abc"}`},
		}},
		{Role: schema.RoleAssistant, Content: "done"},
	}}
	reg := newTestRegistry(map[string]tools.Handler{
		"echo": func(ctx context.Context, args json.RawMessage) (string, error) { return "abc", nil },
	})
	sess := &fakeSession{}
	sink := &recordingSink{}
	a := newTestAgent(reg, sess, sink, 5)

	res, err := runTurn(a, context.Background(), "go", m)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Content != "done" {
		t.Errorf("final = %q, want done", res.Content)
	}
	if sink.count(event.KindIterationEnd) != 2 {
		t.Fatalf("iteration ends = %d, want 2 rounds", sink.count(event.KindIterationEnd))
	}
	if sink.count(event.KindToolDispatch) != 1 || sink.count(event.KindToolResult) != 1 {
		t.Errorf("tool events = %d/%d, want 1/1",
			sink.count(event.KindToolDispatch), sink.count(event.KindToolResult))
	}
	results := sink.toolResults()
	if len(results) != 1 || results[0] != "abc" {
		t.Errorf("toolResults = %v", results)
	}
	// 工具结果消息落会话
	toolMsgs := sess.byRole(schema.RoleTool)
	if len(toolMsgs) != 1 || toolMsgs[0].Content != "abc" || toolMsgs[0].ToolCallID != "call_1" {
		t.Errorf("session tool messages = %v", toolMsgs)
	}
}

func TestRun_UnknownTool(t *testing.T) {
	m := &stubProvider{responses: []*schema.Message{
		{Role: schema.RoleAssistant, ToolCalls: []schema.ToolCall{
			{ID: "call_1", Name: "missing", Args: "{}"},
		}},
		{Role: schema.RoleAssistant, Content: "recovered"},
	}}
	sess := &fakeSession{}
	sink := &recordingSink{}
	a := newTestAgent(newTestRegistry(nil), sess, sink, 5)

	_, err := runTurn(a, context.Background(), "go", m)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	results := sink.toolResults()
	if len(results) != 1 || !strings.Contains(results[0], "unknown tool") {
		t.Errorf("expected unknown-tool error result, got %v", results)
	}
}

func TestRun_ToolPanicRecovered(t *testing.T) {
	m := &stubProvider{responses: []*schema.Message{
		{Role: schema.RoleAssistant, ToolCalls: []schema.ToolCall{
			{ID: "call_1", Name: "boom", Args: "{}"},
		}},
		{Role: schema.RoleAssistant, Content: "ok"},
	}}
	reg := newTestRegistry(map[string]tools.Handler{
		"boom": func(ctx context.Context, args json.RawMessage) (string, error) { panic("exploded") },
	})
	sess := &fakeSession{}
	sink := &recordingSink{}
	a := newTestAgent(reg, sess, sink, 5)

	_, err := runTurn(a, context.Background(), "go", m)
	if err != nil {
		t.Fatalf("Run should not return error on tool panic: %v", err)
	}
	results := sink.toolResults()
	if len(results) == 0 || !strings.Contains(results[0], "panicked") {
		t.Fatalf("expected panic error result, got %v", results)
	}
}

func TestRun_MaxIterationsExceeded(t *testing.T) {
	tcMsg := &schema.Message{Role: schema.RoleAssistant, ToolCalls: []schema.ToolCall{
		{ID: "call_1", Name: "echo", Args: "{}"},
	}}
	m := &stubProvider{responses: []*schema.Message{tcMsg}}
	reg := newTestRegistry(map[string]tools.Handler{
		"echo": func(ctx context.Context, args json.RawMessage) (string, error) { return "x", nil },
	})
	a := newTestAgent(reg, &fakeSession{}, &recordingSink{}, 2)

	_, err := runTurn(a, context.Background(), "go", m)
	if err == nil {
		t.Fatal("expected max-iterations error")
	}
}

func TestRun_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	m := &stubProvider{responses: []*schema.Message{{Role: schema.RoleAssistant, Content: "x"}}}
	a := newTestAgent(newTestRegistry(nil), &fakeSession{}, &recordingSink{}, 5)

	_, err := runTurn(a, ctx, "go", m)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestRun_NilSink(t *testing.T) {
	m := &stubProvider{responses: []*schema.Message{{Role: schema.RoleAssistant, Content: "hi"}}}
	a := newTestAgent(newTestRegistry(nil), &fakeSession{}, nil, 1)

	_, err := runTurn(a, context.Background(), "go", m)
	if err != nil {
		t.Fatalf("Run with nil sink: %v", err)
	}
}

func TestRun_ProviderErrorFailsFast(t *testing.T) {
	m := &stubProvider{
		responses: []*schema.Message{{Role: schema.RoleAssistant, Content: "x"}},
		errs:      []error{errors.New("provider down")},
	}
	a := newTestAgent(newTestRegistry(nil), &fakeSession{}, &recordingSink{}, 5)

	_, err := runTurn(a, context.Background(), "go", m)
	if err == nil {
		t.Fatal("expected error on provider failure")
	}
	if m.calls != 1 {
		t.Errorf("provider calls = %d, want 1 (fail fast, no retry)", m.calls)
	}
}

func TestRun_IterationTimeout(t *testing.T) {
	a := NewReAct(Options{
		System:    func() []*schema.Message { return nil },
		Registry:  newTestRegistry(nil),
		Session:   &fakeSession{},
		Sink:      &recordingSink{},
		SessionID: "test-session",
		Limits: func() Limits {
			return Limits{MaxIterations: 5, IterationTimeout: 50 * time.Millisecond}
		},
	})

	start := time.Now()
	_, err := runTurn(a, context.Background(), "go", &stuckProvider{})
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want DeadlineExceeded", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("iteration timeout did not fire promptly")
	}
}
