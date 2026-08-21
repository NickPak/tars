package tools

import "sync"

// RiskTable 记录"本会话常允许"的危险操作类别（内存态，重启清空）。
// 键形如 "run_command:rm-recursive"。并发安全。
type RiskTable struct {
	mu      sync.Mutex
	allowed map[string]bool
}

// NewRiskTable 创建空的常允许表。
func NewRiskTable() *RiskTable {
	return &RiskTable{allowed: make(map[string]bool)}
}

// Allow 把某类危险操作记入常允许表（"本会话常允许此类"）。
func (t *RiskTable) Allow(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.allowed[key] = true
}

// Allowed 报告某类危险操作是否已被用户常允许。
func (t *RiskTable) Allowed(key string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.allowed[key]
}
