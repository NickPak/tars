package boot

import (
	"tars/pkg/schema"
	"tars/pkg/skill"
)

// internal/boot（消费侧定义，session.Manager 天然满足）

// LoadedSkillState 是"已加载"幂等集合的会话级读写面。
type LoadedSkillState interface {
	IsSkillLoaded(name string) bool
	MarkSkillLoaded(name string)
	GetLoadedSkills() []string
}

var _ skill.SkillProvider = (*SkillProvider)(nil)

// SkillProvider 实现 skills.SkillProvider：读 SKILL.md 走 skills.Manager，
// "已加载"幂等状态走会话级 Info.LoadedSkills。
type SkillProvider struct {
	mgr   *skill.Manager
	state LoadedSkillState
}

func NewSkillProvider(mgr *skill.Manager, state LoadedSkillState) *SkillProvider {
	return &SkillProvider{
		mgr:   mgr,
		state: state,
	}
}

func (r *SkillProvider) Startup() error {
	return nil
}

func (r *SkillProvider) Shutdown() error {
	return nil
}

func (r *SkillProvider) GetSystemMessage() *schema.Message {
	return &schema.Message{
		Role:    schema.RoleSystem,
		Content: r.mgr.RenderIndex(),
	}
}

func (r *SkillProvider) Load(name string) (string, error) {
	return r.mgr.LoadSkill(name)
}

func (r *SkillProvider) Search(query string, limit int) ([]skill.SkillSummary, error) {
	if r.mgr == nil {
		return nil, nil
	}
	hits, err := r.mgr.Search(query, limit)
	if err != nil {
		return nil, err
	}
	out := make([]skill.SkillSummary, len(hits))
	for i, h := range hits {
		out[i] = skill.SkillSummary{Name: h.Name, Description: h.Description, Category: h.Category}
	}
	return out, nil
}

func (r *SkillProvider) SearchLimit() int {
	if r.mgr == nil || r.mgr.GetConfig() == nil {
		return skill.DefaultDiscoverResultLimit
	}
	return r.mgr.GetConfig().DiscoverResultLimit
}

func (r *SkillProvider) IsSkillLoaded(name string) bool {
	return r.state.IsSkillLoaded(name)
}

func (r *SkillProvider) MarkSkillLoaded(name string) {
	r.state.MarkSkillLoaded(name)
}

func (r *SkillProvider) GetLoadedSkills() []string {
	return r.state.GetLoadedSkills()
}
