package session

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"tars/pkg/compaction"
	"tars/pkg/event"
	"tars/pkg/schema"
)

// newTestManager 在临时目录初始化存储并创建一个会话。
func newTestManager(t *testing.T) *Manager {
	t.Helper()
	InitStoreManager(t.TempDir())
	data, err := GetStoreManager().CreateSession()
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	return NewManager(data, event.Discard)
}

// appendTurns 向会话追加 n 个完整轮（user + assistant + tool），返回消息 ID 序列。
func appendTurns(t *testing.T, m *Manager, n int) []string {
	t.Helper()
	var ids []string
	for k := 0; k < n; k++ {
		tcID := fmt.Sprintf("tc%d", k)
		msgs := []*schema.Message{
			{ID: fmt.Sprintf("u%d", k), Role: schema.RoleUser, Content: "u"},
			{ID: fmt.Sprintf("a%d", k), Role: schema.RoleAssistant, Content: "a",
				ToolCalls: []schema.ToolCall{{ID: tcID, Name: "run_command", Args: "{}"}}},
			{ID: fmt.Sprintf("r%d", k), Role: schema.RoleTool, Content: "ok", ToolCallID: tcID},
		}
		now := int64(1000 + k)
		m.AppendMessage(now, msgs...)
		for _, msg := range msgs {
			ids = append(ids, msg.ID)
		}
	}
	return ids
}

// applyTestCompaction 在 cutoffID 处写入一个最小压缩态。
func applyTestCompaction(t *testing.T, m *Manager, cutoffID string) {
	t.Helper()
	err := m.ApplyCompaction(&compaction.Compaction{
		Entries: []*compaction.ArchiveEntry{{
			Range: "turn_1-1", Goal: "g", Result: "ok", Pointer: "archive/turn_1-1.md",
		}},
		CutoffMessageID: cutoffID,
	})
	if err != nil {
		t.Fatalf("apply compaction: %v", err)
	}
}

func TestProjectionIdentityWithoutCompaction(t *testing.T) {
	m := newTestManager(t)
	appendTurns(t, m, 3)
	if got := len(m.History()); got != 9 {
		t.Fatalf("history len = %d, want 9", got)
	}
}

func TestProjectionWithCompaction(t *testing.T) {
	m := newTestManager(t)
	ids := appendTurns(t, m, 4) // 12 条
	cutoff := ids[5]            // 第 2 轮的 tool 结果
	applyTestCompaction(t, m, cutoff)

	h := m.History()
	// 投影 = 1 条合成归档消息 + cutoff 后 6 条原文
	if len(h) != 7 {
		t.Fatalf("projected len = %d, want 7", len(h))
	}
	if h[0].ID != compaction.SyntheticMessageID || h[0].Role != schema.RoleUser {
		t.Fatalf("synthetic head = %s/%s", h[0].ID, h[0].Role)
	}
	if h[1].ID != ids[6] {
		t.Fatalf("retained head = %s, want %s", h[1].ID, ids[6])
	}
	// 原始轨迹不受影响
	if got := len(m.RawHistory()); got != 12 {
		t.Fatalf("raw len = %d, want 12", got)
	}
}

func TestCompactionPersistenceRoundtrip(t *testing.T) {
	m := newTestManager(t)
	ids := appendTurns(t, m, 4)
	applyTestCompaction(t, m, ids[5])

	loaded, err := GetStoreManager().LoadCompaction(m.GetID())
	if err != nil || loaded == nil {
		t.Fatalf("load compaction: %v", err)
	}
	if loaded.CutoffMessageID != ids[5] || len(loaded.Entries) != 1 {
		t.Fatalf("loaded = %+v", loaded)
	}

	// 全量恢复路径：LoadAllSessionData 应带回压缩态并应用投影
	datas, err := GetStoreManager().LoadAllSessionData()
	if err != nil || len(datas) != 1 {
		t.Fatalf("load all: %v, %d", err, len(datas))
	}
	if datas[0].Compaction == nil {
		t.Fatal("compaction not restored")
	}
	if got := len(datas[0].History()); got != 7 {
		t.Fatalf("restored projection len = %d, want 7", got)
	}
}

func TestLoadCompactionCorrupt(t *testing.T) {
	m := newTestManager(t)
	path := filepath.Join(m.GetDataDir(), CompactionFile)
	if err := os.WriteFile(path, []byte("{broken"), 0644); err != nil {
		t.Fatal(err)
	}
	c, err := GetStoreManager().LoadCompaction(m.GetID())
	if err == nil || c != nil {
		t.Fatalf("corrupt compaction: got (%v, %v), want (nil, err)", c, err)
	}
}

func TestRetryCrossingCutoffInvalidates(t *testing.T) {
	m := newTestManager(t)
	ids := appendTurns(t, m, 4)
	applyTestCompaction(t, m, ids[5]) // cutoff 在第 2 轮末

	// 重试第 1 轮的 assistant（a0）：截断到 u0，cutoff 消息被删 → 作废
	if _, err := m.PrepareRetry("a0"); err != nil {
		t.Fatalf("prepare retry: %v", err)
	}
	if m.Compaction() != nil {
		t.Fatal("compaction should be invalidated")
	}
	if _, err := os.Stat(filepath.Join(m.GetDataDir(), CompactionFile)); !os.IsNotExist(err) {
		t.Fatal("compaction.json should be deleted")
	}
}

func TestRetryWithinTailKeepsCompaction(t *testing.T) {
	m := newTestManager(t)
	ids := appendTurns(t, m, 4)
	applyTestCompaction(t, m, ids[5])

	// 重试最后一轮的 assistant（a3）：截断到 u3（cutoff 之后）→ 保留
	if _, err := m.PrepareRetry("a3"); err != nil {
		t.Fatalf("prepare retry: %v", err)
	}
	if m.Compaction() == nil {
		t.Fatal("compaction should be kept")
	}
}

func TestDeleteFromInvalidation(t *testing.T) {
	m := newTestManager(t)
	ids := appendTurns(t, m, 4)
	applyTestCompaction(t, m, ids[5])

	// 删除点在 cutoff 之后：保留
	if _, err := m.DeleteFrom(ids[8]); err != nil {
		t.Fatalf("delete within tail: %v", err)
	}
	if m.Compaction() == nil {
		t.Fatal("compaction should be kept")
	}
	// 删除点进入压缩区：作废
	if _, err := m.DeleteFrom(ids[2]); err != nil {
		t.Fatalf("delete crossing: %v", err)
	}
	if m.Compaction() != nil {
		t.Fatal("compaction should be invalidated")
	}
}

func TestEditUserMessageInvalidation(t *testing.T) {
	m := newTestManager(t)
	ids := appendTurns(t, m, 4)
	applyTestCompaction(t, m, ids[5])

	// 编辑保留区消息：保留
	if err := m.EditUserMessage("u3", "edited"); err != nil {
		t.Fatalf("edit tail: %v", err)
	}
	if m.Compaction() == nil {
		t.Fatal("compaction should be kept")
	}
	// 编辑压缩区消息：作废
	if err := m.EditUserMessage("u0", "edited"); err != nil {
		t.Fatalf("edit archived: %v", err)
	}
	if m.Compaction() != nil {
		t.Fatal("compaction should be invalidated")
	}
}

func TestUnmarkSkillLoaded(t *testing.T) {
	m := newTestManager(t)
	m.MarkSkillLoaded("pptx")
	if !m.IsSkillLoaded("pptx") {
		t.Fatal("skill should be loaded")
	}
	m.UnmarkSkillLoaded("pptx")
	if m.IsSkillLoaded("pptx") {
		t.Fatal("skill should be unmarked")
	}
	// meta.json 写穿
	meta, err := GetStoreManager().LoadMetadata(m.GetID())
	if err != nil || meta == nil {
		t.Fatalf("load meta: %v", err)
	}
	if _, ok := meta.LoadedSkills["pptx"]; ok {
		t.Fatal("meta.json should not contain pptx")
	}
}
