package memory

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"tars/pkg/schema"
)

type fakeWorkspace struct {
	dir      string
	recalled map[string]struct{}
}

func (f *fakeWorkspace) GetWorkspaceDir() string { return f.dir }

func (f *fakeWorkspace) MarkMemoryRecalled(subjects ...string) {
	if f.recalled == nil {
		f.recalled = make(map[string]struct{})
	}
	for _, s := range subjects {
		f.recalled[s] = struct{}{}
	}
}

func (f *fakeWorkspace) GetRecalledMemory() []string {
	out := make([]string, 0, len(f.recalled))
	for s := range f.recalled {
		out = append(out, s)
	}
	slices.Sort(out)
	return out
}

// newTestRuntime 创建仅项目指令记忆形态的 Runtime（无事实记忆——
// mgr 为 nil 时 enabled() 恒 false，user_memory 块省略）。
func newTestRuntime(ws StateProvider) *Runtime {
	return &Runtime{ws: ws, maxBytes: DefaultMaxBytes}
}

func writeAgents(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, AgentsFile), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestRenderMemoryBlock_MissingOrEmpty(t *testing.T) {
	rt := newTestRuntime(&fakeWorkspace{dir: t.TempDir()})
	if msg := rt.RenderMemoryBlock(); msg != nil {
		t.Errorf("missing AGENTS.md should yield nil, got %v", msg)
	}

	dir := t.TempDir()
	writeAgents(t, dir, "  \n\n")
	if msg := newTestRuntime(&fakeWorkspace{dir: dir}).RenderMemoryBlock(); msg != nil {
		t.Error("blank AGENTS.md should yield nil")
	}
}

func TestRenderMemoryBlock_Content(t *testing.T) {
	dir := t.TempDir()
	writeAgents(t, dir, "# Rules\n\nuse pnpm\n")
	msg := newTestRuntime(&fakeWorkspace{dir: dir}).RenderMemoryBlock()
	if msg == nil {
		t.Fatal("expected memory block")
	}
	if msg.Role != schema.RoleUser {
		t.Errorf("memory block must be a user-role message (low-authority position), got %s", msg.Role)
	}
	if !strings.Contains(msg.Content, `<agents_md source="AGENTS.md">`) ||
		!strings.Contains(msg.Content, "use pnpm") {
		t.Errorf("unexpected content: %q", msg.Content)
	}
}

// 工作区切换（SetWorkspaceDir 零消息窗口）：每轮现读，下一轮即生效，
// 无需任何失效通知。
func TestRenderMemoryBlock_WorkspaceSwitch(t *testing.T) {
	ws := &fakeWorkspace{dir: t.TempDir()}
	rt := newTestRuntime(ws)
	if rt.RenderMemoryBlock() != nil {
		t.Fatal("no AGENTS.md yet")
	}
	dir2 := t.TempDir()
	writeAgents(t, dir2, "rules v2")
	ws.dir = dir2
	msg := rt.RenderMemoryBlock()
	if msg == nil || !strings.Contains(msg.Content, "rules v2") {
		t.Error("workspace switch should be picked up on next render")
	}
}

// newTestManagerRuntime 创建带事实记忆的 Runtime（全局/项目根都在临时目录）。
func newTestManagerRuntime(t *testing.T, ws StateProvider) (*Runtime, *Manager) {
	t.Helper()
	workDir := t.TempDir()
	mgr := NewManager(workDir, NewConfig())
	if err := mgr.Startup(); err != nil {
		t.Fatal(err)
	}
	rt := mgr.NewRuntime(ws, t.TempDir(), func() string { return "test-model" })
	return rt, mgr
}

// user_memory 块：remember 写入后索引块出现（含低权威声明与全局/项目分区）；
// 配置禁用则省略。
func TestRenderUserMemoryBlock(t *testing.T) {
	rt, mgr := newTestManagerRuntime(t, &fakeWorkspace{dir: t.TempDir()})

	// 无事实：块省略（AGENTS.md 也不存在 → 整条 nil）
	if msg := rt.RenderMemoryBlock(); msg != nil {
		t.Fatal("no facts and no AGENTS.md should yield nil")
	}

	if _, err := rt.Remember(&RememberInput{Type: FactUser, Subject: "prefer-pnpm", Body: "偏好 pnpm", Scope: ScopeGlobal}); err != nil {
		t.Fatalf("remember: %v", err)
	}
	if _, err := rt.Remember(&RememberInput{Type: FactProject, Subject: "use-gorm", Body: "项目用 GORM", Scope: ScopeProject}); err != nil {
		t.Fatalf("remember: %v", err)
	}

	msg := rt.RenderMemoryBlock()
	if msg == nil {
		t.Fatal("memory block should appear after remember")
	}
	for _, want := range []string{"<memory", "may be outdated", "<global>", "prefer-pnpm", `<project priority="overrides-global">`, "use-gorm"} {
		if !strings.Contains(msg.Content, want) {
			t.Errorf("user_memory block missing %q:\n%s", want, msg.Content)
		}
	}

	// written_by 溯源
	facts, _ := ListFacts(mgr.GetGlobalMemoryDir())
	if len(facts) != 1 || facts[0].WrittenBy != "test-model" {
		t.Errorf("written_by should record the model: %+v", facts[0])
	}

	// 禁用：块省略
	if err := mgr.UpdateConfig(&Config{Enabled: false, MaxIndexBytes: DefaultMaxIndexBytes}); err != nil {
		t.Fatal(err)
	}
	if msg := rt.RenderMemoryBlock(); msg != nil {
		t.Error("disabled memory must not render the memory block")
	}
	if _, err := rt.Remember(&RememberInput{Type: FactUser, Subject: "x", Body: "y"}); err == nil {
		t.Error("disabled memory must reject remember")
	}
}

// Suggest：持久性标记过滤（高精度）+ 候选落盘；禁用则跳过。
func TestRuntimeSuggest(t *testing.T) {
	rt, mgr := newTestManagerRuntime(t, &fakeWorkspace{dir: t.TempDir()})

	rt.Suggest([]CandidateItem{
		{Type: FactFeedback, Text: "以后都用 pnpm 安装依赖", Pointer: "archive://turn_1-2.md"},
		{Type: FactFeedback, Text: "帮我修一下这个超时问题", Pointer: "archive://turn_1-2.md"}, // 临时任务，无标记
		{Type: FactFeedback, Text: "remember to always run gofmt before commit", Pointer: "archive://turn_1-2.md"},
	})

	cands, err := ListCandidates(mgr.GetProjectMemoryDir(rt.projectDir))
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 2 {
		t.Fatalf("durable-marker filter should keep 2 of 3: %+v", cands)
	}
	for _, c := range cands {
		if strings.Contains(c.Body, "超时") {
			t.Error("ephemeral task text must be filtered out")
		}
		if c.Source != "archive://turn_1-2.md" {
			t.Errorf("candidate should carry the archive pointer: %q", c.Source)
		}
		if c.WrittenBy != "test-model" {
			t.Errorf("written_by should record the model: %q", c.WrittenBy)
		}
	}

	// 禁用：不落候选
	if err := mgr.UpdateConfig(&Config{Enabled: false, MaxIndexBytes: DefaultMaxIndexBytes}); err != nil {
		t.Fatal(err)
	}
	rt.Suggest([]CandidateItem{{Type: FactFeedback, Text: "以后都别用 sudo"}})
	cands, _ = ListCandidates(mgr.GetProjectMemoryDir(rt.projectDir))
	if len(cands) != 2 {
		t.Error("disabled memory must not accept candidates")
	}
}

// slugify：中英文保留、符号折叠、长度截断。
func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"以后都用 pnpm 安装依赖":            "以后都用-pnpm-安装依赖",
		"remember to always run gofmt!":   "remember-to-always-run-gofmt",
		"!!!":                             "",
		"prefer dark-mode over light":     "prefer-dark-mode-over-light",
	}
	for in, want := range cases {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

// 全局 AGENTS.md（P4）：两级同存标注来源 + 项目级优先声明；单级退化为原文。
func TestRenderProjectMemory_GlobalAndProject(t *testing.T) {
	rt, mgr := newTestManagerRuntime(t, &fakeWorkspace{dir: t.TempDir()})
	rt.maxBytes = DefaultMaxBytes

	// 仅用户级
	if err := os.WriteFile(mgr.GlobalAgentsFile(), []byte("全局规矩"), 0644); err != nil {
		t.Fatal(err)
	}
	msg := rt.RenderMemoryBlock()
	if msg == nil || !strings.Contains(msg.Content, "全局规矩") || strings.Contains(msg.Content, "## User level") {
		t.Fatalf("global-only should render plain content: %v", msg)
	}

	// 两级同存：标注 + 优先声明
	writeAgents(t, rt.ws.GetWorkspaceDir(), "项目规矩")
	msg = rt.RenderMemoryBlock()
	for _, want := range []string{"## User level", "全局规矩", "## Project level", "项目规矩", "takes precedence"} {
		if !strings.Contains(msg.Content, want) {
			t.Errorf("merged block missing %q:\n%s", want, msg.Content)
		}
	}
}

// 遮蔽规则（P4）：全局同名项不进注入块。
func TestRenderUserMemory_Shadowing(t *testing.T) {
	rt, _ := newTestManagerRuntime(t, &fakeWorkspace{dir: t.TempDir()})
	if _, err := rt.Remember(&RememberInput{Scope: ScopeGlobal, Type: FactUser, Subject: "editor", Body: "全局用 vscode"}); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Remember(&RememberInput{Scope: ScopeProject, Type: FactUser, Subject: "editor", Body: "项目里用 vim"}); err != nil {
		t.Fatal(err)
	}
	content := rt.RenderMemoryBlock().Content
	if strings.Contains(content, "全局用 vscode") {
		t.Error("shadowed global fact must not appear in the injected block")
	}
	if !strings.Contains(content, "项目里用 vim") {
		t.Error("project fact should appear")
	}
}

// recall 追踪（P4）：命中记入会话集合，状态栏区块渲染。
func TestRecallTrackingAndStatus(t *testing.T) {
	rt, _ := newTestManagerRuntime(t, &fakeWorkspace{dir: t.TempDir()})
	if _, err := rt.Remember(&RememberInput{Scope: ScopeProject, Type: FactFeedback, Subject: "use-pnpm", Body: "以后都用 pnpm 安装依赖"}); err != nil {
		t.Fatal(err)
	}

	if got := rt.RenderStatus(0); got != "" {
		t.Error("no recall yet: status section should be empty")
	}
	hits, err := rt.Recall("pnpm", 5)
	if err != nil || len(hits) != 1 {
		t.Fatalf("recall: %v, %+v", err, hits)
	}
	ws := rt.ws.(*fakeWorkspace)
	if !slices.Contains(ws.GetRecalledMemory(), "use-pnpm") {
		t.Errorf("recalled subject should be tracked: %v", ws.GetRecalledMemory())
	}
	if got := rt.RenderStatus(0); !strings.Contains(got, `<memory recalled="use-pnpm"/>`) {
		t.Errorf("status section should render recalled memory: %q", got)
	}
}

// Recall：项目级 + 全局都命中，过期项排除。
func TestRuntimeRecall(t *testing.T) {
	rt, mgr := newTestManagerRuntime(t, &fakeWorkspace{dir: t.TempDir()})
	if _, err := rt.Remember(&RememberInput{Type: FactUser, Subject: "prefer-pnpm", Body: "偏好 pnpm", Scope: ScopeGlobal}); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Remember(&RememberInput{Type: FactProject, Subject: "use-gorm", Body: "项目用 GORM", Scope: ScopeProject}); err != nil {
		t.Fatal(err)
	}

	hits, err := rt.Recall("pnpm", 5)
	if err != nil || len(hits) != 1 || hits[0].Subject != "prefer-pnpm" {
		t.Fatalf("recall: %v, %+v", err, hits)
	}
	if hits, err := rt.Recall("gorm", 5); err != nil || len(hits) != 1 {
		t.Fatalf("project-scope recall: %v, %d", err, len(hits))
	}

	// 过期项排除：直接写入一条过期事实
	if _, err := WriteFact(mgr.GetProjectMemoryDir(rt.projectDir), &Fact{Type: FactProject, Subject: "old-gorm", Body: "gorm 旧事", ExpiresAt: "2020-01-01"}, false); err != nil {
		t.Fatal(err)
	}
	hits, _ = rt.Recall("gorm", 5)
	if len(hits) != 1 {
		t.Error("expired facts must be excluded from recall")
	}
}

func TestRenderMemoryBlock_Truncation(t *testing.T) {
	dir := t.TempDir()
	// 多字节字符填充超上限：验证 UTF-8 安全截断（不切断 rune）
	writeAgents(t, dir, strings.Repeat("规", DefaultMaxBytes))
	msg := newTestRuntime(&fakeWorkspace{dir: dir}).RenderMemoryBlock()
	if msg == nil || !strings.Contains(msg.Content, "truncated") {
		t.Fatal("oversized AGENTS.md should be truncated with read_file pointer")
	}
	if !utf8.ValidString(msg.Content) {
		t.Error("truncated content must stay valid UTF-8")
	}
}
