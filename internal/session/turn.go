package session

import "tars/pkg/schema"

// 轮（turn）= 一条 user 消息 + 其后全部 assistant/tool 消息。
// 本文件是"轮"这一概念在后端的唯一实现处：分组键取 schema.Message.TurnID，
// 旧数据（TurnID 为空）回退"轮起点 = user 消息"的扫描规则。
//
// 纪律：Iteration 是展示元数据，不参与本文件任何判定——它在截断类操作与
// 压缩投影后可能出现缺口或非 1 起始（见 schema.Message.Iteration 注释）。

// turnKey 返回消息所属轮的分组键：优先 TurnID；旧数据回退为
// user 消息自身 ID（非 user 消息返回空，由调用方按位置归属）。
func turnKey(m *schema.Message) string {
	if m.TurnID != "" {
		return m.TurnID
	}
	if m.Role == schema.RoleUser {
		return m.ID
	}
	return ""
}

// CurrentTurnID 返回当前轮的分组键（最后一条 user 消息对应的键）；
// 无 user 消息时返回空串。写入侧（AppendMessage 盖章）使用。
func (i *Data) CurrentTurnID() string {
	for k := len(i.Messages) - 1; k >= 0; k-- {
		if i.Messages[k].Role == schema.RoleUser {
			return turnKey(i.Messages[k])
		}
	}
	return ""
}

// TurnStarts 返回各轮起点在 msgs 中的下标（升序）。
// 判定：TurnID 相对前一条发生变化即新轮；两者皆无 TurnID 时回退
// "遇到 user 消息即新轮"。合成归档消息（TurnID 为空且 Role 为 user、
// ID 为 SyntheticMessageID）不计为轮起点——它是投影产物，不属于任何轮。
func TurnStarts(msgs []*schema.Message) []int {
	var starts []int
	prev := ""
	for idx, m := range msgs {
		if m.ID == SyntheticMessageID {
			continue
		}
		key := turnKey(m)
		switch {
		case key == "":
			// 无键的 assistant/tool（旧数据）：归属前一轮
		case key != prev:
			starts = append(starts, idx)
			prev = key
		}
	}
	return starts
}

// TurnStartBefore 返回下标 idx 所属轮的起点下标；找不到返回 -1。
func TurnStartBefore(msgs []*schema.Message, idx int) int {
	if idx < 0 || idx >= len(msgs) {
		return -1
	}
	key := turnKey(msgs[idx])
	if key != "" {
		// 有键：向前找该键的首条消息
		first := idx
		for k := idx - 1; k >= 0; k-- {
			if turnKey(msgs[k]) == key {
				first = k
			} else if turnKey(msgs[k]) != "" {
				break
			}
		}
		return first
	}
	// 无键（旧数据的 assistant/tool）：回退扫描最近的 user 消息
	for k := idx; k >= 0; k-- {
		if msgs[k].Role == schema.RoleUser && msgs[k].ID != SyntheticMessageID {
			return k
		}
	}
	return -1
}
