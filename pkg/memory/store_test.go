package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWriteFact_CreateAndDuplicate(t *testing.T) {
	root := t.TempDir()
	created, err := WriteFact(root, &Fact{Type: FactUser, Subject: "prefer-pnpm", Body: "偏好 pnpm 而非 npm"}, false)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if !created {
		t.Error("first write should report created=true")
	}

	// frontmatter 回读（格式单一事实源）
	facts, err := ListFacts(root)
	if err != nil || len(facts) != 1 {
		t.Fatalf("list: %v, %d", err, len(facts))
	}
	if facts[0].Subject != "prefer-pnpm" || facts[0].Type != FactUser || facts[0].Body != "偏好 pnpm 而非 npm" {
		t.Errorf("fact mismatch: %+v", facts[0])
	}
	if facts[0].Created == "" || facts[0].LastConfirmed == "" {
		t.Error("dates should be filled")
	}

	// 同 subject 未显式 overwrite 必须失败（白名单细则）
	if _, err := WriteFact(root, &Fact{Type: FactUser, Subject: "prefer-pnpm", Body: "v2"}, false); err == nil {
		t.Error("duplicate subject without overwrite must fail")
	}
}

func TestWriteFact_OverwriteArchivesOld(t *testing.T) {
	root := t.TempDir()
	if _, err := WriteFact(root, &Fact{Type: FactUser, Subject: "prefer-pnpm", Body: "v1"}, false); err != nil {
		t.Fatal(err)
	}
	created, err := WriteFact(root, &Fact{Type: FactUser, Subject: "prefer-pnpm", Body: "v2"}, true)
	if err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	if created {
		t.Error("overwrite should report created=false")
	}

	// 旧值留痕 .archive/，永不真删
	archives, err := os.ReadDir(filepath.Join(root, archiveDirName))
	if err != nil || len(archives) != 1 {
		t.Fatalf("archive dir should contain the old value: %v", err)
	}
	raw, _ := os.ReadFile(filepath.Join(root, archiveDirName, archives[0].Name()))
	if !strings.Contains(string(raw), "v1") {
		t.Error("archived file should contain the old body")
	}

	facts, _ := ListFacts(root)
	if len(facts) != 1 || facts[0].Body != "v2" {
		t.Error("active fact should be the new value")
	}
}

func TestWriteFact_SensitiveRejected(t *testing.T) {
	root := t.TempDir()
	cases := []string{
		"我的 key 是 sk-abcdefghijklmnop1234",
		"api_key=abcdefgh12345678",
		"密码是 hunter2",
		"邮箱 a.b@example.com",
		"身份证 11010119900307123X",
	}
	for _, body := range cases {
		if _, err := WriteFact(root, &Fact{Type: FactUser, Subject: "s-test", Body: body}, false); err == nil {
			t.Errorf("sensitive body should be rejected: %q", body)
		}
	}
}

func TestWriteFact_Validation(t *testing.T) {
	root := t.TempDir()
	if _, err := WriteFact(root, &Fact{Type: "bogus", Subject: "s", Body: "b"}, false); err == nil {
		t.Error("invalid type should fail")
	}
	if _, err := WriteFact(root, &Fact{Type: FactUser, Subject: "has space", Body: "b"}, false); err == nil {
		t.Error("subject with space should fail")
	}
	if _, err := WriteFact(root, &Fact{Type: FactUser, Subject: "ok", Body: strings.Repeat("x", maxFactBodyBytes+1)}, false); err == nil {
		t.Error("oversized body should fail")
	}
}

func TestIndexGenerationAndExpiry(t *testing.T) {
	root := t.TempDir()
	if _, err := WriteFact(root, &Fact{Type: FactUser, Subject: "aaa", Body: "甲"}, false); err != nil {
		t.Fatal(err)
	}
	// 过期事实：直接写文件模拟（WriteFact 不管 expiry 语义）
	expired := &Fact{Type: FactUser, Subject: "old", Body: "旧事", ExpiresAt: time.Now().AddDate(0, 0, -1).Format("2006-01-02")}
	if _, err := WriteFact(root, expired, false); err != nil {
		t.Fatal(err)
	}

	idx := ReadIndex(root)
	if !strings.Contains(idx, "aaa") {
		t.Error("index should contain the active fact")
	}
	if strings.Contains(idx, "old") {
		t.Error("index must exclude expired facts (soft forgetting)")
	}
	if !expired.Expired() {
		t.Error("yesterday expiry should report Expired()")
	}
}

// --- 遮蔽规则（P4：同 subject 项目级遮蔽全局） ---

func TestListEffectiveFacts_Shadowing(t *testing.T) {
	proj, glob := t.TempDir(), t.TempDir()
	// 同名：项目级遮蔽全局
	if _, err := WriteFact(proj, &Fact{Type: FactUser, Subject: "editor", Body: "项目里用 vim"}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteFact(glob, &Fact{Type: FactUser, Subject: "editor", Body: "全局用 vscode"}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteFact(glob, &Fact{Type: FactUser, Subject: "timezone", Body: "UTC+8"}, false); err != nil {
		t.Fatal(err)
	}
	// 过期项排除（即使它遮蔽了全局同名）
	if _, err := WriteFact(proj, &Fact{Type: FactProject, Subject: "deadline", Body: "旧截止", ExpiresAt: "2020-01-01"}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteFact(glob, &Fact{Type: FactProject, Subject: "deadline", Body: "全局参考"}, false); err != nil {
		t.Fatal(err)
	}

	facts, err := ListEffectiveFacts(proj, glob)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, f := range facts {
		got[f.Subject] = f.Body
	}
	if got["editor"] != "项目里用 vim" {
		t.Errorf("project should shadow global: %q", got["editor"])
	}
	if got["timezone"] != "UTC+8" {
		t.Errorf("unshadowed global should survive: %q", got["timezone"])
	}
	if got["deadline"] != "全局参考" {
		t.Errorf("expired project fact must not shadow: %q", got["deadline"])
	}
}

// --- 候选区（采纳制） ---

func TestWriteCandidate_Dedup(t *testing.T) {
	root := t.TempDir()
	cand := &Fact{Type: FactFeedback, Subject: "use-pnpm", Body: "以后都用 pnpm"}

	proposed, err := WriteCandidate(root, cand)
	if err != nil || !proposed {
		t.Fatalf("first proposal should land: %v, %v", proposed, err)
	}
	// 重复提议静默跳过
	if proposed, _ := WriteCandidate(root, cand); proposed {
		t.Error("duplicate candidate should be skipped")
	}

	// 已是正式事实的 subject 不再提议
	if _, err := WriteFact(root, &Fact{Type: FactUser, Subject: "prefer-dark", Body: "偏好深色"}, false); err != nil {
		t.Fatal(err)
	}
	if proposed, _ := WriteCandidate(root, &Fact{Type: FactFeedback, Subject: "prefer-dark", Body: "以后用深色主题"}); proposed {
		t.Error("subject already a fact should be skipped")
	}

	// 敏感模式与正式事实同标准
	if _, err := WriteCandidate(root, &Fact{Type: FactFeedback, Subject: "s", Body: "密码是 hunter2"}); err == nil {
		t.Error("sensitive candidate should be rejected")
	}
}

func TestRejectCandidate_AuditBlocksReproposal(t *testing.T) {
	root := t.TempDir()
	cand := &Fact{Type: FactFeedback, Subject: "use-pnpm", Body: "以后都用 pnpm"}
	if _, err := WriteCandidate(root, cand); err != nil {
		t.Fatal(err)
	}
	if err := RejectCandidate(root, "use-pnpm"); err != nil {
		t.Fatalf("reject: %v", err)
	}

	// 同 subject 不再提议
	if proposed, _ := WriteCandidate(root, cand); proposed {
		t.Error("rejected subject must not be re-proposed")
	}
	// 同正文（不同 subject）也不再提议
	if proposed, _ := WriteCandidate(root, &Fact{Type: FactFeedback, Subject: "pnpm-always", Body: "以后都用 pnpm"}); proposed {
		t.Error("rejected body must not be re-proposed under another subject")
	}
	// 新内容仍可提议
	if proposed, _ := WriteCandidate(root, &Fact{Type: FactFeedback, Subject: "use-gofmt", Body: "提交前一律 gofmt"}); !proposed {
		t.Error("fresh content should still be proposed")
	}

	if err := RejectCandidate(root, "nonexistent"); err == nil {
		t.Error("rejecting a missing candidate should fail")
	}
}

func TestAdoptCandidate_BecomesFact(t *testing.T) {
	root := t.TempDir()
	if _, err := WriteCandidate(root, &Fact{Type: FactFeedback, Subject: "use-pnpm", Body: "以后都用 pnpm", Source: "archive://turn_1-2.md"}); err != nil {
		t.Fatal(err)
	}
	if err := AdoptCandidate(root, "use-pnpm"); err != nil {
		t.Fatalf("adopt: %v", err)
	}

	// 进入正式事实与索引
	facts, _ := ListFacts(root)
	if len(facts) != 1 || facts[0].Subject != "use-pnpm" {
		t.Fatalf("adopted candidate should become a fact: %+v", facts)
	}
	if !strings.Contains(ReadIndex(root), "use-pnpm") {
		t.Error("adopted fact must enter the index")
	}
	// 移出候选区
	if cands, _ := ListCandidates(root); len(cands) != 0 {
		t.Error("adopted candidate must leave the candidate area")
	}
	// 溯源保留
	if facts[0].Source != "archive://turn_1-2.md" {
		t.Errorf("source should be preserved: %q", facts[0].Source)
	}

	// subject 已是正式事实时报错（防覆盖）
	if _, err := WriteCandidate(root, &Fact{Type: FactFeedback, Subject: "other", Body: "新建议"}); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteFact(root, &Fact{Type: FactUser, Subject: "other", Body: "已存在"}, false); err != nil {
		t.Fatal(err)
	}
	if err := AdoptCandidate(root, "other"); err == nil {
		t.Error("adopting over an existing fact must fail")
	}
}

func TestForgetFactArchives(t *testing.T) {
	root := t.TempDir()
	if _, err := WriteFact(root, &Fact{Type: FactProject, Subject: "use-gorm", Body: "项目用 GORM"}, false); err != nil {
		t.Fatal(err)
	}
	if err := ForgetFact(root, "use-gorm"); err != nil {
		t.Fatalf("forget: %v", err)
	}
	if ReadIndex(root) != "" {
		t.Error("forgotten fact must leave the index")
	}
	archives, _ := os.ReadDir(filepath.Join(root, archiveDirName))
	if len(archives) != 1 {
		t.Error("forgotten fact must be archived, not deleted")
	}
	if err := ForgetFact(root, "nonexistent"); err == nil {
		t.Error("forgetting a missing subject should fail")
	}
}
