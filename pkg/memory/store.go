package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// FactType 事实类型（plan/agent-memory-design-plan.md §3.1）：
// 四类陈述性事实 + lesson（过程性知识暂存，毕业后归 write_skill）。
type FactType string

const (
	FactUser      FactType = "user"
	FactFeedback  FactType = "feedback"
	FactProject   FactType = "project"
	FactReference FactType = "reference"
	FactLesson    FactType = "lesson"
)

// Fact 是一条记忆事实：一事实一文件（<root>/<subject>.md），
// frontmatter 承载元数据，正文一两句话。
type Fact struct {
	Type          FactType `json:"type"`
	Subject       string   `json:"subject"` // 主题键：同 root 内唯一活跃值（文件名锚）
	Created       string   `json:"created"` // 2006-01-02
	LastConfirmed string   `json:"lastConfirmed"`
	ExpiresAt     string   `json:"expiresAt,omitempty"` // 空 = 不过期；到期排除出索引与召回（软遗忘，不删除）
	Source        string   `json:"source"`              // user / archive://...（溯源）
	WrittenBy     string   `json:"writtenBy,omitempty"` // 来源模型条目 ID（防模型特化记忆污染）
	Body          string   `json:"body"`
}

const (
	// indexFile 是索引文件名（派生品，任何写入后重建）。
	indexFile = "MEMORY.md"
	// archiveDirName 是 forget/覆盖的留痕目录（永不真删）。
	archiveDirName = ".archive"
	// candidatesDirName 是记忆候选区目录（采纳制：不生效，待用户采纳）。
	candidatesDirName = ".candidates"
	// rejectedDirName 是候选拒绝审计目录（拒绝过的提议不再重复提出）。
	rejectedDirName = ".rejected"
	// maxFactBodyBytes 单条事实正文上限（白名单细则之一）。
	maxFactBodyBytes = 2000
)

var (
	// subjectRe 允许中英文与数字、连字符（天然排除路径分隔符）。
	subjectRe = regexp.MustCompile(`^[\p{L}\p{N}][\p{L}\p{N}-]*$`)

	// sensitivePatterns 敏感信息黑名单（写入即拒，非审批）：
	// 记忆文件是明文，不是密码管理器。
	sensitivePatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)sk-[a-z0-9]{16,}`),                                       // OpenAI 风格密钥
		regexp.MustCompile(`(?i)(api[_-]?key|secret|token)\s*[:=]\s*\S{8,}`),             // 键值形态凭据
		regexp.MustCompile(`(?i)(password|passwd|密码)\s*[:=：是]?\s*\S{4,}`),            // 密码
		regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`),             // 邮箱
		regexp.MustCompile(`\b\d{17}[\dXx]\b`),                                           // 身份证号
	}
)

type factFrontmatter struct {
	Type          string `yaml:"type"`
	Subject       string `yaml:"subject"`
	Created       string `yaml:"created"`
	LastConfirmed string `yaml:"last_confirmed"`
	ExpiresAt     string `yaml:"expires_at,omitempty"`
	Source        string `yaml:"source"`
	WrittenBy     string `yaml:"written_by,omitempty"`
}

// ValidateFact 校验一条待写事实（白名单细则：类型/subject/正文上限/敏感模式）。
func ValidateFact(f *Fact) error {
	switch f.Type {
	case FactUser, FactFeedback, FactProject, FactReference, FactLesson:
	default:
		return fmt.Errorf("memory: invalid type %q (user/feedback/project/reference/lesson)", f.Type)
	}
	if !subjectRe.MatchString(f.Subject) {
		return fmt.Errorf("memory: invalid subject %q (letters/digits/hyphens, no spaces or slashes)", f.Subject)
	}
	body := strings.TrimSpace(f.Body)
	if body == "" {
		return fmt.Errorf("memory: body is required (one or two sentences)")
	}
	if len(body) > maxFactBodyBytes {
		return fmt.Errorf("memory: body too large (%d > %d bytes); keep it a single fact", len(body), maxFactBodyBytes)
	}
	for _, p := range sensitivePatterns {
		if m := p.FindString(body); m != "" {
			return fmt.Errorf("memory: body contains sensitive pattern (%q); memory files are plaintext, not a secret store", m)
		}
	}
	return nil
}

// factPath 返回事实文件路径。subject 已过 subjectRe 校验（无分隔符）。
func factPath(root, subject string) string {
	return filepath.Join(root, subject+".md")
}

// WriteFact 创建或覆盖一条事实：覆盖先把旧文件移入 .archive/（留痕，
// 永不真删），再写新文件，最后重建索引。返回 created 表示本次是新建。
func WriteFact(root string, f *Fact, overwrite bool) (created bool, err error) {
	if err := ValidateFact(f); err != nil {
		return false, err
	}
	path := factPath(root, f.Subject)
	_, statErr := os.Stat(path)
	existed := statErr == nil
	if existed && !overwrite {
		return false, fmt.Errorf("memory: subject %q already exists; retry with overwrite=true to update it", f.Subject)
	}
	if existed {
		if err := archiveFile(root, path, f.Subject); err != nil {
			return false, err
		}
	}

	if f.Created == "" {
		f.Created = time.Now().Format("2006-01-02")
	}
	f.LastConfirmed = time.Now().Format("2006-01-02")

	if err := os.MkdirAll(root, 0755); err != nil {
		return false, fmt.Errorf("memory: create root: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, renderFact(f), 0644); err != nil {
		return false, fmt.Errorf("memory: write fact: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return false, fmt.Errorf("memory: commit fact: %w", err)
	}
	return !existed, GenerateIndex(root)
}

// UpdateFact 编辑一条事实的正文（设置页记忆面板）：保留
// type/source/written_by，LastConfirmed 刷新，旧值归档留痕。
func UpdateFact(root, subject, body string) error {
	facts, err := ListFacts(root)
	if err != nil {
		return err
	}
	var target *Fact
	for _, f := range facts {
		if f.Subject == subject {
			target = f
			break
		}
	}
	if target == nil {
		return fmt.Errorf("memory: subject %q not found", subject)
	}
	target.Body = body
	_, err = WriteFact(root, target, true)
	return err
}

// ForgetFact 删除一条事实（移入 .archive/，可恢复，永不真删）并重建索引。
func ForgetFact(root, subject string) error {
	path := factPath(root, subject)
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("memory: subject %q not found", subject)
	}
	if err := archiveFile(root, path, subject); err != nil {
		return err
	}
	return GenerateIndex(root)
}

// archiveFile 把文件移入 .archive/（文件名带时间戳，同主题多代共存）。
func archiveFile(root, path, subject string) error {
	dir := filepath.Join(root, archiveDirName)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("memory: create archive dir: %w", err)
	}
	dst := filepath.Join(dir, fmt.Sprintf("%s-%d.md", subject, time.Now().UnixMilli()))
	if err := os.Rename(path, dst); err != nil {
		return fmt.Errorf("memory: archive %q: %w", subject, err)
	}
	return nil
}

// renderFact 渲染事实文件全文（yaml.Marshal 保证转义正确）。
func renderFact(f *Fact) []byte {
	fm, _ := yaml.Marshal(factFrontmatter{
		Type:          string(f.Type),
		Subject:       f.Subject,
		Created:       f.Created,
		LastConfirmed: f.LastConfirmed,
		ExpiresAt:     f.ExpiresAt,
		Source:        f.Source,
		WrittenBy:     f.WrittenBy,
	})
	var b strings.Builder
	b.WriteString("---\n")
	b.Write(fm)
	b.WriteString("---\n\n")
	b.WriteString(strings.TrimSpace(f.Body))
	b.WriteByte('\n')
	return []byte(b.String())
}

// parseFact 解析事实文件（--- 分隔的 YAML frontmatter + 正文）。
func parseFact(raw []byte) (*Fact, error) {
	text := strings.TrimPrefix(string(raw), "\uFEFF")
	if !strings.HasPrefix(text, "---") {
		return nil, fmt.Errorf("memory: missing frontmatter")
	}
	lines := strings.Split(text, "\n")
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end == -1 {
		return nil, fmt.Errorf("memory: frontmatter not closed")
	}
	var fm factFrontmatter
	if err := yaml.Unmarshal([]byte(strings.Join(lines[1:end], "\n")), &fm); err != nil {
		return nil, fmt.Errorf("memory: frontmatter parse: %w", err)
	}
	return &Fact{
		Type:          FactType(fm.Type),
		Subject:       fm.Subject,
		Created:       fm.Created,
		LastConfirmed: fm.LastConfirmed,
		ExpiresAt:     fm.ExpiresAt,
		Source:        fm.Source,
		WrittenBy:     fm.WrittenBy,
		Body:          strings.TrimSpace(strings.Join(lines[end+1:], "\n")),
	}, nil
}

// ListFacts 扫描 root 列出全部事实（含过期项；索引与召回另做排除）。
// 解析失败的文件跳过（用户手编出错不拖垮整个记忆库）。
func ListFacts(root string) ([]*Fact, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("memory: read root: %w", err)
	}
	var out []*Fact
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") || e.Name() == indexFile {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(root, e.Name()))
		if err != nil {
			continue
		}
		f, err := parseFact(raw)
		if err != nil {
			continue
		}
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Subject < out[j].Subject })
	return out, nil
}

// Expired 报告事实是否已过有效期（软遗忘判定：排除出索引与召回，不删除）。
func (f *Fact) Expired() bool {
	return f.ExpiresAt != "" && f.ExpiresAt < time.Now().Format("2006-01-02")
}

// GenerateIndex 从事实文件重建 MEMORY.md 索引（派生品，可随时重建）。
// 过期事实不进索引。索引行：- [type] subject — 首行正文（created）
func GenerateIndex(root string) error {
	facts, err := ListFacts(root)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0755); err != nil {
		return fmt.Errorf("memory: create root: %w", err)
	}
	path := filepath.Join(root, indexFile)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(renderIndexLines(facts)), 0644); err != nil {
		return fmt.Errorf("memory: write index: %w", err)
	}
	return os.Rename(tmp, path)
}

// renderIndexLines 渲染索引行（过期项跳过）。GenerateIndex 与运行时
// 注入共用同一格式——冻结：同输入同字节。
func renderIndexLines(facts []*Fact) string {
	var b strings.Builder
	for _, f := range facts {
		if f.Expired() {
			continue
		}
		firstLine, _, _ := strings.Cut(f.Body, "\n")
		fmt.Fprintf(&b, "- [%s] %s — %s（%s）\n", f.Type, f.Subject, firstLine, f.Created)
	}
	return b.String()
}

// ListEffectiveFacts 合并项目级与全局事实并应用遮蔽规则
// （Reasonix：同 subject 项目级遮蔽等价全局）。过期项排除。
// 结果顺序：项目级在前（subject 升序），全局未被遮蔽项随后。
func ListEffectiveFacts(projectRoot, globalRoot string) ([]*Fact, error) {
	proj, err := ListFacts(projectRoot)
	if err != nil {
		return nil, err
	}
	glob, err := ListFacts(globalRoot)
	if err != nil {
		return nil, err
	}
	out := make([]*Fact, 0, len(proj)+len(glob))
	shadowed := make(map[string]bool, len(proj))
	for _, f := range proj {
		if f.Expired() {
			continue
		}
		shadowed[f.Subject] = true
		out = append(out, f)
	}
	for _, f := range glob {
		if f.Expired() || shadowed[f.Subject] {
			continue
		}
		out = append(out, f)
	}
	return out, nil
}

// ListArchived 列出 .archive/ 留痕（forget/覆盖的旧值——审计视图用）。
func ListArchived(root string) ([]*Fact, error) {
	return listFactsIn(filepath.Join(root, archiveDirName))
}

// ListRejected 列出 .rejected/ 拒绝审计（面板审计视图用）。
func ListRejected(root string) ([]*Fact, error) {
	return listFactsIn(filepath.Join(root, rejectedDirName))
}

// ReadIndex 读取索引内容（不存在/为空返回空串——调用方据此省略注入块）。
func ReadIndex(root string) string {
	raw, err := os.ReadFile(filepath.Join(root, indexFile))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

// --- 候选区（P3 采纳制：压缩联动产出的建议，用户采纳前不生效） ---

func candidatePath(root, subject string) string {
	return filepath.Join(root, candidatesDirName, subject+".md")
}

// WriteCandidate 写入一条候选。幂等去重（重复提议静默跳过，返回
// proposed=false）：已是正式事实 / 已在候选区 / 曾被拒绝（subject 或
// 正文相同）都不再提出。敏感模式校验与正式事实同标准。
func WriteCandidate(root string, f *Fact) (proposed bool, err error) {
	if err := ValidateFact(f); err != nil {
		return false, err
	}
	if _, err := os.Stat(factPath(root, f.Subject)); err == nil {
		return false, nil // 已是正式事实
	}
	if _, err := os.Stat(candidatePath(root, f.Subject)); err == nil {
		return false, nil // 已在候选区
	}
	if rejected, err := isRejected(root, f.Subject, f.Body); err != nil {
		return false, err
	} else if rejected {
		return false, nil // 拒绝审计：不再重复提议
	}

	dir := filepath.Join(root, candidatesDirName)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return false, fmt.Errorf("memory: create candidates dir: %w", err)
	}
	if f.Created == "" {
		f.Created = time.Now().Format("2006-01-02")
	}
	f.LastConfirmed = f.Created
	if err := os.WriteFile(candidatePath(root, f.Subject), renderFact(f), 0644); err != nil {
		return false, fmt.Errorf("memory: write candidate: %w", err)
	}
	return true, nil
}

// ListCandidates 列出候选区全部建议（解析失败的跳过）。
func ListCandidates(root string) ([]*Fact, error) {
	return listFactsIn(filepath.Join(root, candidatesDirName))
}

// RejectCandidate 拒绝一条候选：移入 .rejected/（审计——此后同 subject
// 或同正文的提议不再出现）。
func RejectCandidate(root, subject string) error {
	src := candidatePath(root, subject)
	if _, err := os.Stat(src); err != nil {
		return fmt.Errorf("memory: candidate %q not found", subject)
	}
	dir := filepath.Join(root, rejectedDirName)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("memory: create rejected dir: %w", err)
	}
	dst := filepath.Join(dir, subject+".md")
	if err := os.Rename(src, dst); err != nil {
		return fmt.Errorf("memory: reject candidate %q: %w", subject, err)
	}
	return nil
}

// AdoptCandidate 采纳一条候选：转为正式事实（触发索引重建）并移出候选区。
// subject 已是正式事实时报错（面板引导用户先编辑既有事实）。
func AdoptCandidate(root, subject string) error {
	raw, err := os.ReadFile(candidatePath(root, subject))
	if err != nil {
		return fmt.Errorf("memory: candidate %q not found", subject)
	}
	f, err := parseFact(raw)
	if err != nil {
		return err
	}
	if _, err := WriteFact(root, f, false); err != nil {
		return err
	}
	if err := os.Remove(candidatePath(root, subject)); err != nil {
		return fmt.Errorf("memory: remove adopted candidate: %w", err)
	}
	return nil
}

// isRejected 检查拒绝审计：同 subject 或同正文（去空白比较）视为已拒绝。
func isRejected(root, subject, body string) (bool, error) {
	rejected, err := listFactsIn(filepath.Join(root, rejectedDirName))
	if err != nil {
		return false, err
	}
	norm := strings.Join(strings.Fields(body), " ")
	for _, f := range rejected {
		if f.Subject == subject {
			return true, nil
		}
		if strings.Join(strings.Fields(f.Body), " ") == norm {
			return true, nil
		}
	}
	return false, nil
}

// listFactsIn 解析目录下全部事实文件（ListFacts 的内部共享版，不触碰索引）。
func listFactsIn(dir string) ([]*Fact, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("memory: read dir %s: %w", dir, err)
	}
	var out []*Fact
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		f, err := parseFact(raw)
		if err != nil {
			continue
		}
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Subject < out[j].Subject })
	return out, nil
}
