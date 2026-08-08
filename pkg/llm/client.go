// Package llm provides a thin wrapper around Eino's ChatModel for LLM access.
//
// It creates a Gemini-native ChatModel instance using Google's genai SDK
// (not the OpenAI-compatible endpoint). The Gemini native API properly handles
// thought_signature in tool calls, which the OpenAI-compatible endpoint does not.
//
// The agent loop uses schema.Message and schema.ToolInfo directly.
package llm

import (
	"context"
	"errors"
	"time"

	"github.com/cloudwego/eino-ext/components/model/gemini"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"google.golang.org/genai"
)

// Options holds the configuration for creating an LLM client.
type Options struct {
	ApiKey  string        `yaml:"apiKey,omitempty" json:"apiKey,omitempty"`
	ModelId string        `yaml:"modelId,omitempty" json:"modelId,omitempty"`
	BaseUrl string        `yaml:"baseUrl,omitempty" json:"baseUrl,omitempty"`
	Timeout time.Duration `yaml:"timeout,omitempty" json:"timeout,omitempty"`

	// InputPricePerMillion / OutputPricePerMillion 每百万 token 价格（元），
	// 用于状态栏费用估算；为 0 时费用不展示。
	InputPricePerMillion  float64 `yaml:"inputPricePerMillion,omitempty" json:"inputPricePerMillion,omitempty"`
	OutputPricePerMillion float64 `yaml:"outputPricePerMillion,omitempty" json:"outputPricePerMillion,omitempty"`
	// ContextWindow 模型上下文窗口（tokens），用于计算上下文使用百分比。
	ContextWindow int `yaml:"contextWindow,omitempty" json:"contextWindow,omitempty"`
}

// Client wraps an Eino Gemini ChatModel.
type Client struct {
	chatModel *gemini.ChatModel
}

// NewClient creates a new LLM client backed by Eino's Gemini-native
// ChatModel provider. This uses the Google genai SDK directly, which
// properly handles thought_signature in tool call replay.
func NewClient(options *Options) (*Client, error) {
	if options == nil {
		return nil, errors.New("options is nil")
	}
	if options.ApiKey == "" || options.ModelId == "" {
		return nil, errors.New("apiKey and modelId must be specified")
	}

	ctx := context.Background()
	genaiClient, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey: options.ApiKey,
	})
	if err != nil {
		return nil, err
	}

	cm, err := gemini.NewChatModel(ctx, &gemini.Config{
		Client: genaiClient,
		Model:  options.ModelId,
		ThinkingConfig: &genai.ThinkingConfig{
			IncludeThoughts: true, // 开启 thinking，模型返回的推理过程会填充到 Message.ReasoningContent
		},
	})
	if err != nil {
		return nil, err
	}

	return &Client{chatModel: cm}, nil
}

// ChatModel returns the underlying Eino ChatModel for direct use.
// It implements model.ToolCallingChatModel (Generate + Stream + WithTools).
func (c *Client) ChatModel() model.ToolCallingChatModel {
	return c.chatModel
}

// Generate is a convenience wrapper for non-streaming generation without tools.
func (c *Client) Generate(ctx context.Context, messages []*schema.Message) (*schema.Message, error) {
	return c.chatModel.Generate(ctx, messages)
}
