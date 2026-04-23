package skills

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- helpers ---

func writeSkillMD(t *testing.T, base, category, name, content string) {
	t.Helper()
	dir := filepath.Join(base, name)
	if category != "" {
		dir = filepath.Join(base, category, name)
	}
	os.MkdirAll(dir, 0755)
	os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0644)
}

func wf(t *testing.T, path, content string) {
	t.Helper()
	os.WriteFile(path, []byte(content), 0644)
}

func mustLoad(t *testing.T, dirs []string, filter []string) []Skill {
	t.Helper()
	s, err := LoadCatalog(context.Background(), dirs, filter)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func byName(skills []Skill) map[string]Skill {
	m := make(map[string]Skill, len(skills))
	for _, s := range skills {
		m[s.Name] = s
	}
	return m
}

// --- LoadCatalog ---

func TestLoadCatalog_BasicScenarios(t *testing.T) {
	t.Run("empty dir", func(t *testing.T) {
		if s := mustLoad(t, []string{t.TempDir()}, nil); len(s) != 0 {
			t.Fatalf("expected 0, got %d", len(s))
		}
	})
	t.Run("nonexistent dir", func(t *testing.T) {
		if s := mustLoad(t, []string{filepath.Join(t.TempDir(), "nope")}, nil); len(s) != 0 {
			t.Fatalf("expected 0, got %d", len(s))
		}
	})
	t.Run("ignores other extensions", func(t *testing.T) {
		d := t.TempDir()
		writeSkillMD(t, d, "", "good", "---\nname: good\ndescription: good\n---\nGood skill")
		wf(t, filepath.Join(d, "bad.txt"), "not a skill")
		wf(t, filepath.Join(d, "also_bad.json"), "{}")
		if s := mustLoad(t, []string{d}, nil); len(s) != 1 || s[0].Name != "good" {
			t.Fatalf("expected [good], got %v", s)
		}
	})
	t.Run("dot dirs skipped", func(t *testing.T) {
		d := t.TempDir()
		os.MkdirAll(filepath.Join(d, ".git", "leaked"), 0755)
		wf(t, filepath.Join(d, ".git", "leaked", "SKILL.md"), "---\nname: leaked\ndescription: bad\n---\nbody")
		writeSkillMD(t, d, "", "real", "---\nname: real\ndescription: ok\n---\nbody")
		if s := mustLoad(t, []string{d}, nil); len(s) != 1 || s[0].Name != "real" {
			t.Fatalf("expected [real], got %v", s)
		}
	})
	t.Run("dot-file directories ignored", func(t *testing.T) {
		d := t.TempDir()
		os.MkdirAll(filepath.Join(d, ".hidden"), 0755)
		os.MkdirAll(filepath.Join(d, ".secret"), 0755)
		writeSkillMD(t, d, "", "visible", "---\nname: visible\ndescription: ok\n---\nbody")
		if s := mustLoad(t, []string{d}, nil); len(s) != 1 || s[0].Name != "visible" {
			t.Fatalf("expected [visible], got %v", s)
		}
	})
}

func TestLoadCatalog_NewFormat(t *testing.T) {
	dir := t.TempDir()
	writeSkillMD(t, dir, "", "code-review",
		"---\nname: code-review\ndescription: Review code.\nmetadata:\n  author: cobot\n  version: \"1.0\"\n---\n\n# Code Review\n")
	s := mustLoad(t, []string{dir}, nil)
	if len(s) != 1 {
		t.Fatalf("expected 1, got %d", len(s))
	}
	sk := s[0]
	if sk.Name != "code-review" || sk.Source != "global" || sk.Category != "" {
		t.Errorf("name=%q source=%q category=%q", sk.Name, sk.Source, sk.Category)
	}
	if sk.Description != "Review code." || sk.Metadata == nil || sk.Metadata["author"] != "cobot" {
		t.Errorf("desc=%q metadata=%v", sk.Description, sk.Metadata)
	}
}

func TestLoadCatalog_Category(t *testing.T) {
	dir := t.TempDir()
	writeSkillMD(t, dir, "coding", "review", "---\nname: review\ndescription: Review.\n---\n\n# Review\n")
	s := mustLoad(t, []string{dir}, nil)
	if len(s) != 1 || s[0].Category != "coding" {
		t.Fatalf("got %v", s)
	}
}

func TestLoadCatalog_MergeOverride(t *testing.T) {
	gDir, wDir := t.TempDir(), t.TempDir()
	writeSkillMD(t, gDir, "", "shared", "---\nname: shared\ndescription: Global\n---\nGlobal")
	writeSkillMD(t, gDir, "", "only-global", "---\nname: only-global\ndescription: Only global\n---\nOnly global")
	writeSkillMD(t, wDir, "", "shared", "---\nname: shared\ndescription: Workspace\n---\nWorkspace")
	m := byName(mustLoad(t, []string{gDir, wDir}, nil))
	if len(m) != 2 {
		t.Fatalf("expected 2, got %d", len(m))
	}
	sh := m["shared"]
	// LoadCatalog returns tier-1 metadata only (no Content).
	// Workspace override and last-match-wins semantics are what this test verifies.
	if sh.Source != "workspace" {
		t.Errorf("shared: source=%q, want workspace", sh.Source)
	}
	if _, ok := m["only-global"]; !ok {
		t.Error("missing only-global")
	}
}

func TestLoadCatalog_Filter(t *testing.T) {
	dir := t.TempDir()
	writeSkillMD(t, dir, "", "a-skill", "---\nname: a-skill\ndescription: A\n---\nA")
	writeSkillMD(t, dir, "", "b-skill", "---\nname: b-skill\ndescription: B\n---\nB")
	writeSkillMD(t, dir, "", "c-skill", "---\nname: c-skill\ndescription: C\n---\nC")
	t.Run("partial filter", func(t *testing.T) {
		m := byName(mustLoad(t, []string{dir}, []string{"a-skill", "c-skill"}))
		if len(m) != 2 {
			t.Fatalf("expected 2, got %d", len(m))
		}
		if _, ok := m["a-skill"]; !ok {
			t.Error("missing a-skill")
		}
		if _, ok := m["b-skill"]; ok {
			t.Error("b-skill should be filtered")
		}
	})
	t.Run("empty filter includes all", func(t *testing.T) {
		if s := mustLoad(t, []string{dir}, []string{}); len(s) != 3 {
			t.Fatalf("expected 3, got %d", len(s))
		}
	})
}

func TestLoadFull(t *testing.T) {
	dir := t.TempDir()
	writeSkillMD(t, dir, "", "my-skill", "---\nname: my-skill\ndescription: test\n---\n\nBody\n")
	sk, err := LoadFull(context.Background(), []string{dir}, "my-skill")
	if err != nil || sk.Name != "my-skill" || !strings.Contains(sk.Content, "Body") {
		t.Fatalf("err=%v name=%q content=%q", err, sk.Name, sk.Content)
	}
	if _, err := LoadFull(context.Background(), []string{dir}, "x"); err == nil {
		t.Error("expected error for missing")
	}
}

func TestListLinkedFiles(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "references"), 0755)
	os.MkdirAll(filepath.Join(dir, "templates"), 0755)
	wf(t, filepath.Join(dir, "references", "a.md"), "a")
	wf(t, filepath.Join(dir, "references", "b.md"), "b")
	wf(t, filepath.Join(dir, "templates", "c.md"), "c")
	f := ListLinkedFiles(dir)
	if len(f["references"]) != 2 || len(f["templates"]) != 1 {
		t.Errorf("got %v", f)
	}
}

func TestReadLinkedFile(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "references"), 0755)
	wf(t, filepath.Join(dir, "references", "api.md"), "docs")
	got, err := ReadLinkedFile(dir, "references/api.md")
	if err != nil || got != "docs" {
		t.Fatalf("got=%q err=%v", got, err)
	}
	for _, tc := range []struct{ path, desc string }{
		{"../etc/passwd", "traversal"}, {"/etc/passwd", "absolute"}, {"other/f", "invalid dir"},
	} {
		if _, err := ReadLinkedFile(dir, tc.path); err == nil {
			t.Errorf("no error for %s", tc.desc)
		}
	}
}

func TestReadLinkedFile_EdgeCases(t *testing.T) {
	t.Run("symlink escape", func(t *testing.T) {
		dir := t.TempDir()
		os.MkdirAll(filepath.Join(dir, "references"), 0755)
		outside := filepath.Join(t.TempDir(), "s.txt")
		wf(t, outside, "x")
		link := filepath.Join(dir, "references", "l.txt")
		if err := os.Symlink(outside, link); err != nil {
			t.Skip(err)
		}
		if _, err := ReadLinkedFile(dir, "references/l.txt"); err == nil {
			t.Error("expected error")
		}
	})
	t.Run("stat error", func(t *testing.T) {
		dir := t.TempDir()
		os.MkdirAll(filepath.Join(dir, "references"), 0755)
		if _, err := ReadLinkedFile(dir, "references/nope.txt"); err == nil {
			t.Error("expected error")
		}
	})
}

func TestSkillsToPrompt(t *testing.T) {
	if SkillsToPrompt(nil) != "" {
		t.Error("expected empty")
	}
	r := SkillsToPrompt([]Skill{
		{Name: "a", Description: "Alpha"},
		{Name: "b", Description: "Beta", Category: "cat"},
	})
	if !strings.Contains(r, "- a: Alpha") || !strings.Contains(r, "cat:\n    - b: Beta") {
		t.Errorf("prompt = %q", r)
	}
}

func TestFindSkillDir(t *testing.T) {
	dir := t.TempDir()
	writeSkillMD(t, dir, "", "new-skill", "---\nname: new-skill\ndescription: test\n---\nbody")
	writeSkillMD(t, dir, "cat", "cat-skill", "---\nname: cat-skill\ndescription: test\n---\nbody")

	tests := []struct {
		name    string
		input   string
		wantRel string
		wantErr bool
	}{
		{"new format", "new-skill", "new-skill", false},
		{"category", "cat-skill", filepath.Join("cat", "cat-skill"), false},
		{"not found", "missing", "", true},
		{"path traversal", "../../etc", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := FindSkillDir(dir, tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			want := tt.wantRel
			if !filepath.IsAbs(want) {
				want = filepath.Join(dir, want)
			}
			if got != want {
				t.Errorf("got %q, want %q", got, want)
			}
		})
	}
}

func TestVerifyContainment(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "references")
	os.MkdirAll(sub, 0755)
	fp := filepath.Join(sub, "f.txt")
	wf(t, fp, "ok")
	if _, err := VerifyContainment(fp, dir); err != nil {
		t.Fatalf("contained file: %v", err)
	}
	outside := filepath.Join(t.TempDir(), "s.txt")
	wf(t, outside, "x")
	link := filepath.Join(sub, "l.txt")
	if err := os.Symlink(outside, link); err != nil {
		t.Skip(err)
	}
	if _, err := VerifyContainment(link, dir); err == nil {
		t.Error("expected escape error")
	}
}

func TestValidateSkillName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid simple", "my-skill", false},
		{"valid two chars", "ab", false},
		{"valid alphanumeric", "skill123", false},
		{"single char too short", "a", true},
		{"empty", "", true},
		{"uppercase", "My-Skill", true},
		{"starts with hyphen", "-skill", true},
		{"ends with hyphen", "skill-", true},
		{"contains space", "my skill", true},
		{"contains slash", "my/skill", true},
		{"too long", strings.Repeat("a", 65), true},
		{"max length valid", strings.Repeat("a", 64), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSkillName(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateSkillName(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestIsValidCategoryName(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"coding", true},
		{"code-review", true},
		{".", false},
		{"..", false},
		{".hidden", false},
		{"../etc", false},
		{"a/b", false},
		{"a\\b", false},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := isValidCategoryName(tt.input); got != tt.want {
				t.Errorf("isValidCategoryName(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestIsPathTraversalSafe(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"references/file.md", true},
		{"templates/output.txt", true},
		{"../etc/passwd", false},
		{"/etc/passwd", false},
		{"\\windows\\system32", false},
		{"scripts/../../etc/passwd", false},
		{"", true},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := IsPathTraversalSafe(tt.input); got != tt.want {
				t.Errorf("IsPathTraversalSafe(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestExtractDescription(t *testing.T) {
	tests := []struct {
		name, content, want string
	}{
		{"h1 heading", "# My Skill\nContent", "My Skill"},
		{"h2 heading", "## Code Review\nContent", "Code Review"},
		{"h3 heading", "### Deep Section\nContent", "Deep Section"},
		{"h4 heading", "#### Very Deep\nContent", "Very Deep"},
		{"no heading", "Just a plain description", "Just a plain description"},
		{"empty lines first", "\n\n## Heading\nContent", "Heading"},
		{"skips frontmatter delimiter", "---\n## Real Title\nContent", "Real Title"},
		{"empty content", "", ""},
		{"only whitespace", "   \n  \n", ""},
		{"heading no space", "##NoSpace\nContent", "##NoSpace"},
		{"heading multiple spaces", "##  Double Space\nContent", "Double Space"},
		{"h1 multiple spaces", "#  Extra\nContent", "Extra"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractDescription(tt.content); got != tt.want {
				t.Errorf("extractDescription(%q) = %q, want %q", tt.content, got, tt.want)
			}
		})
	}
}

func TestEnsureContainedDir(t *testing.T) {
	t.Run("creates new dir", func(t *testing.T) {
		base := t.TempDir()
		target := filepath.Join(base, "references", "sub")
		if err := EnsureContainedDir(target, base); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(target); os.IsNotExist(err) {
			t.Error("directory was not created")
		}
	})
	t.Run("existing dir ok", func(t *testing.T) {
		base := t.TempDir()
		target := filepath.Join(base, "templates")
		os.MkdirAll(target, 0755)
		if err := EnsureContainedDir(target, base); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("traversal rejected", func(t *testing.T) {
		base := t.TempDir()
		if err := EnsureContainedDir(filepath.Join(base, "..", "etc"), base); err == nil {
			t.Error("expected error for path traversal")
		}
	})
	t.Run("intermediate symlink escape rejected", func(t *testing.T) {
		base := t.TempDir()
		outside := t.TempDir()

		link := filepath.Join(base, "references")
		if err := os.Symlink(outside, link); err != nil {
			t.Skipf("symlinks not supported or require elevated privileges: %v", err)
		}

		target := filepath.Join(link, "sub")
		if err := EnsureContainedDir(target, base); err == nil {
			t.Fatal("expected error for intermediate symlink escape")
		}
	})
}
