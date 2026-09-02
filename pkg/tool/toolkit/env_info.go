package toolkit

// SystemEnv 是 agent 状态栏 EnvInfo 接口的实现（消费侧窄接口，方法集
// 天然满足，无需 import agent 包）：静态系统信息直读进程环境，
// 进程内不变，状态栏在 Start 时一次性采集。
type SystemEnv struct{}

func (SystemEnv) OSInfo() string        { return OSInfo() }
func (SystemEnv) ShellInfo() string     { return ShellInfo() }
func (SystemEnv) PythonVersion() string { return PythonVersion() }
