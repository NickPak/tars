package skill

import (
	"strings"

	"tars/pkg/schema"
)

var _ Provider = (*Runtime)(nil)

// Runtime 是 Provider 的会话级实现：读写 SKILL.md、检索、索引渲染全部
// 穿透到 Manager（活引用，永远读最新快照）；"已加载"幂等状态走会话级
// StateProvider（session.Manager 天然满足，构造时注入——状态归会话，
// Runtime 本体跨轮复用）。
//
// 三层生命周期：Manager（进程级权威 + 工厂）→ Registry（不可变 COW
// 快照）→ Runtime（会话级视图）。Runtime 独立于 Registry 存在的原因：
// Registry 已发布即不可变，而"已加载"状态在会话内持续可变；且会话
// 必须读到其他会话/设置页变更后的最新注册表，不能持有快照副本。
type Runtime struct {
	mgr   *Manager
	state StateProvider
}

// NewRuntime 工厂方法：为一次会话产出技能运行时（Manager 是进程级
// 单例，Runtime 每会话一个）。
func (m *Manager) NewRuntime(state StateProvider) *Runtime {
	return &Runtime{
		mgr:   m,
		state: state,
	}
}

func (r *Runtime) Startup() error {
	return nil
}

func (r *Runtime) Shutdown() error {
	return nil
}

// GetSystemMessage 把技能索引渲染为 system 消息（PromptCompose 每轮消费）。
func (r *Runtime) GetSystemMessage() *schema.Message {
	return &schema.Message{
		Role:    schema.RoleSystem,
		Content: r.mgr.RenderIndex(),
	}
}

func (r *Runtime) Load(name string) (string, error) {
	return r.mgr.LoadSkill(name)
}

// WriteSkill 实现 Provider 的写入面：写盘/登记/索引重建全部走 Manager
// （调用前已过审批门）。
func (r *Runtime) WriteSkill(name, description, category, body string, overwrite bool) (bool, error) {
	return r.mgr.WriteSkill(name, description, category, body, overwrite)
}

func (r *Runtime) Search(query string, limit int) ([]Summary, error) {
	if r.mgr == nil {
		return nil, nil
	}
	hits, err := r.mgr.Search(query, limit)
	if err != nil {
		return nil, err
	}
	out := make([]Summary, len(hits))
	for i, h := range hits {
		out[i] = Summary{Name: h.Name, Description: h.Description, Category: h.Category}
	}
	return out, nil
}

func (r *Runtime) SearchLimit() int {
	if r.mgr == nil || r.mgr.GetConfig() == nil {
		return DefaultDiscoverResultLimit
	}
	return r.mgr.GetConfig().DiscoverResultLimit
}

func (r *Runtime) IsSkillLoaded(name string) bool {
	return r.state.IsSkillLoaded(name)
}

func (r *Runtime) MarkSkillLoaded(name string) {
	r.state.MarkSkillLoaded(name)
}

func (r *Runtime) GetLoadedSkills() []string {
	return r.state.GetLoadedSkills()
}

// RenderStatus 实现 agent 状态栏的 StatusSection（消费侧窄接口，方法集
// 天然满足，无需 import agent 包）：把已加载技能幂等集合自渲染为
// <skills loaded/> 区块。集合为空时返回空串（区块省略）。
// 已加载技能（load_skill 幂等集合）：让模型明确知道哪些手册已在轨迹中。
func (r *Runtime) RenderStatus(int) string {
	loaded := r.state.GetLoadedSkills()
	if len(loaded) == 0 {
		return ""
	}
	return "  <skills loaded=\"" + strings.Join(loaded, ", ") + "\"/>\n"
}
