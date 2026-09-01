package toolkit

import (
	"fmt"
	"tars/pkg/tool/kernel"

	"tars/pkg/sandbox"
)

// FileTools 是工作区文件工具集（read/write/edit/glob/grep）的载体（Carrier）：
// 持有执行环境的文件面（sandbox.FileSystem），五个工具的 Definition 由其
// 方法产出，workspace 边界检查由 FileSystem 实现保证。
//
// archive 是压缩归档的只读通道（archive:// 虚拟路径，见 archive.go）：
// 归档原文在会话数据目录下、workspace 之外，沙箱按设计读不到它——
// 轨迹与派生归档不该落在模型可写区域。故不新增工具，而是让 read_file
// 多认一种路径形态。
type FileTools struct {
	fs      sandbox.FileSystem
	archive ArchiveProvider
}

// NewFileTools 创建文件工具集载体。fs 为执行环境的文件面（native/docker/vm）；
// nil 时各 handler 报错（装配层须保证注入）。archive 为 nil 时 archive://
// 路径报错，普通文件读写不受影响。
func NewFileTools(fs sandbox.FileSystem, archive ArchiveProvider) *FileTools {
	return &FileTools{fs: fs, archive: archive}
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
