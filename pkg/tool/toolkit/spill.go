package toolkit

import (
	"fmt"
	"strings"
	"sync/atomic"
	"time"
)

// 超长命令输出的落盘替身（master plan 第 1 层：工具结果预算控制）。
//
// 原实现 TruncateOutput 只保留**前** MaxOutputBytes 字节，其余永久丢失——
// run_command / code_interpreter 没有分页参数，模型重跑同一条命令只会被切在
// 同一位置，后半段永远拿不到。而命令输出的结论恰恰在尾部：`go test` 的失败
// 汇总、栈顶、`[exit: 1]`……被切掉的正是最该看的部分。
//
// 两处改进：
//   - 全文落盘到归档目录，返回 archive:// 指针 → 信息可找回（read_file 可读，
//     且支持 offset/limit 分页）
//   - 就地展示改为**头 + 尾**，中间标注省略量 → 既有起始上下文，又保住结论
const (
	// spillHeadRatio 就地展示预算中分给头部的比例；其余给尾部。
	// 偏向尾部：命令输出的结论在末尾。
	spillHeadRatio = 3 // 1/3 头，2/3 尾
	// spillNoticeReserve 为中间的省略标注预留的字节。
	spillNoticeReserve = 256
)

// OutputSpill 把超长输出落盘并渲染为"头 + 省略标注 + 尾"。
// 载体级持有（会话内长命），seq 保证同一会话内的归档名唯一。
type OutputSpill struct {
	archive ArchiveProvider
	seq     atomic.Uint64
}

// NewOutputSpill 创建落盘器；archive 为 nil 时退化为原地截断（不落盘）。
func NewOutputSpill(archive ArchiveProvider) *OutputSpill {
	return &OutputSpill{archive: archive}
}

// Apply 处理一段工具输出：不超限原样返回；超限则落盘 + 头尾摘要。
// tool 用于归档名与归档文件头（便于人工排查）。
//
// 落盘失败绝不让工具调用失败——退回原地截断（保底行为等于改动前）。
func (o *OutputSpill) Apply(tool, s string) string {
	if len(s) <= MaxOutputBytes {
		return s
	}
	if o == nil || o.archive == nil {
		return TruncateOutput(s)
	}

	name := fmt.Sprintf("out_%s_%d_%d.md", tool, time.Now().UnixMilli(), o.seq.Add(1))
	if _, err := o.archive.WriteArchive(strings.TrimSuffix(name, ".md"), renderSpill(tool, s)); err != nil {
		return TruncateOutput(s)
	}

	budget := MaxOutputBytes - spillNoticeReserve
	headBudget := budget / spillHeadRatio
	head := cutHead(s, headBudget)
	tail := cutTail(s, budget-len(head))

	omitted := len(s) - len(head) - len(tail)
	var b strings.Builder
	b.Grow(len(head) + len(tail) + spillNoticeReserve)
	b.WriteString(head)
	// 标注放在头尾之间，物理位置对应被省略的位置，模型不会误以为尾部紧接头部。
	// 提示 offset/limit：落盘文件通常远大于单次输出上限，一次 read_file 只
	// 拿到开头——不点明分页，模型会把首屏当全文。
	fmt.Fprintf(&b, "\n\n[... %s of %s omitted; full output: read_file(path=%q) — it exceeds one read, use offset/limit to page through ...]\n\n",
		humanBytes(omitted), humanBytes(len(s)), ArchiveScheme+name)
	b.WriteString(tail)
	return b.String()
}

// renderSpill 渲染落盘文件内容（全文，不截断）。
func renderSpill(tool, s string) []byte {
	var b strings.Builder
	b.Grow(len(s) + 128)
	b.WriteString("# Full output of ")
	b.WriteString(tool)
	b.WriteString("\n\n> This is the complete output; the tool result in context shows only its head and tail.\n\n")
	b.WriteString(s)
	b.WriteString("\n")
	return []byte(b.String())
}

// cutHead 取前 n 字节，尽量落在行边界，且不切断多字节字符。
func cutHead(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	cut := s[:n]
	if i := strings.LastIndexByte(cut, '\n'); i > n/2 {
		cut = cut[:i]
	}
	return strings.ToValidUTF8(cut, "")
}

// cutTail 取后 n 字节，尽量落在行边界，且不切断多字节字符。
func cutTail(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	cut := s[len(s)-n:]
	if i := strings.IndexByte(cut, '\n'); i >= 0 && i < n/2 {
		cut = cut[i+1:]
	}
	return strings.ToValidUTF8(cut, "")
}

// humanBytes 人类可读的字节量（给模型判断"值不值得读回来"）。
func humanBytes(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
