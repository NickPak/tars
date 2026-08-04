package tools

import (
	"encoding/json"
)

// UnmarshalArgs 将模型生成的 JSON 参数反序列化为指定类型，供 Handler 使用。
func UnmarshalArgs[T any](args json.RawMessage) (T, error) {
	var v T
	err := json.Unmarshal(args, &v)
	return v, err
}
