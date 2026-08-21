package mcp

import (
	"fmt"

	"tars/pkg/search"
)

// FullToolName 合成会话内工具名：mcp__<server>__<tool>。
// 前缀命名天然防同名遮蔽（3.3 tool shadowing），且让模型一眼识别来源。
func FullToolName(server, tool string) string {
	return fmt.Sprintf("mcp__%s__%s", server, tool)
}

// Search 在启用服务器的探测缓存中按自然语言需求检索工具
// （BM25 引擎与 skills 共用 pkg/search）。检索文档为
// "工具名 + 工具描述 + 服务器名"：服务器名参与索引使"yahoo finance 股价"
// 这类需求能命中 description 较弱的工具。
// 未探测（ToolCount=0）的服务器自然无条目、不参与命中。
func (m *Manager) Search(query string, limit int) []ToolHit {
	enabled := map[string]*ServerInfo{}
	for _, srv := range m.Enabled() {
		enabled[srv.Name] = srv
	}

	var items []search.Item[ToolHit]
	for name := range enabled {
		for _, ti := range m.Tools(name) {
			hit := ToolHit{
				Server:      name,
				Name:        ti.Name,
				FullName:    FullToolName(name, ti.Name),
				Description: ti.Description,
				SourceType:  enabled[name].SourceType,
				InputSchema: ti.InputSchema,
			}
			items = append(items, search.Item[ToolHit]{
				Text:    ti.Name + " " + ti.Description + " " + name,
				Payload: hit,
			})
		}
	}
	return search.Search(items, query, limit)
}
