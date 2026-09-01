package llm

import (
	"errors"
	"strings"
)

// ErrContextOverflow 标记"请求超出模型上下文窗口"这一类供应商错误。
// 宿主经 errors.Is 可靠识别（各家原始文案差异极大，见 contextOverflowMarkers）。
var ErrContextOverflow = errors.New("上下文超出模型窗口上限")

// contextOverflowMarkers 是各供应商"上下文超限"错误的判别子串（小写匹配）。
//
// 没有统一错误码可用，只能按文案识别。取舍：**宁可漏判也不误判**——
// 漏判退回原始错误（用户看到供应商原文，信息量低但不失真），误判则会把别的
// 故障错误地引导用户去"开新会话"，掩盖真实原因。故不用 "token" 这类过宽的词。
var contextOverflowMarkers = []string{
	"context_length_exceeded",             // OpenAI / DeepSeek / 多数兼容层错误码
	"maximum context length",              // OpenAI 文案
	"context length exceeded",             //
	"reduce the length of the messages",   // OpenAI 建议语
	"prompt is too long",                  // Anthropic
	"input token count",                   // Gemini
	"exceeds the maximum number of tokens", // Gemini / 部分兼容层
	"too many tokens",                     //
	"context window",                      //
}

// IsContextOverflow 报告 err 是否为"超出模型上下文窗口"。
func IsContextOverflow(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrContextOverflow) {
		return true
	}
	msg := strings.ToLower(err.Error())
	for _, m := range contextOverflowMarkers {
		if strings.Contains(msg, m) {
			return true
		}
	}
	return false
}
