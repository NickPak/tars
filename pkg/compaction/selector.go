package compaction

import "tars/pkg/schema"

// Selector 在原始轨迹上选择新的压缩边界（02 篇 §7）。
type Selector interface {
	// Select 返回本次压缩集（raw 的子切片）与新 cutoff（压缩集最后一条
	// 消息的 ID）；ok=false 表示不值得压（压缩集过小或无足够轮次可切）。
	// 约束：保留区必须从某条 user 消息开始（轮边界对齐，01 篇 §3），
	// 不得切断 assistant 与其 tool 结果的配对。
	Select(raw []*schema.Message, currentCutoff string) (batch []*schema.Message, cutoffID string, ok bool)
}

// tailKeep 是默认选择器：保留尾部最近 keepTurns 个完整轮，其余进压缩集；
// 压缩集消息数 < minBatch 时不压（收益不抵一次缓存重建）。
type tailKeep struct {
	keepTurns int
	minBatch  int
}

// NewTailKeepSelector 创建尾部保留选择器。
func NewTailKeepSelector(keepTurns, minBatch int) Selector {
	return tailKeep{keepTurns: keepTurns, minBatch: minBatch}
}

func (s tailKeep) Select(raw []*schema.Message, currentCutoff string) ([]*schema.Message, string, bool) {
	start := 0
	if currentCutoff != "" {
		// cutoff 失效（轨迹被截断但压缩态未清理等）防御性回退为从头开始。
		if idx := indexOfID(raw, currentCutoff); idx >= 0 {
			start = idx + 1
		}
	}

	// 保留区起点：从尾部向前数第 keepTurns 个轮（user 消息）的位置。
	cut := -1
	turns := 0
	for i := len(raw) - 1; i > start; i-- {
		if raw[i].Role == schema.RoleUser {
			turns++
			if turns >= s.keepTurns {
				cut = i
				break
			}
		}
	}
	if cut <= start {
		return nil, "", false // 尾部轮数不足，无可压
	}

	batch := raw[start:cut]
	if len(batch) < s.minBatch {
		return nil, "", false
	}
	return batch, raw[cut-1].ID, true
}

func indexOfID(raw []*schema.Message, id string) int {
	for i, m := range raw {
		if m.ID == id {
			return i
		}
	}
	return -1
}
