package session

import "tars/pkg/schema"

// Pressure 压缩压力档位（02 篇 §7.1）：常规档压不回预算时逐级放宽保留区。
//
// 为什么降级要作用在保留区而不是归档区：保留区是预算里最大的一项——单条
// 工具输出上限 64KB ≈ 16k token（toolkit.MaxOutputBytes），6 轮就能吃满
// 128k 窗口的全部预算；而归档条目每条约 200 token，要几百轮才到同等量级。
// 卡住时只清归档区，回收的是小项，照样超。
type Pressure int

const (
	// PressureNormal 常规档：keepTurns 与 minBatch 均按配置生效。
	PressureNormal Pressure = iota
	// PressureHigh 高压档：保留区减半（minBatch 仍生效）。
	PressureHigh
	// PressureCritical 极限档：只保留最后 1 轮，且忽略 minBatch——
	// minBatch 的本意是"收益不抵一次缓存重建"，但此刻不压就是请求被
	// 供应商拒绝、整轮失败，任何压缩都比失败强。
	PressureCritical
)

// Pressures 是降级阶梯（由轻到重），供调用方顺序尝试。
var Pressures = []Pressure{PressureNormal, PressureHigh, PressureCritical}

func (p Pressure) String() string {
	switch p {
	case PressureHigh:
		return "high"
	case PressureCritical:
		return "critical"
	default:
		return "normal"
	}
}

// Selector 在原始轨迹上选择新的压缩边界（02 篇 §7）。
type Selector interface {
	// Select 返回本次压缩集（raw 的子切片）与新 cutoff（压缩集最后一条
	// 消息的 ID）；ok=false 表示不值得压（压缩集过小或无足够轮次可切）。
	// 约束：保留区必须从某条 user 消息开始（轮边界对齐，01 篇 §3），
	// 不得切断 assistant 与其 tool 结果的配对。
	// p 为压力档位，决定保留区力度（见 Pressure）。
	Select(raw []*schema.Message, currentCutoff string, p Pressure) (batch []*schema.Message, cutoffID string, ok bool)
}

// TailKeep 是默认选择器：保留尾部最近 keepTurns 个完整轮，其余进压缩集；
// 压缩集消息数 < minBatch 时不压（收益不抵一次缓存重建）。
type TailKeep struct {
	keepTurns int
	minBatch  int
}

// NewTailKeepSelector 创建尾部保留选择器。
func NewTailKeepSelector(keepTurns, minBatch int) Selector {
	return &TailKeep{keepTurns: keepTurns, minBatch: minBatch}
}

// params 按压力档位算出本次生效的保留轮数与最小批量。
func (s *TailKeep) params(p Pressure) (keepTurns, minBatch int) {
	keepTurns, minBatch = s.keepTurns, s.minBatch
	switch p {
	case PressureHigh:
		keepTurns = (keepTurns + 1) / 2 // 向上取整，避免 1 → 0
	case PressureCritical:
		keepTurns, minBatch = 1, 1
	}
	if keepTurns < 1 {
		keepTurns = 1
	}
	if minBatch < 1 {
		minBatch = 1
	}
	return keepTurns, minBatch
}

func (s *TailKeep) Select(raw []*schema.Message, currentCutoff string, p Pressure) ([]*schema.Message, string, bool) {
	keepTurns, minBatch := s.params(p)

	start := 0
	if currentCutoff != "" {
		// cutoff 失效（轨迹被截断但压缩态未清理等）防御性回退为从头开始。
		if idx := indexOfID(raw, currentCutoff); idx >= 0 {
			start = idx + 1
		}
	}

	// 保留区起点：从尾部向前数第 keepTurns 个轮的起点（轮划分见 turn.go；
	// 切点落在轮边界上，assistant 与其 tool 结果的配对不会被切断）。
	cut := -1
	starts := TurnStarts(raw)
	if kept := len(starts) - keepTurns; kept > 0 {
		if c := starts[kept]; c > start {
			cut = c
		}
	}
	if cut <= start {
		return nil, "", false // 尾部轮数不足，无可压
	}

	batch := raw[start:cut]
	if len(batch) < minBatch {
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
