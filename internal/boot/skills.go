package boot

import (
	"tars/internal/session"
	"tars/pkg/schema"
	"tars/pkg/skill"
)

// SkillProvider 实现 skills.SkillProvider：读 SKILL.md 走 skills.Manager，
// "已加载"幂等状态走会话级 Info.LoadedSkills。
type SkillProvider struct {
	mgr  *skill.Manager
	sess *session.Manager
}

func NewSkillProvider(mgr *skill.Manager, sess *session.Manager) *SkillProvider {
	return &SkillProvider{mgr: mgr, sess: sess}
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

func (r *SkillProvider) IsLoaded(name string) bool {
	return r.sess.IsSkillLoaded(name)
}

func (r *SkillProvider) MarkLoaded(name string) {
	r.sess.MarkSkillLoaded(name)
}

func (r *SkillProvider) Loaded() []string {
	return r.sess.LoadedSkillNames()
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

var _ skill.SkillProvider = (*SkillProvider)(nil)
