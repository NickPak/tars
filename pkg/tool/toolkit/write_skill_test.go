package toolkit

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"tars/pkg/skill"
)

// write_skill 测试复用 load_skill_test.go 的 mockSkillRuntime（合并后的
// skill.SkillProvider 接口读写一体，mock 也随之合并）。

func callWriteSkill(rt skill.Provider, raw string) (string, error) {
	return NewSkillWriterTool(rt).definition().Handler(context.Background(), json.RawMessage(raw))
}

func TestWriteSkill_CreateMessageMentionsDisabled(t *testing.T) {
	rt := newMockSkillRuntime()
	rt.writeCreated = true
	out, err := callWriteSkill(rt, `{"name":"deploy-app","description":"d","content":"# Deploy"}`)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if !rt.writeCalled || rt.gotWriteName != "deploy-app" {
		t.Fatalf("writer not called correctly: %+v", rt)
	}
	if !strings.Contains(out, "DISABLED") {
		t.Errorf("create message must tell the model the skill is disabled by default: %q", out)
	}
}

func TestWriteSkill_OverwritePassthrough(t *testing.T) {
	rt := newMockSkillRuntime()
	rt.writeCreated = false
	out, err := callWriteSkill(rt, `{"name":"deploy-app","description":"d","content":"# v2","overwrite":true}`)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if !rt.gotOverwrite {
		t.Error("overwrite flag must be passed through")
	}
	if !strings.Contains(out, "overwritten") {
		t.Errorf("overwrite message mismatch: %q", out)
	}
}

func TestWriteSkill_WriterErrorPropagates(t *testing.T) {
	rt := newMockSkillRuntime()
	rt.writeErr = errors.New("skills: invalid name")
	if _, err := callWriteSkill(rt, `{"name":"BAD","description":"d","content":"x"}`); err == nil {
		t.Error("writer error must propagate to the model")
	}
}

func TestWriteSkill_InvalidArgs(t *testing.T) {
	rt := newMockSkillRuntime()
	if _, err := callWriteSkill(rt, `not-json`); err == nil {
		t.Error("expected error for invalid JSON")
	}
	if rt.writeCalled {
		t.Error("writer must not be called on invalid args")
	}
}

func TestWriteSkill_NoRuntime(t *testing.T) {
	if _, err := callWriteSkill(nil, `{"name":"x","description":"d","content":"c"}`); err == nil {
		t.Error("expected error without a skill runtime")
	}
}
