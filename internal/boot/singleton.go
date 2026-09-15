package boot

import "sync/atomic"

// current 是进程级 App 持有器：由装配层（main 包的生命周期服务）在
// ServiceStartup 时写入唯一实例，各 Wails 服务经 Current 读取。
//
// App 本体仍是普通对象——测试照常 new 自己的实例，不经过此持有器；
// 这里只是为桌面进程的单一装配结果提供一个无传递成本的访问点。
var current atomic.Pointer[App]

// SetCurrent 登记进程级 App 实例（装配期调用一次，先于任何前端调用）。
func SetCurrent(a *App) {
	current.Store(a)
}

// Current 返回进程级 App 实例；未装配时返回 nil（调用方应视为
// "backend not ready"——正常时序下不会发生：Wails 在全部
// ServiceStartup 完成后才放行前端调用）。
func Current() *App {
	return current.Load()
}
