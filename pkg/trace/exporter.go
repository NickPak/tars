package trace

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"time"

	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// fileExporter is a sdktrace.SpanExporter that appends each completed span
// as one JSON line to a per-conversation trace.jsonl file.
type fileExporter struct {
	mu  sync.Mutex
	f   *os.File
	enc *json.Encoder
}

func newFileExporter(path string) (*fileExporter, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, err
	}
	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	return &fileExporter{f: f, enc: enc}, nil
}

// spanRecord is the JSONL representation of one completed span.
type spanRecord struct {
	TraceID      string         `json:"traceId"`
	SpanID       string         `json:"spanId"`
	ParentSpanID string         `json:"parentSpanId,omitempty"`
	Name         string         `json:"name"`
	StartTime    time.Time      `json:"startTime"`
	EndTime      time.Time      `json:"endTime"`
	DurationMs   int64          `json:"durationMs"`
	Status       string         `json:"status"`
	StatusMsg    string         `json:"statusMessage,omitempty"`
	Attributes   map[string]any `json:"attributes,omitempty"`
}

// ExportSpans appends all completed spans to the JSONL file.
func (e *fileExporter) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, s := range spans {
		rec := spanRecord{
			TraceID:    s.SpanContext().TraceID().String(),
			SpanID:     s.SpanContext().SpanID().String(),
			Name:       s.Name(),
			StartTime:  s.StartTime(),
			EndTime:    s.EndTime(),
			DurationMs: s.EndTime().Sub(s.StartTime()).Milliseconds(),
		}
		if p := s.Parent(); p.IsValid() {
			rec.ParentSpanID = p.SpanID().String()
		}
		switch s.Status().Code {
		case codes.Ok:
			rec.Status = "ok"
		case codes.Error:
			rec.Status = "error"
			rec.StatusMsg = s.Status().Description
		default:
			rec.Status = "unset"
		}
		if attrs := s.Attributes(); len(attrs) > 0 {
			rec.Attributes = make(map[string]any, len(attrs))
			for _, kv := range attrs {
				rec.Attributes[string(kv.Key)] = kv.Value.AsInterface()
			}
		}
		if err := e.enc.Encode(rec); err != nil {
			return err
		}
	}
	return nil
}

// Shutdown closes the underlying file.
func (e *fileExporter) Shutdown(context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.f.Close()
}
