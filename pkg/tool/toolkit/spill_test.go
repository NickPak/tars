package toolkit

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

// mkOutput 造一段行号可辨识的长输出（便于断言头尾各保住了哪部分）。
func mkOutput(lines int) string {
	var b strings.Builder
	for i := 1; i <= lines; i++ {
		fmt.Fprintf(&b, "line %06d: %s\n", i, strings.Repeat("x", 60))
	}
	return b.String()
}

// 不超限：原样返回，不落盘。
func TestSpillPassthroughUnderLimit(t *testing.T) {
	a := &fakeArchive{}
	sp := NewOutputSpill(a)
	in := "short output\n"

	if got := sp.Apply("run_command", in); got != in {
		t.Fatalf("output altered: %q", got)
	}
	if len(a.files) != 0 {
		t.Fatalf("nothing should be spilled: %v", a.files)
	}
}

// 超限：头尾都保住，中间标注省略量与指针；全文落盘。
//
// 头尾并存是关键改进：命令输出的结论在**尾部**（`go test` 的失败汇总、
// 栈顶、exit 码）。原实现只留头部，恰好丢掉最该看的部分。
func TestSpillKeepsHeadAndTail(t *testing.T) {
	a := &fakeArchive{}
	sp := NewOutputSpill(a)
	in := mkOutput(2000) // ~140KB，远超 24KB

	out := sp.Apply("run_command", in)

	if len(out) > MaxOutputBytes {
		t.Fatalf("spilled output %d bytes exceeds cap %d", len(out), MaxOutputBytes)
	}
	if !strings.Contains(out, "line 000001:") {
		t.Fatalf("head lost:\n%s", out[:200])
	}
	if !strings.Contains(out, "line 002000:") {
		t.Fatalf("tail lost (this is the regression the change is about):\n%s", out[len(out)-200:])
	}
	if !strings.Contains(out, "omitted") || !strings.Contains(out, ArchiveScheme) {
		t.Fatalf("notice missing amount or pointer:\n%s", out)
	}
	// 标注须点明分页：落盘文件通常大于单次输出上限，不说模型会把首屏当全文
	if !strings.Contains(out, "offset/limit") {
		t.Fatalf("notice must mention paging:\n%s", out)
	}

	// 落盘的是全文
	if len(a.files) != 1 {
		t.Fatalf("expected exactly one spill file, got %d", len(a.files))
	}
	for _, content := range a.files {
		if !strings.Contains(string(content), "line 001000:") {
			t.Fatal("spill file should hold the complete output, including the middle")
		}
		if len(content) < len(in) {
			t.Fatalf("spill file %d bytes < original %d", len(content), len(in))
		}
	}
}

// 指针必须能被 read_file 的 archive:// 通道解析（自产指针不能被自己挡住），
// 且落盘文件本身通常远超单次输出上限——必须能靠分页读到中段。
func TestSpillPointerIsReadable(t *testing.T) {
	a := &fakeArchive{}
	ft, _ := archiveFT(t, nil)
	ft.archive = a
	sp := NewOutputSpill(a)

	out := sp.Apply("code_interpreter", mkOutput(2000))

	// 从标注里取出指针原样交给 read_file
	i := strings.Index(out, ArchiveScheme)
	if i < 0 {
		t.Fatalf("no pointer in output:\n%s", out)
	}
	ptr := out[i:]
	if j := strings.IndexAny(ptr, "\"' )\n"); j > 0 {
		ptr = ptr[:j]
	}

	// 首屏：读得到，且因为文件大于单次上限，必须给出分页通知——
	// 否则模型不知道自己只看到了开头，会把首屏当全文。
	got := call(t, ft.ReadFile(), fmt.Sprintf(`{"path":%q}`, ptr))
	if !strings.Contains(got, "Full output of code_interpreter") {
		t.Fatalf("archive head not readable:\n%s", got[:min(300, len(got))])
	}
	if !strings.Contains(got, "showing lines") {
		t.Fatalf("large archive must announce pagination:\n%s", got[max(0, len(got)-300):])
	}

	// 分页：offset 能抵达中段（原实现下这部分是永久丢失的）
	got = call(t, ft.ReadFile(), fmt.Sprintf(`{"path":%q,"offset":1000,"limit":5}`, ptr))
	if !strings.Contains(got, "line 000997:") {
		t.Fatalf("pagination did not reach the middle of the spill file:\n%s", got)
	}
}

// 落盘失败不得让工具调用失败：退回原地截断（保底等于改动前行为）。
func TestSpillFallsBackOnWriteError(t *testing.T) {
	a := &fakeArchive{writeErr: errors.New("disk full")}
	sp := NewOutputSpill(a)
	in := mkOutput(2000)

	out := sp.Apply("run_command", in)
	if !strings.Contains(out, "truncated to first") {
		t.Fatalf("expected plain truncation fallback:\n%s", out[len(out)-200:])
	}
	if len(out) > MaxOutputBytes+200 {
		t.Fatalf("fallback output too large: %d", len(out))
	}
}

// 未装配归档通道时同样退化为截断，不 panic。
func TestSpillWithoutProvider(t *testing.T) {
	sp := NewOutputSpill(nil)
	out := sp.Apply("run_command", mkOutput(2000))
	if !strings.Contains(out, "truncated to first") {
		t.Fatalf("expected truncation without provider:\n%s", out[len(out)-200:])
	}
}

// 多次落盘的归档名互不覆盖（同一会话内并发/连续调用）。
func TestSpillNamesAreUnique(t *testing.T) {
	a := &fakeArchive{}
	sp := NewOutputSpill(a)
	for i := 0; i < 3; i++ {
		sp.Apply("run_command", mkOutput(2000))
	}
	if len(a.files) != 3 {
		t.Fatalf("expected 3 distinct spill files, got %d: %v", len(a.files), keysOf(a.files))
	}
	// 且每个名字都过 archive:// 白名单
	for name := range a.files {
		if !archiveNamePattern.MatchString(name) {
			t.Fatalf("spill name %q fails the archive:// whitelist", name)
		}
	}
}

func keysOf(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// 切割不得产生非法 UTF-8（多字节字符不能被切半）。
func TestSpillKeepsValidUTF8(t *testing.T) {
	a := &fakeArchive{}
	sp := NewOutputSpill(a)
	// 全中文无换行，每字符 3 字节：按字节切割极易落在字符中间，
	// 且没有换行可供对齐，正好压测 ToValidUTF8 兜底。
	in := strings.Repeat("测试输出内容", 20000)

	out := sp.Apply("run_command", in)
	if !utf8.ValidString(out) {
		t.Fatal("spilled output contains invalid UTF-8 (cut landed mid-rune)")
	}
	if !strings.Contains(out, "omitted") {
		t.Fatalf("notice missing:\n%s", out[:200])
	}
}
