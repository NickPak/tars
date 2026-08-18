package llm

import (
	"context"
	"encoding/json"

	"tars/pkg/schema"

	"github.com/cloudwego/eino/components/model"
	eino "github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"
)

// ChatRequest 是一次模型调用的完整输入（领域类型，system 消息已在
// Messages 头部——对模型而言 system 只是普通角色消息）。
type ChatRequest struct {
	Messages []*schema.Message
	Tools    []*schema.ToolSchema
}

// Provider 是模型提供者契约（流式）。接真实模型时实现 Stream 一个方法即可。
type Provider interface {
	Stream(ctx context.Context, req *ChatRequest) (Stream, error)
}

// Stream 是一次流式调用的增量帧序列。
type Stream interface {
	// Recv 返回下一增量帧（内容字段增量）；io.EOF 表示流结束。
	Recv() (*schema.Message, error)
	// Final 在流结束后调用，返回拼接好的完整消息（含 Usage）。
	// 空流返回 (nil, nil)。
	Final() (*schema.Message, error)
	// Close 关闭流。
	Close() error
}

// NewProvider 把 eino 模型适配为 Provider：工具描述在此完成绑定（WithTools）。
// entryID 是本次调用对应的配置条目 ID（费用核算用）：provider 的响应
// 里没有这个概念（它是本地配置的），由适配层在产出 Usage 时标注——
// agent 与 Controller 都无需再回填。
func NewProvider(m model.ToolCallingChatModel, tools []*schema.ToolSchema, entryID string) (Provider, error) {
	bound, err := m.WithTools(toEinoToolInfos(tools))
	if err != nil {
		return nil, err
	}
	return &einoProvider{model: bound, entryID: entryID}, nil
}

type einoProvider struct {
	model       model.ToolCallingChatModel
	entryID  string
}

func (p *einoProvider) Stream(ctx context.Context, req *ChatRequest) (Stream, error) {
	msgs := make([]*eino.Message, 0, len(req.Messages))
	for _, m := range req.Messages {
		msgs = append(msgs, ToEinoMessage(m))
	}
	sr, err := p.model.Stream(ctx, msgs)
	if err != nil {
		return nil, err
	}
	return &einoStream{sr: sr, entryID: p.entryID}, nil
}

// einoStream 把 eino 增量帧流适配为领域帧流：Recv 实时转发转换，
// Final 用 eino.ConcatMessages 完成拼接（tool call 分片按 index 合并，
// 语义与 eino 一致），Usage 取最后一帧非空的 ResponseMeta.Usage，
// 并标注配置条目 ID（NewProvider 时绑定）。
type einoStream struct {
	sr         *eino.StreamReader[*eino.Message]
	frames     []*eino.Message
	entryID string
}

func (s *einoStream) Recv() (*schema.Message, error) {
	chunk, err := s.sr.Recv()
	if err != nil {
		return nil, err
	}
	s.frames = append(s.frames, chunk)
	return FromEinoMessage(chunk), nil
}

func (s *einoStream) Final() (*schema.Message, error) {
	if len(s.frames) == 0 {
		return nil, nil
	}
	full, err := eino.ConcatMessages(s.frames)
	if err != nil {
		return nil, err
	}
	msg := FromEinoMessage(full)
	// 流式 provider 通常只在末帧携带 usage：从尾向前找第一个非空的。
	for i := len(s.frames) - 1; i >= 0; i-- {
		if rm := s.frames[i].ResponseMeta; rm != nil && rm.Usage != nil {
			msg.Usage = &schema.UsageInfo{
				PromptTokens:     rm.Usage.PromptTokens,
				CompletionTokens: rm.Usage.CompletionTokens,
				TotalTokens:      rm.Usage.TotalTokens,
				CachedTokens:     rm.Usage.PromptTokenDetails.CachedTokens,
				EntryID:          s.entryID,
			}
			break
		}
	}
	return msg, nil
}

func (s *einoStream) Close() error {
	s.sr.Close()
	return nil
}

// toEinoToolInfos 把领域工具描述转换为 eino ToolInfo（含 JSON Schema 包装）。
func toEinoToolInfos(tools []*schema.ToolSchema) []*eino.ToolInfo {
	infos := make([]*eino.ToolInfo, 0, len(tools))
	for _, t := range tools {
		schemaBytes, _ := json.Marshal(t.Parameters)
		var js jsonschema.Schema
		_ = json.Unmarshal(schemaBytes, &js)
		infos = append(infos, &eino.ToolInfo{
			Name:        t.Name,
			Desc:        t.Description,
			ParamsOneOf: eino.NewParamsOneOfByJSONSchema(&js),
		})
	}
	return infos
}
