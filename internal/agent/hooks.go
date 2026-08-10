package agent

import (
	"context"
	"encoding/json"
	"tars/pkg/tools"
	"time"

	"github.com/cloudwego/eino/schema"
)

// Hooks receives loop events. All methods are optional (nil-safe via the
// nopHooks fallback) and are called synchronously in the loop goroutine,
// except ToolResult which may arrive concurrently from tool goroutines.
//
// The event order within one iteration is:
//
//	IterationStart
//	StreamChunk × N          (as the model streams its reply)
//	[ToolsStart              (only if the reply contains tool calls)
//	 ToolResult × N          (one per tool, as each finishes)
//	 ToolsEnd]               (after all tools of this round complete)
//	IterationEnd             (full history + this round's delta)
type Hooks interface {
	// IterationStart fires before each LLM call. iteration is 1-based.
	// messages is the full input context that will be sent to the model
	// (system + history + status bar) — hosts use it for tracing.
	IterationStart(ctx context.Context, iteration int, messages []*schema.Message)
	// IterationEnd fires after one full iteration completes — LLM call plus
	// any tool executions. full is the complete message history including
	// this round; delta contains only the messages produced in this round
	// (the assistant message, plus tool result messages if any).
	// Hosts typically persist delta here; it fires every round, including
	// the final plain-text round.
	IterationEnd(ctx context.Context, iteration int, full []*schema.Message, delta []*schema.Message)
	// StreamChunk fires for each streamed content/reasoning chunk of an iteration.
	StreamChunk(ctx context.Context, iteration int, chunk *schema.Message)
	// ToolsStart fires once before a batch of tool calls is executed.
	ToolsStart(ctx context.Context, calls []schema.ToolCall)
	// ToolResult fires as each tool finishes (may arrive concurrently if the
	// executor runs tools in parallel).
	ToolResult(ctx context.Context, result tools.ToolResult)
	// ToolsEnd fires after all tool calls of this round have completed,
	// carrying the results in input order.
	ToolsEnd(ctx context.Context, results []tools.ToolResult)
	// OnError fires when a model call fails (provider error, iteration
	// timeout, ...). iteration is the 1-based ReAct round; attempt is the
	// 1-based failed-attempt count within that round. streamedChunks reports
	// how many chunks had already been delivered via StreamChunk before the
	// failure — when > 0, the host has already partially presented this
	// round's output, so an automatic retry would duplicate content (the
	// host should abort, or roll back its UI first).
	//
	// Return retry=true to attempt the model call again after delay;
	// retry=false aborts the loop with this error.
	//
	// User cancellation never reaches OnError: when the parent ctx is done,
	// the loop returns immediately without consulting the host.
	OnError(ctx context.Context, iteration, attempt, streamedChunks int, err error) (retry bool, delay time.Duration)
}

// nopHooks is the no-op Hooks implementation used when the caller passes nil.
type nopHooks struct{}

func (nopHooks) IterationStart(context.Context, int, []*schema.Message)                  {}
func (nopHooks) IterationEnd(context.Context, int, []*schema.Message, []*schema.Message) {}
func (nopHooks) StreamChunk(context.Context, int, *schema.Message)                       {}
func (nopHooks) ToolsStart(context.Context, []schema.ToolCall)                           {}
func (nopHooks) ToolResult(context.Context, tools.ToolResult)                            {}
func (nopHooks) ToolsEnd(context.Context, []tools.ToolResult)                            {}

// OnError default: never retry (fail fast).
func (nopHooks) OnError(context.Context, int, int, int, error) (bool, time.Duration) { return false, 0 }

// MarshalMessages is a helper for hooks that need to serialize messages
// (e.g. for jsonl persistence). Kept here so hosts don't repeat it.
func MarshalMessages(msgs []*schema.Message) ([][]byte, error) {
	lines := make([][]byte, 0, len(msgs))
	for _, m := range msgs {
		b, err := json.Marshal(m)
		if err != nil {
			return nil, err
		}
		lines = append(lines, b)
	}
	return lines, nil
}
