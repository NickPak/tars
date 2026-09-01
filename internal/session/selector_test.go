package session

import (
	"fmt"
	"testing"

	"tars/pkg/schema"
)

// mkTrajectory 构造 n 个完整轮：每轮 user + assistant(1 个 tool call) + tool 结果。
func mkTrajectory(n int) []*schema.Message { return mkTrajectoryP("m", n) }

// mkTrajectoryP 同 mkTrajectory，但 ID 带前缀（轨迹增长时保持唯一）。
func mkTrajectoryP(prefix string, n int) []*schema.Message {
	var out []*schema.Message
	id := 0
	next := func() string { id++; return fmt.Sprintf("%s%d", prefix, id) }
	for t := 0; t < n; t++ {
		tcID := fmt.Sprintf("%s-tc%d", prefix, t)
		out = append(out,
			&schema.Message{ID: next(), Role: schema.RoleUser, Content: "u"},
			&schema.Message{ID: next(), Role: schema.RoleAssistant, Content: "a",
				ToolCalls: []schema.ToolCall{{ID: tcID, Name: "run_command", Args: "{}"}}},
			&schema.Message{ID: next(), Role: schema.RoleTool, Content: "ok", ToolCallID: tcID},
		)
	}
	return out
}

func TestSelectKeepsTailTurns(t *testing.T) {
	raw := mkTrajectory(10) // 30 条
	sel := NewTailKeepSelector(3, 2)
	batch, cutoff, ok := sel.Select(raw, "", PressureNormal)
	if !ok {
		t.Fatal("expected ok")
	}
	if len(batch) != 21 { // 前 7 轮
		t.Fatalf("batch len = %d, want 21", len(batch))
	}
	if cutoff != batch[len(batch)-1].ID {
		t.Fatalf("cutoff = %s, want %s", cutoff, batch[len(batch)-1].ID)
	}
	// 轮边界对齐：保留区首条必须是 user（assistant 与 tool 结果配对不切断）。
	if next := raw[len(batch)]; next.Role != schema.RoleUser {
		t.Fatalf("retained head role = %s, want user", next.Role)
	}
}

func TestSelectRespectsMinBatch(t *testing.T) {
	raw := mkTrajectory(3) // 9 条
	sel := NewTailKeepSelector(2, 8)
	if _, _, ok := sel.Select(raw, "", PressureNormal); ok {
		t.Fatal("expected not ok: batch (3) < minBatch (8)")
	}
}

func TestSelectTooFewTurns(t *testing.T) {
	raw := mkTrajectory(6) // 恰好 6 轮
	sel := NewTailKeepSelector(6, 1)
	if _, _, ok := sel.Select(raw, "", PressureNormal); ok {
		t.Fatal("expected not ok: no compressible turns beyond keepTurns")
	}
}

func TestSelectIncremental(t *testing.T) {
	raw := mkTrajectory(10)
	sel := NewTailKeepSelector(3, 2)

	batch1, cutoff1, ok1 := sel.Select(raw, "", PressureNormal)
	if !ok1 {
		t.Fatal("first select: expected ok")
	}
	// 同一轨迹上重复选择：没有新轮次可压，应放弃（增量 append-only）。
	if _, _, ok := sel.Select(raw, cutoff1, PressureNormal); ok {
		t.Fatal("second select on unchanged trajectory should not be ok")
	}

	// 轨迹增长 5 轮后第二次选择：从旧 cutoff 之后开始。
	raw = append(raw, mkTrajectoryP("b", 5)...)
	batch2, cutoff2, ok2 := sel.Select(raw, cutoff1, PressureNormal)
	if !ok2 {
		t.Fatal("second select after growth: expected ok")
	}
	if batch2[0].ID != raw[len(batch1)].ID {
		t.Fatalf("second batch starts at %s, want %s", batch2[0].ID, raw[len(batch1)].ID)
	}
	if cutoff2 == cutoff1 {
		t.Fatal("cutoff did not advance")
	}
	if len(batch2) != 15 { // 旧尾部 3 轮 + 新增 2 轮，留 3 轮
		t.Fatalf("batch2 len = %d, want 15", len(batch2))
	}
}

func TestSelectCutoffLostFallback(t *testing.T) {
	raw := mkTrajectory(10)
	sel := NewTailKeepSelector(3, 2)
	batchLost, _, ok1 := sel.Select(raw, "nonexistent", PressureNormal)
	batchFresh, _, ok2 := sel.Select(raw, "", PressureNormal)
	if !ok1 || !ok2 {
		t.Fatal("expected ok for both")
	}
	if batchLost[0].ID != batchFresh[0].ID || len(batchLost) != len(batchFresh) {
		t.Fatal("lost cutoff should fall back to从头开始")
	}
}

// --- 压力降级阶梯 ---

// 档位越高保留区越窄、压缩集越大（保留区是预算里最大的一项）。
func TestSelectPressureShrinksRetained(t *testing.T) {
	raw := mkTrajectory(10) // 30 条
	sel := NewTailKeepSelector(6, 8)

	want := map[Pressure]int{
		PressureNormal:   12, // 留 6 轮 → 压 4 轮
		PressureHigh:     21, // 留 3 轮 → 压 7 轮
		PressureCritical: 27, // 留 1 轮 → 压 9 轮
	}
	for _, p := range Pressures {
		batch, _, ok := sel.Select(raw, "", p)
		if !ok {
			t.Fatalf("pressure %s: expected ok", p)
		}
		if len(batch) != want[p] {
			t.Fatalf("pressure %s: batch len = %d, want %d", p, len(batch), want[p])
		}
	}
}

// 极限档忽略 minBatch：此刻不压的后果是请求超长被拒、整轮失败，
// 任何压缩都比失败强。
func TestSelectCriticalIgnoresMinBatch(t *testing.T) {
	raw := mkTrajectory(2) // 6 条
	sel := NewTailKeepSelector(6, 8)

	if _, _, ok := sel.Select(raw, "", PressureNormal); ok {
		t.Fatal("normal: expected not ok (keepTurns=6 covers all turns)")
	}
	batch, _, ok := sel.Select(raw, "", PressureCritical)
	if !ok {
		t.Fatal("critical: expected ok")
	}
	if len(batch) != 3 { // 留最后 1 轮，压第 1 轮的 3 条（< minBatch=8 但仍压）
		t.Fatalf("critical batch len = %d, want 3", len(batch))
	}
}

// keepTurns=1 时高压档不得把保留区算成 0 轮（向上取整）。
func TestSelectHighPressureKeepsAtLeastOneTurn(t *testing.T) {
	sel := &TailKeep{keepTurns: 1, minBatch: 1}
	if keep, _ := sel.params(PressureHigh); keep != 1 {
		t.Fatalf("keepTurns at high pressure = %d, want 1", keep)
	}
	raw := mkTrajectory(3)
	batch, _, ok := sel.Select(raw, "", PressureHigh)
	if !ok || len(batch) != 6 {
		t.Fatalf("batch len = %d (ok=%v), want 6", len(batch), ok)
	}
}
