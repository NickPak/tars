package toolkit

import (
	"fmt"
	"tars/pkg/tool/kernel"

	"tars/pkg/sandbox"
)

// FileTools 是工作区文件工具集（read/write/edit/glob/grep）的载体（Carrier）：
// 持有执行环境的文件面（sandbox.FileSystem），五个工具的 Definition 由其
// 方法产出，workspace 边界检查由 FileSystem 实现保证。
type FileTools struct {
	fs sandbox.FileSystem
}

// NewFileTools 创建文件工具集载体。fs 为执行环境的文件面（native/docker/vm）；
// nil 时各 handler 报错（装配层须保证注入）。
func NewFileTools(fs sandbox.FileSystem) *FileTools {
	return &FileTools{fs: fs}
}

// Definitions 实现 tool.Carrier：产出五个文件工具（字典序，注册顺序稳定）。
func (f *FileTools) Definitions() []*kernel.Definition {
	return []*kernel.Definition{
		f.EditFile(),
		f.GlobFiles(),
		f.GrepFiles(),
		f.ReadFile(),
		f.WriteFile(),
	}
}

// Close 实现 tool.Carrier：文件工具无自有资源（执行环境归装配层回收）。
func (f *FileTools) Close() error { return nil }

// fileSystem 返回文件面；nil 时返回错误（供各 handler 开头调用）。
func (f *FileTools) fileSystem() (sandbox.FileSystem, error) {
	if f.fs == nil {
		return nil, fmt.Errorf("no execution environment configured")
	}
	return f.fs, nil
}
