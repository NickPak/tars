package session

import (
	"context"
	"io"
	"strings"
	"testing"

	"tars/pkg/llm"
	"tars/pkg/schema"
)

type fakeStream struct {
	msg *schema.Message
	err error
}

func (s *fakeStream) Recv() (*schema.Message, error) { return nil, io.EOF }
func (s *fakeStream) Final() (*schema.Message, error) { return s.msg, s.err }
func (s *fakeStream) Close() error                    { return nil }

type fakeProvider struct {
	content string
	gotReq  *llm.ChatRequest
}

func (p *fakeProvider) Stream(_ context.Context, req *llm.ChatRequest) (llm.Stream, error) {
	p.gotReq = req
	return &fakeStream{msg: &schema.Message{Role: schema.RoleAssistant, Content: p.content}}, nil
}

const sampleEntriesJSON = `[{"range":"turn_1-1","goal":"g","actions":["read_file×1"],"result":"ok","artifacts":["a.go"],"identifiers":["abc1234"],"pointer":"archive/turn_1-1.md"}]`

func TestLLMExtractorRequestShape(t *testing.T) {
	prefix := mkTrajectory(2)
	p := &fakeProvider{content: sampleEntriesJSON}
	req := &ExtractRequest{
		Prefix: prefix, Batch: prefix[:3],
		Range: "turn_1-1", Pointer: "archive/turn_1-1.md",
	}
	entries, err := LLMExtractor{}.Extract(context.Background(), p, req)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(entries) != 1 || entries[0].Pointer != "archive/turn_1-1.md" {
		t.Fatalf("entries = %+v", entries)
	}

	// 追加式构造：消息 = 前缀 + 1 条指令；不带工具
	if len(p.gotReq.Messages) != len(prefix)+1 {
		t.Fatalf("messages = %d, want %d", len(p.gotReq.Messages), len(prefix)+1)
	}
	last := p.gotReq.Messages[len(p.gotReq.Messages)-1]
	if last.Role != schema.RoleUser {
		t.Fatalf("instruction role = %s", last.Role)
	}
	if !strings.Contains(last.Content, "turn_1-1") || !strings.Contains(last.Content, "archive/turn_1-1.md") {
		t.Fatal("instruction missing range/pointer")
	}
	if len(p.gotReq.Tools) != 0 {
		t.Fatal("extraction call must not bind tools")
	}
}

func TestParseEntriesTolerant(t *testing.T) {
	content := "Sure, here you go:\n```json\n[{\"range\":\"turn_1-2\",\"goal\":\"g\",\"result\":\"ok\"}]\n```\nDone."
	entries, err := parseEntries(content)
	if err != nil || len(entries) != 1 {
		t.Fatalf("entries=%v err=%v", entries, err)
	}
}

func TestParseEntriesInvalid(t *testing.T) {
	if _, err := parseEntries("no json here"); err == nil {
		t.Fatal("want parse error")
	}
	if _, err := parseEntries("[]"); err == nil {
		t.Fatal("want empty-array error")
	}
}

func TestLLMExtractorNilProvider(t *testing.T) {
	if _, err := (LLMExtractor{}).Extract(context.Background(), nil, &ExtractRequest{Range: "turn_1-1"}); err == nil {
		t.Fatal("want error for nil provider")
	}
}
