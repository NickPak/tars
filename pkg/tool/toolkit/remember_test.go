package toolkit

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"tars/pkg/ask"
	"tars/pkg/memory"
)

// mockMemoryProvider 实现 memory.Provider：记录 Remember 调用。
type mockMemoryProvider struct {
	remembered  *memory.RememberInput
	rememberErr error
	recallFacts []*memory.Fact
}

func (m *mockMemoryProvider) Remember(in *memory.RememberInput) (bool, error) {
	m.remembered = in
	if m.rememberErr != nil {
		return false, m.rememberErr
	}
	return true, nil
}

func (m *mockMemoryProvider) Recall(query string, limit int) ([]*memory.Fact, error) {
	return m.recallFacts, nil
}

// mockAsker 实现 ask.AskProvider：返回预设答复并记录问题。
type mockAsker struct {
	answer   string
	question string
}

func (m *mockAsker) Ask(_ context.Context, _ string, q *ask.Question) (*ask.Answer, error) {
	m.question = q.Question
	return &ask.Answer{Value: m.answer, Source: "user"}, nil
}

func callRemember(mp *mockMemoryProvider, asker *mockAsker, raw string) (string, error) {
	var a ask.AskProvider
	if asker != nil {
		a = asker
	}
	return NewRememberTool(mp, a).definition().Handler(context.Background(), json.RawMessage(raw))
}

func TestRemember_CreatePassesWhitelist(t *testing.T) {
	mp := &mockMemoryProvider{}
	out, err := callRemember(mp, nil, `{"type":"user","subject":"prefer-pnpm","body":"偏好 pnpm","scope":"global"}`)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if mp.remembered == nil || mp.remembered.Subject != "prefer-pnpm" || mp.remembered.Scope != memory.ScopeGlobal {
		t.Fatalf("remember input mismatch: %+v", mp.remembered)
	}
	if !strings.Contains(out, "remembered") {
		t.Errorf("create message mismatch: %q", out)
	}
}

func TestRemember_OverwriteRequiresConfirm(t *testing.T) {
	mp := &mockMemoryProvider{}
	asker := &mockAsker{answer: "confirm"}
	if _, err := callRemember(mp, asker, `{"type":"project","subject":"use-gorm","body":"v2","overwrite":true}`); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if !strings.Contains(asker.question, "use-gorm") {
		t.Errorf("overwrite must ask the user about the subject: %q", asker.question)
	}
	if mp.remembered == nil || !mp.remembered.Overwrite {
		t.Error("confirmed overwrite must reach the provider with Overwrite=true")
	}
}

func TestRemember_OverwriteDenied(t *testing.T) {
	mp := &mockMemoryProvider{}
	asker := &mockAsker{answer: "deny"}
	out, err := callRemember(mp, asker, `{"type":"project","subject":"use-gorm","body":"v2","overwrite":true}`)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if mp.remembered != nil {
		t.Error("denied overwrite must not reach the provider")
	}
	if !strings.Contains(out, "declined") {
		t.Errorf("denial should be reported to the model: %q", out)
	}
}

func TestRemember_StoreErrorPropagates(t *testing.T) {
	mp := &mockMemoryProvider{rememberErr: errors.New("memory: body contains sensitive pattern")}
	if _, err := callRemember(mp, nil, `{"type":"user","subject":"s","body":"密码是 x"}`); err == nil {
		t.Error("store validation error must propagate to the model")
	}
}

func TestRecall_FormatsMatches(t *testing.T) {
	mp := &mockMemoryProvider{recallFacts: []*memory.Fact{
		{Type: memory.FactUser, Subject: "prefer-pnpm", Body: "偏好 pnpm", LastConfirmed: "2026-09-04", WrittenBy: "qwen"},
	}}
	out, err := NewRecallTool(mp).definition().Handler(context.Background(),
		json.RawMessage(`{"query":"pnpm"}`))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if !strings.Contains(out, "prefer-pnpm") || !strings.Contains(out, "偏好 pnpm") {
		t.Errorf("recall output mismatch: %q", out)
	}

	mpEmpty := &mockMemoryProvider{}
	out, _ = NewRecallTool(mpEmpty).definition().Handler(context.Background(),
		json.RawMessage(`{"query":"nothing"}`))
	if !strings.Contains(out, "no memories matched") {
		t.Errorf("empty result message mismatch: %q", out)
	}
}
