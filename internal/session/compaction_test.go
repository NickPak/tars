package session

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tars/pkg/event"
	"tars/pkg/llm"
	"tars/pkg/schema"
)

// 测试默认压缩参数（与 agent.Config 的默认值一致）。
const (
	testThreshold   = 0.8
	testKeepTurns   = 6
	testMinBatch    = 8
	testMaxFailures = 3
)

// testLLMManager 构造仅用于阈值判定的 llm.Manager（不建模型客户端）。
func testLLMManager(t *testing.T, contextWindow int) *llm.Manager {
	t.Helper()
	cfg := &llm.Config{
		Active: "p/m",
		Providers: map[string]*llm.ProviderConfig{
			"p": {ID: "p", Type: llm.ProviderOpenAI, BaseUrl: "http://127.0.0.1:1"},
		},
		Models: map[string]*llm.ModelConfig{
			"p/m": {EntryID: "p/m", Provider: "p", ModelId: "m", ContextWindow: contextWindow},
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("llm config: %v", err)
	}
	return llm.NewManager(cfg)
}

// recordingSink 捕获发射的事件（压缩事件断言用）。
type recordingSink struct{ events []event.Event }

func (r *recordingSink) Emit(e event.Event) { r.events = append(r.events, e) }

// newTestManager 在临时目录初始化存储并创建一个会话（默认压缩参数，静默 sink）。
func newTestManager(t *testing.T) *Manager {
	t.Helper()
	m, _ := newManagerWithSink(t, event.Discard, nil)
	return m
}

// newCompressingManager 创建带事件捕获的会话；ext 为 nil 时用 StaticExtractor
// （不调 LLM，供管线贯通测试）。
func newCompressingManager(t *testing.T, ext Extractor) (*Manager, *recordingSink) {
	t.Helper()
	sink := &recordingSink{}
	if ext == nil {
		ext = StaticExtractor{}
	}
	return newManagerWithSink(t, sink, ext)
}

func newManagerWithSink(t *testing.T, sink event.Sink, ext Extractor) (*Manager, *recordingSink) {
	t.Helper()
	InitStoreManager(t.TempDir())
	data, err := GetStoreManager().CreateSession()
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	m := NewManager(data, sink, testLLMManager(t, 128000),
		testThreshold, testKeepTurns, testMinBatch, testMaxFailures)
	if ext != nil {
		m.extractor = ext // 包内测试直接注入，避免为测试增设装配面
	}
	rec, _ := sink.(*recordingSink)
	return m, rec
}

// appendTurns 向会话追加 n 个完整轮（user + assistant + tool），返回消息 ID 序列。
func appendTurns(t *testing.T, m *Manager, n int) []string {
	t.Helper()
	return appendTurnsP(t, m, "", n)
}

// appendTurnsP 同 appendTurns，ID 带前缀（轨迹多次增长时保持唯一）。
// 消息内容取真实体量（工具输出 KB 级）——压缩的经济性校验以字节数为准，
// 占位符级的短内容会让"条目比原文还大"，与生产场景不符。
func appendTurnsP(t *testing.T, m *Manager, prefix string, n int) []string {
	t.Helper()
	userText := strings.Repeat("请帮我检查这个模块的实现。", 8)                           // ~300B
	assistantText := strings.Repeat("我先读取相关文件确认现状。", 8)                      // ~300B
	toolOutput := strings.Repeat("file content line with some detail\n", 40) // ~1.4KB
	var ids []string
	for k := 0; k < n; k++ {
		tcID := fmt.Sprintf("%stc%d", prefix, k)
		msgs := []*schema.Message{
			{ID: fmt.Sprintf("%su%d", prefix, k), Role: schema.RoleUser, Content: userText},
			{ID: fmt.Sprintf("%sa%d", prefix, k), Role: schema.RoleAssistant, Content: assistantText,
				ToolCalls: []schema.ToolCall{{ID: tcID, Name: "run_command", Args: `{"command":"go build ./..."}`}}},
			{ID: fmt.Sprintf("%sr%d", prefix, k), Role: schema.RoleTool, Content: toolOutput, ToolCallID: tcID},
		}
		now := int64(1000 + k)
		m.AppendMessage(now, msgs...)
		for _, msg := range msgs {
			ids = append(ids, msg.ID)
		}
	}
	return ids
}

// withLastUsage 给轨迹最后一条 assistant 消息标注实测用量（压缩触发信号）。
func withLastUsage(m *Manager, promptTokens int) {
	msgs := m.data.Messages
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == schema.RoleAssistant {
			msgs[i].Usage = &schema.UsageInfo{PromptTokens: promptTokens}
			return
		}
	}
}

// applyTestCompaction 在 cutoffID 处写入一个最小压缩态。
func applyTestCompaction(t *testing.T, m *Manager, cutoffID string) {
	t.Helper()
	err := m.SetCompaction(&CompactionData{
		Entries: []*ArchiveEntry{{
			Range: "turn_1-1", Goal: "g", Result: "ok", Pointer: "archive/turn_1-1.md",
		}},
		CutoffMessageID: cutoffID,
	})
	if err != nil {
		t.Fatalf("apply compaction: %v", err)
	}
}

// --- 合成归档消息渲染（冻结：同输入同字节） ---

func TestMessageDeterministic(t *testing.T) {
	c := &CompactionData{Entries: []*ArchiveEntry{{
		Range: "turn_1-3", Goal: "g", UserAsks: []string{"帮我修一下超时"},
		Actions: []string{"a"}, Result: "ok",
		Facts: []string{"f.go", "abc123"}, Pointer: "archive/turn_1-3.md",
	}}}
	m1 := c.Message()
	m2 := c.Message()
	if m1 == nil || m2 == nil {
		t.Fatal("Message returned nil")
	}
	if m1.Content != m2.Content {
		t.Fatal("render not frozen: byte mismatch across calls")
	}
	if m1.ID != SyntheticMessageID || m1.Role != schema.RoleUser {
		t.Fatalf("synthetic message id/role = %s/%s", m1.ID, m1.Role)
	}
	if !strings.Contains(m1.Content, "<context_archive") || !strings.Contains(m1.Content, "turn_1-3") {
		t.Fatal("archive content missing expected markers")
	}
}

// 视图形态是紧凑文本而非 JSON：不得出现 JSON 语法噪声，且各字段齐备。
func TestMessageRendersCompactText(t *testing.T) {
	c := &CompactionData{Entries: []*ArchiveEntry{{
		Range: "turn_1-2", Goal: "定位超时配置", UserAsks: []string{"帮我修一下超时"},
		Actions: []string{"read_file pkg/llm/config.go", "edit manager.go"},
		Result:  "成功；曾误改 provider.go 后回滚",
		Facts:   []string{"pkg/llm/config.go", "pkg/llm/manager.go"},
		Pointer: "archive/turn_1-2.md",
	}}}
	got := c.Message().Content

	for _, want := range []string{
		"### turn_1-2 → archive/turn_1-2.md",
		"goal: 定位超时配置",
		"user: 帮我修一下超时",
		"did:\n- read_file pkg/llm/config.go\n- edit manager.go",
		"result: 成功；曾误改 provider.go 后回滚",
		"keep: pkg/llm/config.go, pkg/llm/manager.go",
		"</context_archive>",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	// JSON 语法税必须消失（引号括号缩进对模型无信息价值）
	for _, bad := range []string{`"goal":`, `"pointer":`, "[\n", "null"} {
		if strings.Contains(got, bad) {
			t.Fatalf("JSON artifact %q leaked into view:\n%s", bad, got)
		}
	}
}

// 空字段不渲染：避免 null / 空行白占 token。
func TestMessageOmitsEmptyFields(t *testing.T) {
	c := &CompactionData{Entries: []*ArchiveEntry{{
		Range: "turn_1-1", Goal: "g", Pointer: "archive://turn_1-1.md",
	}}}
	got := c.Message().Content
	for _, bad := range []string{"user:", "did:", "result:", "keep:"} {
		if strings.Contains(got, bad) {
			t.Fatalf("empty field %q rendered:\n%s", bad, got)
		}
	}
}

// 归档区指引必须同时说到三件事：归档存在、怎么读、何时读。
// 缺任何一条，"摘要不足时读回原文"这条通道就失效——不说存在模型不知道有
// 原文可捞；不说读法它会拿指针猜普通路径；不说节制它会把所有指针读一遍。
func TestMessageTeachesArchiveAccess(t *testing.T) {
	c := &CompactionData{Entries: []*ArchiveEntry{{
		Range: "turn_1-2", Goal: "g", Pointer: "archive://turn_1-2.md",
	}}}
	got := c.Message().Content

	// ① 存在性
	if !strings.Contains(got, "compacted") {
		t.Fatalf("header must state that earlier turns were compacted:\n%s", got)
	}
	// ② 读法：确切的调用形态 + 可原样复制的指针
	if !strings.Contains(got, `read_file(path="archive://`) {
		t.Fatalf("header must show the exact read_file call form:\n%s", got)
	}
	if !strings.Contains(got, "### turn_1-2 → archive://turn_1-2.md") {
		t.Fatalf("pointer must appear in copy-pasteable form:\n%s", got)
	}
	// ③ 节制
	if !strings.Contains(got, "ONLY when") {
		t.Fatalf("header must tell the model to read pointers sparingly:\n%s", got)
	}
}

func TestMessageNilEmpty(t *testing.T) {
	var nilC *CompactionData
	if nilC.Message() != nil {
		t.Fatal("nil compaction should render nil")
	}
	if (&CompactionData{}).Message() != nil {
		t.Fatal("empty entries should render nil")
	}
}

// --- 归档区膨胀闸门：存根与丢弃 ---

func TestEntryStub(t *testing.T) {
	e := &ArchiveEntry{
		Range: "turn_1-2", Goal: "g", UserAsks: []string{"q"},
		Actions: []string{"a"}, Result: "ok", Facts: []string{"f.go"},
		Pointer: "archive/turn_1-2.md",
	}
	if e.IsStub() {
		t.Fatal("full entry misreported as stub")
	}
	e.Stub()
	if !e.IsStub() {
		t.Fatal("stubbed entry should report IsStub")
	}
	// 存根保留"有这么一段、目标是什么、原文在哪"——pointer 是逃生通道，不可丢。
	if e.Range == "" || e.Goal == "" || e.Pointer == "" {
		t.Fatalf("stub lost identity fields: %+v", e)
	}
	// 且体量显著下降
	full := renderEntries([]*ArchiveEntry{{
		Range: "turn_1-2", Goal: "g", UserAsks: []string{"q"},
		Actions: []string{"a"}, Result: "ok", Facts: []string{"f.go"},
		Pointer: "archive/turn_1-2.md",
	}})
	if stub := renderEntries([]*ArchiveEntry{e}); len(stub) >= len(full) {
		t.Fatalf("stub (%d B) should be smaller than full entry (%d B)", len(stub), len(full))
	}
}

func TestCapEntriesStubsOldest(t *testing.T) {
	m := newTestManager(t)
	m.MaxFullEntries, m.MaxEntries = 2, 100

	mk := func(i int) *ArchiveEntry {
		return &ArchiveEntry{
			Range: fmt.Sprintf("turn_%d-%d", i, i), Goal: "g",
			Actions: []string{"a"}, Result: "ok", Facts: []string{"f.go"},
			Pointer: fmt.Sprintf("archive/turn_%d-%d.md", i, i),
		}
	}
	in := []*ArchiveEntry{mk(1), mk(2), mk(3), mk(4)}
	out := m.capEntries(in)

	if len(out) != 4 {
		t.Fatalf("capEntries dropped entries: len = %d, want 4", len(out))
	}
	// 最旧 2 条退化为存根，最新 2 条保持完整
	for i, want := range []bool{true, true, false, false} {
		if out[i].IsStub() != want {
			t.Fatalf("entry %d IsStub = %v, want %v", i, out[i].IsStub(), want)
		}
	}
	// 原地不改：入参条目仍是完整形态（compress 承诺失败时内存态不变）
	if in[0].IsStub() {
		t.Fatal("capEntries mutated the caller's entries in place")
	}
}

func TestCapEntriesDropsOldestStubs(t *testing.T) {
	m := newTestManager(t)
	m.MaxFullEntries, m.MaxEntries = 1, 3

	in := make([]*ArchiveEntry, 0, 6)
	for i := 1; i <= 6; i++ {
		in = append(in, &ArchiveEntry{
			Range: fmt.Sprintf("turn_%d-%d", i, i), Goal: "g", Actions: []string{"a"},
			Pointer: fmt.Sprintf("archive/turn_%d-%d.md", i, i),
		})
	}
	out := m.capEntries(in)

	if len(out) != 3 {
		t.Fatalf("len = %d, want 3 (MaxEntries)", len(out))
	}
	// 丢的是最旧端，留下的是最新 3 条
	if out[0].Range != "turn_4-4" || out[2].Range != "turn_6-6" {
		t.Fatalf("wrong window kept: %s..%s", out[0].Range, out[2].Range)
	}
	// 窗口内最旧的 2 条仍为存根，只有最新 1 条完整
	if out[0].IsStub() != true || out[1].IsStub() != true || out[2].IsStub() != false {
		t.Fatalf("stub pattern = %v/%v/%v", out[0].IsStub(), out[1].IsStub(), out[2].IsStub())
	}
}

func TestCapEntriesNoopUnderLimits(t *testing.T) {
	m := newTestManager(t)
	in := []*ArchiveEntry{
		{Range: "turn_1-1", Goal: "g", Actions: []string{"a"}, Pointer: "archive/turn_1-1.md"},
	}
	out := m.capEntries(in)
	if len(out) != 1 || out[0].IsStub() {
		t.Fatalf("entries under limits should pass through untouched: %+v", out)
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
	if h[0].ID != SyntheticMessageID || h[0].Role != schema.RoleUser {
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
	if m.GetCompaction() != nil {
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
	if m.GetCompaction() == nil {
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
	if m.GetCompaction() == nil {
		t.Fatal("compaction should be kept")
	}
	// 删除点进入压缩区：作废
	if _, err := m.DeleteFrom(ids[2]); err != nil {
		t.Fatalf("delete crossing: %v", err)
	}
	if m.GetCompaction() != nil {
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
	if m.GetCompaction() == nil {
		t.Fatal("compaction should be kept")
	}
	// 编辑压缩区消息：作废
	if err := m.EditUserMessage("u0", "edited"); err != nil {
		t.Fatalf("edit archived: %v", err)
	}
	if m.GetCompaction() != nil {
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
