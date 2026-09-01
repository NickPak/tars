package session

import (
	"context"
	"testing"

	"tars/pkg/schema"
)

// 写入侧：user 消息自带轮键，其后 assistant/tool 继承同一键。
func TestTurnIDStamping(t *testing.T) {
	m := newTestManager(t)
	uid := m.AppendUserMessage("hi")
	m.AppendMessage(1, &schema.Message{ID: "a1", Role: schema.RoleAssistant, Iteration: 1})
	m.AppendMessage(2, &schema.Message{ID: "r1", Role: schema.RoleTool, Iteration: 1, ToolCallID: "tc"})
	uid2 := m.AppendUserMessage("again")
	m.AppendMessage(3, &schema.Message{ID: "a2", Role: schema.RoleAssistant, Iteration: 1})

	msgs := m.data.Messages
	want := map[string]string{uid: uid, "a1": uid, "r1": uid, uid2: uid2, "a2": uid2}
	for _, msg := range msgs {
		if got := want[msg.ID]; msg.TurnID != got {
			t.Fatalf("msg %s TurnID = %q, want %q", msg.ID, msg.TurnID, got)
		}
	}
	if got := m.data.CurrentTurnID(); got != uid2 {
		t.Fatalf("CurrentTurnID = %q, want %q", got, uid2)
	}
	// 批量追加：批内 user 消息开新轮，后续消息归属它（不误继承上一轮）
	m.AppendMessage(4,
		&schema.Message{ID: "u3", Role: schema.RoleUser},
		&schema.Message{ID: "a3", Role: schema.RoleAssistant, Iteration: 1},
	)
	last := m.data.Messages[len(m.data.Messages)-1]
	if last.TurnID != "u3" {
		t.Fatalf("batch-appended assistant TurnID = %q, want u3", last.TurnID)
	}
}

func TestTurnStarts(t *testing.T) {
	msgs := []*schema.Message{
		{ID: "u1", Role: schema.RoleUser, TurnID: "u1"},
		{ID: "a1", Role: schema.RoleAssistant, TurnID: "u1", Iteration: 1},
		{ID: "r1", Role: schema.RoleTool, TurnID: "u1", Iteration: 1},
		{ID: "a2", Role: schema.RoleAssistant, TurnID: "u1", Iteration: 2},
		{ID: "u2", Role: schema.RoleUser, TurnID: "u2"},
		{ID: "a3", Role: schema.RoleAssistant, TurnID: "u2", Iteration: 1},
	}
	if got := TurnStarts(msgs); len(got) != 2 || got[0] != 0 || got[1] != 4 {
		t.Fatalf("TurnStarts = %v, want [0 4]", got)
	}
	if got := TurnStartBefore(msgs, 3); got != 0 {
		t.Fatalf("TurnStartBefore(3) = %d, want 0", got)
	}
	if got := TurnStartBefore(msgs, 5); got != 4 {
		t.Fatalf("TurnStartBefore(5) = %d, want 4", got)
	}
}

// Iteration 不连续（截断/删除后出现缺口、非 1 起始）不影响轮划分——
// 分组只认 TurnID，序号纯展示。
func TestTurnStartsIgnoresIterationGaps(t *testing.T) {
	msgs := []*schema.Message{
		{ID: "u1", Role: schema.RoleUser, TurnID: "u1"},
		{ID: "a1", Role: schema.RoleAssistant, TurnID: "u1", Iteration: 3}, // 非 1 起始
		{ID: "a2", Role: schema.RoleAssistant, TurnID: "u1", Iteration: 7}, // 缺口
		{ID: "u2", Role: schema.RoleUser, TurnID: "u2"},
		{ID: "a3", Role: schema.RoleAssistant, TurnID: "u2", Iteration: 0}, // 缺失
	}
	if got := TurnStarts(msgs); len(got) != 2 || got[0] != 0 || got[1] != 3 {
		t.Fatalf("TurnStarts = %v, want [0 3]", got)
	}
	if got := TurnStartBefore(msgs, 2); got != 0 {
		t.Fatalf("TurnStartBefore(2) = %d, want 0", got)
	}
}

// 旧数据（无 TurnID）回退"轮起点 = user 消息"的扫描规则。
func TestTurnStartsLegacyFallback(t *testing.T) {
	msgs := []*schema.Message{
		{ID: "u1", Role: schema.RoleUser},
		{ID: "a1", Role: schema.RoleAssistant},
		{ID: "r1", Role: schema.RoleTool},
		{ID: "u2", Role: schema.RoleUser},
		{ID: "a2", Role: schema.RoleAssistant},
	}
	if got := TurnStarts(msgs); len(got) != 2 || got[0] != 0 || got[1] != 3 {
		t.Fatalf("TurnStarts = %v, want [0 3]", got)
	}
	if got := TurnStartBefore(msgs, 4); got != 3 {
		t.Fatalf("TurnStartBefore(4) = %d, want 3", got)
	}
}

// 合成归档消息（压缩投影产物，role=user）不得被计为轮起点。
func TestTurnStartsSkipsSyntheticMessage(t *testing.T) {
	msgs := []*schema.Message{
		{ID: SyntheticMessageID, Role: schema.RoleUser},
		{ID: "u1", Role: schema.RoleUser, TurnID: "u1"},
		{ID: "a1", Role: schema.RoleAssistant, TurnID: "u1", Iteration: 1},
	}
	if got := TurnStarts(msgs); len(got) != 1 || got[0] != 1 {
		t.Fatalf("TurnStarts = %v, want [1]", got)
	}
}

// 重试截断到轮起点：TurnID 跨重试稳定（复用同一 user 消息）。
func TestPrepareRetryUsesTurnBoundary(t *testing.T) {
	m := newTestManager(t)
	uid := m.AppendUserMessage("q")
	m.AppendMessage(1, &schema.Message{ID: "a1", Role: schema.RoleAssistant, Iteration: 1,
		ToolCalls: []schema.ToolCall{{ID: "tc", Name: "x"}}})
	m.AppendMessage(2, &schema.Message{ID: "r1", Role: schema.RoleTool, Iteration: 1, ToolCallID: "tc"})
	m.AppendMessage(3, &schema.Message{ID: "a2", Role: schema.RoleAssistant, Iteration: 2})

	// 重试第 2 次迭代的消息 → 截断到该轮起点（保留 user 一条）
	text, err := m.PrepareRetry("a2")
	if err != nil {
		t.Fatalf("prepare retry: %v", err)
	}
	if text != "q" {
		t.Fatalf("userText = %q, want q", text)
	}
	if len(m.data.Messages) != 1 || m.data.Messages[0].ID != uid {
		t.Fatalf("messages after retry = %+v", m.data.Messages)
	}
	// 新一轮消息继续挂在同一 TurnID 下
	m.AppendMessage(4, &schema.Message{ID: "a3", Role: schema.RoleAssistant, Iteration: 1})
	if got := m.data.Messages[1].TurnID; got != uid {
		t.Fatalf("post-retry TurnID = %q, want %q（跨重试稳定）", got, uid)
	}
}

// 压缩投影后的视图从中间轮开始：轮划分仍正确（合成消息被跳过）。
func TestTurnStartsOnProjectedView(t *testing.T) {
	m, _ := newCompressingManager(t, nil)
	appendTurns(t, m, 10)
	withLastUsage(m, 200000)
	m.MaybeCompress(context.Background(), nil)
	if m.Compaction() == nil {
		t.Fatal("compression did not happen")
	}

	view := m.History()
	if view[0].ID != SyntheticMessageID {
		t.Fatalf("projected head = %s, want synthetic", view[0].ID)
	}
	starts := TurnStarts(view)
	if len(starts) != testKeepTurns {
		t.Fatalf("projected turns = %d, want %d", len(starts), testKeepTurns)
	}
	if starts[0] != 1 {
		t.Fatalf("first turn start = %d, want 1 (after synthetic)", starts[0])
	}
}
