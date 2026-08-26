package guard

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"tars/pkg/tool/kernel"
	"testing"

	"tars/pkg/ask"
	"tars/pkg/event"
	"tars/pkg/schema"
)

func TestClassify_ByLevel(t *testing.T) {
	call := schema.ToolCall{ID: "tc-1", Name: "mcp__srv__tool", Args: `{"q":"x"}`}

	low := &kernel.Definition{Name: call.Name, Risk: kernel.RiskLevelLow}
	if req := Classify(low, call); req != nil {
		t.Errorf("low risk declaration should not be gated")
	}

	for _, lv := range []kernel.RiskLevel{kernel.RiskLevelMedium, kernel.RiskLevelHigh} {
		def := &kernel.Definition{Name: call.Name, Risk: lv}
		req := Classify(def, call)
		if req == nil {
			t.Fatalf("%s declaration should be gated", lv)
		}
		if req.RiskKey != call.Name {
			t.Errorf("by-level RiskKey = %q, want tool name", req.RiskKey)
		}
		if !strings.Contains(req.Reason, string(lv)) {
			t.Errorf("reason should mention level: %q", req.Reason)
		}
	}
}

func TestClassify_ByLevelSummaryTruncated(t *testing.T) {
	long := strings.Repeat("x", 400)
	def := &kernel.Definition{Name: "mcp__a__b", Risk: kernel.RiskLevelMedium}
	req := Classify(def, schema.ToolCall{ID: "1", Name: def.Name, Args: long})
	if req == nil {
		t.Fatal("expected approval request")
	}
	if len(req.Summary) >= len(long) {
		t.Error("summary should be truncated")
	}
}

// 声明优先于规则：medium 声明 + 规则同时存在时按级别拦截（RiskKey 无规则后缀）。
func TestClassify_DeclarationBeatsRules(t *testing.T) {
	def := &kernel.Definition{
		Name: "t",
		Risk: kernel.RiskLevelMedium,
		RiskRules: []kernel.RiskRule{
			{ID: "r1", Reason: "x", ArgsKey: "command", Pattern: regexp.MustCompile(`danger`)},
		},
	}
	req := Classify(def, schema.ToolCall{ID: "1", Name: "t", Args: `{"command":"danger"}`})
	if req == nil || req.RiskKey != "t" {
		t.Errorf("declaration should win, got %+v", req)
	}
}

func TestClassify_RulesEngine(t *testing.T) {
	def := &kernel.Definition{
		Name: "t",
		RiskRules: []kernel.RiskRule{
			{ID: "r1", Reason: "hit r1", ArgsKey: "command", Pattern: regexp.MustCompile(`danger`)},
		},
	}

	req := Classify(def, schema.ToolCall{ID: "1", Name: "t", Args: `{"command":"run danger now"}`})
	if req == nil {
		t.Fatal("pattern hit should be gated")
	}
	if req.RiskKey != "t:r1" || req.Reason != "hit r1" {
		t.Errorf("unexpected request: %+v", req)
	}
	if req.TimeoutSeconds != ask.DefaultAskTimeout {
		t.Errorf("timeout = %d, want %d", req.TimeoutSeconds, ask.DefaultAskTimeout)
	}

	if got := Classify(def, schema.ToolCall{ID: "1", Name: "t", Args: `{"command":"safe"}`}); got != nil {
		t.Errorf("safe input should pass, got %+v", got)
	}
	// 目标字段缺失/非字符串/JSON 非法：不按危险拦
	for _, args := range []string{`{}`, `{"command":123}`, `not-json`} {
		if got := Classify(def, schema.ToolCall{ID: "1", Name: "t", Args: args}); got != nil {
			t.Errorf("args %s should pass, got %+v", args, got)
		}
	}
}

// fakeApprover 记录审批请求并按预设答复。
type fakeApprover struct {
	answer *ask.Answer
	err    error
	seen   []*ask.ApprovalRequest
}

func (f *fakeApprover) Approve(_ context.Context, _ event.Sink, _ string, ar *ask.ApprovalRequest) (*ask.Answer, error) {
	f.seen = append(f.seen, ar)
	return f.answer, f.err
}

func gatedDef() *kernel.Definition {
	return &kernel.Definition{
		Name: "t",
		RiskRules: []kernel.RiskRule{
			{ID: "r1", Reason: "hit", ArgsKey: "command", Pattern: regexp.MustCompile(`danger`)},
		},
	}
}

func dangerCall() schema.ToolCall {
	return schema.ToolCall{ID: "tc-1", Name: "t", Args: `{"command":"danger"}`}
}

func TestGate_SafeCallPassesWithoutApproval(t *testing.T) {
	ap := &fakeApprover{answer: &ask.Answer{Value: "deny"}}
	g := NewGate(ap, nil, nil, "s1")
	dec, err := g.Check(context.Background(), gatedDef(), schema.ToolCall{ID: "1", Name: "t", Args: `{"command":"safe"}`})
	if err != nil || !dec.Allow {
		t.Errorf("safe call should pass: dec=%+v err=%v", dec, err)
	}
	if len(ap.seen) != 0 {
		t.Error("safe call must not trigger approval")
	}
}

func TestGate_DenyFlowsThrough(t *testing.T) {
	ap := &fakeApprover{answer: &ask.Answer{Value: "deny", Reason: "no way"}}
	g := NewGate(ap, nil, nil, "s1")
	dec, err := g.Check(context.Background(), gatedDef(), dangerCall())
	if err != nil {
		t.Fatal(err)
	}
	if dec.Allow {
		t.Fatal("deny should block")
	}
	var out map[string]any
	if json.Unmarshal([]byte(dec.Output), &out) != nil || out["approved"] != false || out["reason"] != "no way" {
		t.Errorf("unexpected deny output: %s", dec.Output)
	}
}

func TestGate_NoApproverDeniesByDefault(t *testing.T) {
	g := NewGate(nil, nil, nil, "s1")
	dec, err := g.Check(context.Background(), gatedDef(), dangerCall())
	if err != nil {
		t.Fatal(err)
	}
	if dec.Allow {
		t.Error("no approver must deny dangerous calls")
	}
}

func TestGate_AllowAlwaysCached(t *testing.T) {
	ap := &fakeApprover{answer: &ask.Answer{Value: "allow_always"}}
	g := NewGate(ap, nil, nil, "s1")

	if dec, _ := g.Check(context.Background(), gatedDef(), dangerCall()); !dec.Allow {
		t.Fatal("allow_always should pass")
	}
	if len(ap.seen) != 1 {
		t.Fatalf("first call should ask once, asked %d", len(ap.seen))
	}
	// 第二次同类调用：常允许表命中，不再询问
	if dec, _ := g.Check(context.Background(), gatedDef(), dangerCall()); !dec.Allow {
		t.Fatal("cached allow should pass")
	}
	if len(ap.seen) != 1 {
		t.Error("cached allow must not re-ask")
	}
}

func TestRiskTable(t *testing.T) {
	tab := NewRiskTable()
	if tab.Allowed("k") {
		t.Error("empty table must not allow")
	}
	tab.Allow("k")
	if !tab.Allowed("k") {
		t.Error("Allow should be recorded")
	}
}
