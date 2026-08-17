package runner

import (
	"tars/internal/session"
	"tars/internal/skills"
	"tars/pkg/tools"
)

// skillRuntime 实现 tools.SkillRuntime：读 SKILL.md 走 skills.Manager，
// "已加载"幂等状态走会话级 Info.LoadedSkills。
type skillRuntime struct {
	mgr  *skills.Manager
	sess *session.Info
}

func newSkillRuntime(mgr *skills.Manager, sess *session.Info) *skillRuntime {
	return &skillRuntime{mgr: mgr, sess: sess}
}

func (r *skillRuntime) Load(name string) (string, error) {
	return r.mgr.LoadSkill(name)
}

func (r *skillRuntime) IsLoaded(name string) bool {
	return r.sess.IsSkillLoaded(name)
}

func (r *skillRuntime) MarkLoaded(name string) {
	r.sess.MarkSkillLoaded(name)
}

func (r *skillRuntime) Loaded() []string {
	return r.sess.LoadedSkillNames()
}

func (r *skillRuntime) Search(query string, limit int) ([]tools.SkillSummary, error) {
	if r.mgr == nil {
		return nil, nil
	}
	hits, err := r.mgr.Search(query, limit)
	if err != nil {
		return nil, err
	}
	out := make([]tools.SkillSummary, len(hits))
	for i, h := range hits {
		out[i] = tools.SkillSummary{Name: h.Name, Description: h.Description, Category: h.Category}
	}
	return out, nil
}

var _ tools.SkillRuntime = (*skillRuntime)(nil)
