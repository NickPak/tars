package guard

import "sync"

// RiskTable 记录"本会话常允许"的危险操作类别（内存态，重启清空）。
// 键形如 "run_command:rm-recursive-force"。会话级组件：持久化会把
// 本地临时授权泄露成长期信任。
type RiskTable struct {
	mu      sync.RWMutex
	allowed map[string]bool
}

// NewRiskTable 创建空的常允许表。
func NewRiskTable() *RiskTable {
	return &RiskTable{allowed: make(map[string]bool)}
}

// Allow 记录常允许类别。
func (t *RiskTable) Allow(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.allowed[key] = true
}

// Allowed 判断类别是否已常允许。
func (t *RiskTable) Allowed(key string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.allowed[key]
}
