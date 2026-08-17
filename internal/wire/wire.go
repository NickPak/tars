// Package wire 是装配层（composition root）：项目里唯一 new 领域对象并组装依赖的地方。
//
// 它把用户配置变成一份就绪的运行时依赖（Runtime），服务层只负责持有并委托，
// 不直接 new 任何领域对象。这样「谁创建对象」的决策集中在一处，内核包
// （session/skills/tools/llm/agent/turn）保持无全局单例、可独立测试。
package wire

import (
	"fmt"
	"runtime"

	"tars/internal/event"
	"tars/internal/runner"
	"tars/internal/session"
	"tars/internal/skills"
	"tars/pkg/llm"
	"tars/pkg/prompt"
	"tars/pkg/store"
	"tars/pkg/tools"

	"github.com/cloudwego/eino/schema"
)

// Options 是装配所需的输入，字段来自应用配置（config.AppConfig）。
type Options struct {
	WorkDir string
	LLM     *llm.Config
	Skills  *skills.Config
	Sink    event.Sink // 事件输出端口（由服务层实现，内核不 import Wails）
}

// Runtime 是一份就绪的运行时依赖集合，由 Build 创建并返回。
// 服务层持有 Runtime，按需取用各依赖委托给领域对象。
type Runtime struct {
	Store    *store.SessionStore
	Tools    *tools.Manager
	Skills   *skills.Manager
	LLM      *llm.Registry
	Sessions *session.Manager
	SysMsg   *schema.Message
	Runner   *runner.Runner // 长命运行器：持有 deps + sessions，驱动一轮轮对话
}

// Build 创建全部运行时依赖并组装成一份 Runtime。
// 它是「new 领域对象」的唯一入口；调用方只持有 Runtime 并委托。
func Build(opts Options) (*Runtime, error) {
	if opts.Sink == nil {
		opts.Sink = event.Discard
	}

	st, err := store.NewSessionStore(opts.WorkDir)
	if err != nil {
		return nil, fmt.Errorf("wire: session store: %w", err)
	}

	sk, err := skills.NewManager(opts.WorkDir, opts.Skills)
	if err != nil {
		return nil, fmt.Errorf("wire: skills: %w", err)
	}
	if err := sk.GenerateIndex(); err != nil {
		return nil, fmt.Errorf("wire: skills index: %w", err)
	}

	tm := tools.NewManagerWithBuiltins(opts.WorkDir)

	lr := llm.NewRegistry(opts.LLM)

	sysMsg := prompt.BuildSystemMessage(prompt.EnvironmentContext{
		OS:       runtime.GOOS,
		Platform: runtime.GOARCH,
		Tools:    tm.ToolNames(),
	})

	sessions := session.NewManager(st)
	deps := runner.Deps{
		Store:  st,
		Tools:  tm,
		Skills: sk,
		LLM:    lr,
		SysMsg: sysMsg,
		Sink:   opts.Sink,
	}

	return &Runtime{
		Store:    st,
		Tools:    tm,
		Skills:   sk,
		LLM:      lr,
		Sessions: sessions,
		SysMsg:   sysMsg,
		Runner:   runner.New(deps, sessions),
	}, nil
}
