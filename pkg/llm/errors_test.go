package llm

import (
	"errors"
	"fmt"
	"testing"
)

// 各供应商的真实错误文案都应被识别（没有统一错误码，只能按文案）。
func TestIsContextOverflowDialects(t *testing.T) {
	cases := []struct{ name, msg string }{
		{"openai", "This model's maximum context length is 128000 tokens. However, your messages resulted in 131204 tokens. Please reduce the length of the messages."},
		{"openai code", `{"error":{"code":"context_length_exceeded","type":"invalid_request_error"}}`},
		{"anthropic", "prompt is too long: 203458 tokens > 199999 maximum"},
		{"gemini", "The input token count (1052341) exceeds the maximum number of tokens allowed (1048576)."},
		{"deepseek", "This model's maximum context length is 65536 tokens"},
		{"generic window", "request exceeds the model's context window"},
	}
	for _, c := range cases {
		if !IsContextOverflow(errors.New(c.msg)) {
			t.Errorf("%s: should be recognized as context overflow: %s", c.name, c.msg)
		}
		// 经 %w 包装多层后仍可识别（实际链路上会被 agent 包装）
		wrapped := fmt.Errorf("model stream failed at iteration 7: %w", errors.New(c.msg))
		if !IsContextOverflow(wrapped) {
			t.Errorf("%s: wrapped error should still be recognized", c.name)
		}
	}
}

// 宁可漏判不可误判：别的故障不得被引导到"开新会话"，那会掩盖真实原因。
func TestIsContextOverflowNoFalsePositives(t *testing.T) {
	for _, msg := range []string{
		"connection refused",
		"401 Unauthorized: invalid api key",
		"rate limit exceeded, please retry after 20s",
		"model not found: gpt-4o-mini",
		"completion tokens limit reached",       // 输出长度，非输入超窗
		"max_tokens must be less than 4096",    // 参数校验
		"tool call arguments exceeded 10 items",
		"context deadline exceeded",            // 超时——最危险的误判源
		"context canceled",
	} {
		if IsContextOverflow(errors.New(msg)) {
			t.Errorf("false positive on: %s", msg)
		}
	}
}

func TestIsContextOverflowNilAndSentinel(t *testing.T) {
	if IsContextOverflow(nil) {
		t.Error("nil must not be an overflow")
	}
	if !IsContextOverflow(ErrContextOverflow) {
		t.Error("the sentinel itself must be recognized")
	}
	if !IsContextOverflow(fmt.Errorf("wrapped: %w", ErrContextOverflow)) {
		t.Error("wrapped sentinel must be recognized")
	}
}
