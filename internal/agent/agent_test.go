package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"tars/pkg/tool/kernel"
	"testing"
	"time"

	"tars/pkg/event"
	"tars/pkg/llm"
	"tars/pkg/schema"
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

// fakeSession 是 agent.Session 的内存实现（交错式：消息尾部追加，无聚合）。
type fakeSession struct {
	msgs []*schema.Message
}

func (s *fakeSession) GetID() string           { return "test-session" }
func (s *fakeSession) GetWorkspaceDir() string { return "" }

func (s *fakeSession) History() []*schema.Message {
	out := make([]*schema.Message, len(s.msgs))
	copy(out, s.msgs)
	return out
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

// testCarrier 是测试用的实名载体：持有一组 handler 映射产出的 Definition。
type testCarrier struct {
	defs []*kernel.Definition
}

func (c *testCarrier) Definitions() []*kernel.Definition { return c.defs }
func (c *testCarrier) Close() error                      { return nil }

// newTestRegistry builds a real tool.Registry (the given custom handlers
// registered into an empty kernel registry). Registry.Execute already provides
// parallel execution, panic recovery, and unknown-tool handling — so tests
// exercise the production path.
func newTestRegistry(handlers map[string]kernel.Handler) *kernel.Registry {
	r := kernel.NewRegistry(nil)
	for name, h := range handlers {
		r.Register(&testCarrier{defs: []*kernel.Definition{{
			Name:       name,
			Parameters: map[string]any{"type": "object"},
			Handler:    h,
		}}})
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

// fakeComposer 是 prompt.Composer 的测试实现（空 system 消息）。
type fakeComposer struct{}

func (fakeComposer) GetSystemMessage() []*schema.Message { return nil }

// fakeSkillStatus 是 SkillStatus 的测试实现（无已加载技能）。
type fakeSkillStatus struct{}

func (fakeSkillStatus) GetLoadedSkills() []string { return nil }

func newTestAgent(reg *kernel.Registry, sess *fakeSession, sink event.Sink, maxIter int) *ReActAgent {
	cfg := &Config{MaxIterations: maxIter}
	cfg.Validate()
	a := NewReAct(cfg, fakeComposer{}, sess, sink, reg, nil, fakeSkillStatus{}, &mockMCPRuntime{}, nil)
	if err := a.Startup(); err != nil {
		panic(err)
	}
	return a
}

// runTurn 以固定 assistantID 跑一轮（provider 作为轮级输入）。
func runTurn(a *ReActAgent, ctx context.Context, userMsg string, p llm.Provider) (*Result, error) {
	return a.Run(ctx, "test-assistant", p)
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
	reg := newTestRegistry(map[string]kernel.Handler{
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
	reg := newTestRegistry(map[string]kernel.Handler{
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
	reg := newTestRegistry(map[string]kernel.Handler{
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

// TestRun_InterleavedLayout 交错式验收（07 篇期 1）：一轮 3 迭代的会话消息
// 呈标准交错序——每次迭代一条全新 assistant 消息，tool 结果紧随其后按 ID 配对；
// 首轮消息 ID = 轮锚点，后续迭代分配新 ID；流式/工具事件携带当轮迭代 ID。
func TestRun_InterleavedLayout(t *testing.T) {
	m := &stubProvider{responses: []*schema.Message{
		{Role: schema.RoleAssistant, Reasoning: "r1", ToolCalls: []schema.ToolCall{
			{ID: "call_1", Name: "echo", Args: `{"text":"a"}`},
		}},
		{Role: schema.RoleAssistant, Reasoning: "r2", ToolCalls: []schema.ToolCall{
			{ID: "call_2", Name: "echo", Args: `{"text":"b"}`},
		}},
		{Role: schema.RoleAssistant, Content: "final"},
	}}
	reg := newTestRegistry(map[string]kernel.Handler{
		"echo": func(ctx context.Context, args json.RawMessage) (string, error) { return "ok", nil },
	})
	sess := &fakeSession{}
	sink := &recordingSink{}
	a := newTestAgent(reg, sess, sink, 5)

	res, err := runTurn(a, context.Background(), "go", m)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Content != "final" || res.Iterations != 3 {
		t.Fatalf("result = %+v, want final/3 iterations", res)
	}

	// 消息序列：assistant, tool, assistant, tool, assistant（标准交错序）
	if len(sess.msgs) != 5 {
		t.Fatalf("messages = %d, want 5", len(sess.msgs))
	}
	wantRoles := []schema.Role{schema.RoleAssistant, schema.RoleTool, schema.RoleAssistant, schema.RoleTool, schema.RoleAssistant}
	for i, role := range wantRoles {
		if sess.msgs[i].Role != role {
			t.Fatalf("msgs[%d].Role = %s, want %s", i, sess.msgs[i].Role, role)
		}
	}
	// 配对相邻：带调用的 assistant 紧随其后是配对的 tool 结果
	if sess.msgs[1].ToolCallID != "call_1" || sess.msgs[3].ToolCallID != "call_2" {
		t.Fatalf("tool pairing broken: %v / %v", sess.msgs[1].ToolCallID, sess.msgs[3].ToolCallID)
	}
	// ID 分配：首轮 = 轮锚点；迭代 2/3 = 新 ID 且互不相同
	if sess.msgs[0].ID != "test-assistant" {
		t.Fatalf("first iteration ID = %s, want turn anchor", sess.msgs[0].ID)
	}
	if sess.msgs[2].ID == "test-assistant" || sess.msgs[4].ID == "test-assistant" ||
		sess.msgs[2].ID == sess.msgs[4].ID {
		t.Fatalf("iteration IDs not distinct: %s / %s", sess.msgs[2].ID, sess.msgs[4].ID)
	}
	// 每迭代消息自带用量/耗时盖章字段（CreatedAt 非零）
	for i, m := range sess.msgs {
		if m.Role == schema.RoleAssistant && m.CreatedAt == 0 {
			t.Fatalf("assistant msgs[%d] missing CreatedAt", i)
		}
	}
	// 流式/工具事件归属当轮迭代 ID：call_1 的 dispatch 属于首轮（锚点），call_2 属于迭代 2
	var dispatchMsgID []string
	for _, e := range sink.events {
		if e.Kind == event.KindToolDispatch {
			dispatchMsgID = append(dispatchMsgID, e.Tool.MessageID)
		}
	}
	if len(dispatchMsgID) != 2 || dispatchMsgID[0] != "test-assistant" || dispatchMsgID[1] != sess.msgs[2].ID {
		t.Fatalf("tool dispatch message IDs = %v", dispatchMsgID)
	}
}

func TestRun_IterationTimeout(t *testing.T) {
	cfg := &Config{MaxIterations: 5, IterationTimeout: 50 * time.Millisecond}
	cfg.Validate()
	a := NewReAct(cfg, fakeComposer{}, &fakeSession{}, &recordingSink{},
		newTestRegistry(nil), nil, fakeSkillStatus{}, &mockMCPRuntime{}, nil)
	if err := a.Startup(); err != nil {
		t.Fatal(err)
	}

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
