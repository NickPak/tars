package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"tars/pkg/tools"

	"github.com/cloudwego/eino/schema"
)

func TestRun_StatusBarInjectedAndNotPersisted(t *testing.T) {
	m := &stubModel{responses: []*schema.Message{
		{Role: schema.Assistant, Content: "hello"},
	}}
	a := New(m, newTestManager(nil), 5)

	h := &recordingHooks{}
	_, err := a.Run(context.Background(), []*schema.Message{{Role: schema.User, Content: "hi"}}, h)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(h.iterDeltas) != 1 {
		t.Fatalf("iterDeltas = %d rounds, want 1", len(h.iterDeltas))
	}
	for _, msg := range h.iterDeltas[0] {
		if strings.Contains(msg.Content, "<agent_status") {
			t.Errorf("status bar leaked into IterationEnd delta: %q", msg.Content)
		}
	}
}

func TestStatusBar_CountersTrackToolCalls(t *testing.T) {
	sb := NewStatusBar()

	// 空计数器不渲染 calls 行
	msg := sb.Render(context.Background(), 1)
	if strings.Contains(msg.Content, "calls:") {
		t.Errorf("empty counters should not render calls line: %q", msg.Content)
	}

	// 记录两次 echo 调用（成功）
	sb.RecordToolCall("echo", nil)
	sb.RecordToolCall("echo", nil)
	msg = sb.Render(context.Background(), 2)
	if !strings.Contains(msg.Content, "echo×2") {
		t.Errorf("counters should list echo×2: %q", msg.Content)
	}
	if !strings.Contains(msg.Content, "iteration: 2") {
		t.Errorf("counters should show iteration: %q", msg.Content)
	}
}

func TestStatusBar_ConsecutiveErrorsProduceHint(t *testing.T) {
	sb := NewStatusBar()
	sb.RecordToolCall("fail", fmt.Errorf("boom"))

	msg := sb.Render(context.Background(), 2)
	if !strings.Contains(msg.Content, "连续 1 次失败") {
		t.Errorf("expected consecutive failure hint: %q", msg.Content)
	}
	if !strings.Contains(msg.Content, "勿原样重试") {
		t.Errorf("expected do-not-retry hint: %q", msg.Content)
	}

	// 成功一次后清零
	sb.RecordToolCall("ok", nil)
	msg = sb.Render(context.Background(), 3)
	if strings.Contains(msg.Content, "连续") {
		t.Errorf("consecutive errors should clear after success: %q", msg.Content)
	}
}

func TestStatusBar_StaticEnvInInit(t *testing.T) {
	// 静态字段（os/shell）在 New 时初始化，Render 后应出现
	sb := NewStatusBar()
	msg := sb.Render(context.Background(), 1)
	if !strings.Contains(msg.Content, "os: ") {
		t.Errorf("os should appear in env zone: %q", msg.Content)
	}
	if !strings.Contains(msg.Content, "shell: ") {
		t.Errorf("shell should appear in env zone: %q", msg.Content)
	}
}

func TestStatusBar_SeqIncrementsWithIteration(t *testing.T) {
	sb := NewStatusBar()
	for _, iter := range []int{1, 5, 100} {
		msg := sb.Render(context.Background(), iter)
		want := fmt.Sprintf(`seq="%d"`, iter)
		if !strings.Contains(msg.Content, want) {
			t.Errorf("iter %d: expected %q in %q", iter, want, msg.Content)
		}
	}
}

func TestRun_ToolErrorOutputNoPanic(t *testing.T) {
	m := &stubModel{responses: []*schema.Message{
		{Role: schema.Assistant, ToolCalls: []schema.ToolCall{
			{ID: "c1", Function: schema.FunctionCall{Name: "broken", Arguments: "{}"}},
		}},
		{Role: schema.Assistant, Content: "recovered"},
	}}
	mgr := newTestManager(map[string]tools.Handler{
		"broken": func(_ context.Context, _ json.RawMessage) (string, error) {
			return "permission denied", fmt.Errorf("permission denied")
		},
	})
	a := New(m, mgr, 5)

	_, err := a.Run(context.Background(), []*schema.Message{{Role: schema.User, Content: "go"}}, &recordingHooks{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
}
