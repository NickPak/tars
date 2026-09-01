package toolkit

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// ArchiveScheme 是归档原文的虚拟路径前缀。
//
// 为什么用 scheme 而不是 ".archive/" 这类相对前缀：用户工作区里完全可能真有
// 一个 archive/ 目录，普通前缀会撞车且行为取决于"谁先存在"，不确定。而
// "archive://" 永远不是合法相对路径，语义唯一；万一分支漏判流到下游，
// sandbox.confine 也必然拒绝（fail-safe，不会误读到别的文件）。
const ArchiveScheme = "archive://"

// archiveNamePattern 归档名白名单：纯文件名，无目录分隔符，无 ".."。
// 这是路径穿越的唯一防线——scheme 之后不允许任何路径拼接自由度。
var archiveNamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]+\.md$`)

// ArchiveProvider 提供本会话归档目录的读写（由 session 层实现）。
// 读侧服务压缩条目与溢出输出的回读；写侧服务超长工具输出落盘。
type ArchiveProvider interface {
	ReadArchive(name string) ([]byte, error)
	WriteArchive(name string, content []byte) (string, error)
}

// ArchiveName 解析归档虚拟路径；ok=false 表示这不是归档路径，按普通文件走。
func ArchiveName(path string) (string, bool) {
	if !strings.HasPrefix(path, ArchiveScheme) {
		return "", false
	}
	return strings.TrimPrefix(path, ArchiveScheme), true
}

// readArchive 读取归档原文。只读——写侧工具没有这个分支，
// write_file/edit_file 碰 archive:// 路径会被 confine 直接拒。
func (f *FileTools) readArchive(name string) ([]byte, error) {
	if f.archive == nil {
		return nil, fmt.Errorf("this session has no compaction archive")
	}
	if !archiveNamePattern.MatchString(name) {
		return nil, fmt.Errorf("invalid archive name %q: expected a bare file name such as %sturn_3-5.md (no directories, no ..)", name, ArchiveScheme)
	}
	data, err := f.archive.ReadArchive(name)
	if err != nil {
		return nil, fmt.Errorf("read archive %q: %w — use the pointer exactly as shown in the <context_archive> block", name, err)
	}
	return data, nil
}

// archiveHint 当模型把归档指针误写成普通工作区路径时，在错误里附上正确读法。
// 归档指针形如 turn_3-5.md / msg_r7.md，普通项目里几乎不会有同名文件，
// 误判成本低；一次失败调用即可自我纠正。
func archiveHint(path string, err error) error {
	base := filepath.Base(path)
	if !strings.HasSuffix(base, ".md") {
		return err
	}
	if !strings.HasPrefix(base, "turn_") && !strings.HasPrefix(base, "msg_") {
		return err
	}
	return fmt.Errorf("%w — if you meant a compaction archive, read it as %s%s", err, ArchiveScheme, base)
}
