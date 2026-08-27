package compaction

import (
	"strings"
	"testing"

	"tars/pkg/schema"
)

func TestMessageDeterministic(t *testing.T) {
	c := &Compaction{Entries: []*ArchiveEntry{{
		Range: "turn_1-3", Goal: "g", Actions: []string{"a"}, Result: "ok",
		Artifacts: []string{"f.go"}, Identifiers: []string{"abc123"}, Pointer: "archive/turn_1-3.md",
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

func TestMessageNilEmpty(t *testing.T) {
	var nilC *Compaction
	if nilC.Message() != nil {
		t.Fatal("nil compaction should render nil")
	}
	if (&Compaction{}).Message() != nil {
		t.Fatal("empty entries should render nil")
	}
}
