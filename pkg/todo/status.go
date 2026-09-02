package todo

import (
	"strconv"
	"strings"
)

// RenderStatus 实现 agent 包状态栏的 StatusSection 消费侧窄接口
// （方法集天然满足，无需 import agent 包）：把当前 TODO 快照自渲染为
// 状态栏 <todo> 区块的 XML 片段。列表为空时返回空串（区块省略）。
//
// 陈旧提醒：列表连续 N 轮未更新（版本号未变）且仍有未完成项时，
// 在区块内追加推进提醒。iteration 由 Agent 循环传入。
func (s *Manager) RenderStatus(iteration int) string {
	s.mu.Lock()
	if s.version != s.renderVersion {
		s.renderVersion = s.version
		s.renderChangedIter = iteration
	}
	todos := s.todos
	changedIter := s.renderChangedIter
	s.mu.Unlock()

	if len(todos) == 0 {
		return ""
	}

	var b strings.Builder
	b.Grow(256)
	b.WriteString("  <todo>\n")
	pending := 0
	for i, t := range todos {
		b.WriteString("    [")
		b.WriteString(strconv.Itoa(i + 1))
		b.WriteString("] ")
		b.WriteString(truncateLine(t.Content, 60))
		b.WriteString(" — ")
		b.WriteString(t.Status)
		b.WriteByte('\n')
		if t.Status == TodoPending || t.Status == TodoInProgress {
			pending++
		}
	}
	if stale := iteration - changedIter; stale >= 3 && pending > 0 {
		b.WriteString("    todo: unchanged for ")
		b.WriteString(strconv.Itoa(stale))
		b.WriteString(" turns (")
		b.WriteString(strconv.Itoa(pending))
		b.WriteString(" items pending) → check whether to advance or update it\n")
	}
	b.WriteString("  </todo>\n")
	return b.String()
}

// truncateLine 截断到 n 个字符并去除换行（状态栏单行展示用）。
func truncateLine(s string, n int) string {
	s = strings.TrimSpace(s)
	if !strings.ContainsRune(s, '\n') {
		if len(s) <= n {
			return s
		}
		return s[:n] + "…"
	}
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
