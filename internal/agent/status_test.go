package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"tars/pkg/store"
	"tars/pkg/tools"

	"github.com/cloudwego/eino/schema"
)

func TestRun_StatusBarInjectedAndNotPersisted(t *testing.T) {
	m := &stubModel{responses: []*schema.Message{
		{Role: schema.Assistant, Content: "hello"},
	}}
	a := New(5, 0, newTestManager(nil), m)

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
	a := New(5, 0, mgr, m)

	_, err := a.Run(context.Background(), []*schema.Message{{Role: schema.User, Content: "go"}}, &recordingHooks{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// --- TODO zone tests ---

func TestStatusBar_TodoZoneRendersFromStore(t *testing.T) {
	sb := NewStatusBar()
	todoStore := store.NewTodoStore("") // 无文件路径，纯内存
	todoStore.Replace([]store.Todo{
		{ID: "1", Content: "搭建 MCP 服务器", Status: store.TodoInProgress},
		{ID: "2", Content: "编写测试", Status: store.TodoPending},
	})

	msg := sb.Render(store.WithTodoStore(context.Background(), todoStore), 1)

	if !strings.Contains(msg.Content, "<todo>") {
		t.Errorf("expected <todo> zone: %q", msg.Content)
	}
	if !strings.Contains(msg.Content, "搭建 MCP 服务器") {
		t.Errorf("expected todo content: %q", msg.Content)
	}
	if !strings.Contains(msg.Content, "in_progress") {
		t.Errorf("expected todo status: %q", msg.Content)
	}
	if !strings.Contains(msg.Content, "[1]") {
		t.Errorf("expected numbered index: %q", msg.Content)
	}
}

func TestStatusBar_TodoZoneOmittedWhenEmpty(t *testing.T) {
	sb := NewStatusBar()
	todoStore := store.NewTodoStore("")

	msg := sb.Render(store.WithTodoStore(context.Background(), todoStore), 1)

	if strings.Contains(msg.Content, "<todo>") {
		t.Errorf("empty todo should not render <todo> zone: %q", msg.Content)
	}
}

func TestStatusBar_TodoZoneOmittedWhenNoStore(t *testing.T) {
	sb := NewStatusBar()
	// ctx 中不注入 TodoStore
	msg := sb.Render(context.Background(), 1)
	if strings.Contains(msg.Content, "<todo>") {
		t.Errorf("nil todoStore should not render <todo> zone: %q", msg.Content)
	}
}

func TestStatusBar_TodoStalenessReminder(t *testing.T) {
	sb := NewStatusBar()
	todoStore := store.NewTodoStore("")
	todoStore.Replace([]store.Todo{
		{ID: "1", Content: "任务A", Status: store.TodoPending},
	})
	ctx := store.WithTodoStore(context.Background(), todoStore)

	// 第 1 轮：刚创建，不提示
	msg := sb.Render(ctx, 1)
	if strings.Contains(msg.Content, "未更新") {
		t.Errorf("iteration 1 should not show staleness: %q", msg.Content)
	}

	// 第 4 轮：已 3 轮未更新，应提示
	msg = sb.Render(ctx, 4)
	if !strings.Contains(msg.Content, "已 3 轮未更新") {
		t.Errorf("iteration 4 should show staleness reminder: %q", msg.Content)
	}

	// 更新 todo（版本变化），重置计数
	todoStore.Replace([]store.Todo{
		{ID: "1", Content: "任务A", Status: store.TodoInProgress},
	})
	msg = sb.Render(ctx, 5)
	if strings.Contains(msg.Content, "未更新") {
		t.Errorf("after update, staleness should reset: %q", msg.Content)
	}
}

func TestStatusBar_TodoStalenessSkipsWhenAllDone(t *testing.T) {
	sb := NewStatusBar()
	todoStore := store.NewTodoStore("")
	todoStore.Replace([]store.Todo{
		{ID: "1", Content: "任务A", Status: store.TodoCompleted},
		{ID: "2", Content: "任务B", Status: store.TodoCancelled},
	})

	// 即使多轮未更新，全完成/取消时不提示
	msg := sb.Render(store.WithTodoStore(context.Background(), todoStore), 5)
	if strings.Contains(msg.Content, "未更新") {
		t.Errorf("no staleness when all done/cancelled: %q", msg.Content)
	}
}
