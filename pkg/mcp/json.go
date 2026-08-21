package mcp

import "encoding/json"

// remarshalJSON 把任意可 JSON 序列化的值经 marshal/unmarshal 归一化到 v
// （用于把 SDK 可能返回的结构体形式 InputSchema 统一为 map[string]any）。
func remarshalJSON(in any, out any) error {
	data, err := json.Marshal(in)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}
