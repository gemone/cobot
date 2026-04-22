package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cobot-agent/cobot/internal/skills"
)

// newTestSkillsHandler creates a skillsHandler with a temp workspace.
func newTestSkillsHandler(t *testing.T) *skillsHandler {
	t.Helper()
	return &skillsHandler{ws: newTestWorkspace(t)}
}

func skillContent(name, desc, body string) string {
	return "---\nname: " + name + "\ndescription: " + desc + "\n---\n\n" + body + "\n"
}

// --- ValidateContent ---

func TestValidateAndCheckContent_ValidatesContent(t *testing.T) {
	tests := []struct {
		name, content, expectedName string
		wantErr                     bool
		errSubstr                   string
	}{
		{"valid", skillContent("my-skill", "A skill", "body"), "my-skill", false, ""},
		{"missing frontmatter", "just plain text", "x", true, "invalid"},
		{"name mismatch", skillContent("wrong", "D", "body"), "my-skill", true, "does not match"},
		{"no description", "---\nname: s\n---\nbody\n", "s", true, "description is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := skills.ValidateContent(tt.content, tt.expectedName)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && tt.errSubstr != "" && !strings.Contains(err.Error(), tt.errSubstr) {
				t.Errorf("error %q should contain %q", err.Error(), tt.errSubstr)
			}
		})
	}
}

// --- validateAndCheckContent ---

func TestValidateAndCheckContent(t *testing.T) {
	tests := []struct {
		name, content, skillName string
		wantErr                  bool
		errSubstr                string
	}{
		{"valid", skillContent("s", "D", "body"), "s", false, ""},
		{"empty content", "", "s", true, "content is required"},
		{"oversized", strings.Repeat("x", int(skills.MaxSkillFileSize)+1), "s", true, "maximum size"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateAndCheckContent(tt.content, tt.skillName)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && tt.errSubstr != "" && !strings.Contains(err.Error(), tt.errSubstr) {
				t.Errorf("error %q should contain %q", err.Error(), tt.errSubstr)
			}
		})
	}
}

// --- resolveLinkedFile ---

func TestResolveLinkedFile(t *testing.T) {
	h := newTestSkillsHandler(t)
	// Create a skill so findWritableDir works.
	skillDir := filepath.Join(h.ws.SkillsDir(), "my-skill")
	os.MkdirAll(skillDir, 0755)
	os.WriteFile(filepath.Join(skillDir, skills.SkillFile), []byte(skillContent("my-skill", "D", "b")), 0644)

	tests := []struct {
		name, filePath string
		wantErr        bool
		errSubstr      string
	}{
		{"empty path", "", true, "file_path is required"},
		{"traversal", "../etc/passwd", true, "path traversal"},
		{"invalid dir", "other/file.txt", true, "must be under"},
		{"valid", "references/doc.md", false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			skillDir, fullPath, err := h.resolveLinkedFile("my-skill", tt.filePath)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && tt.errSubstr != "" && !strings.Contains(err.Error(), tt.errSubstr) {
				t.Errorf("error %q should contain %q", err.Error(), tt.errSubstr)
			}
			if err == nil {
				if skillDir == "" || fullPath == "" {
					t.Error("expected non-empty paths")
				}
			}
		})
	}
}

// --- executeManage (CRUD) ---

func TestExecuteManage_Create(t *testing.T) {
	h := newTestSkillsHandler(t)
	content := skillContent("test-skill", "A test skill", "Hello world")

	// Create.
	args, _ := json.Marshal(manageParams{
		Action:  "create",
		Name:    "test-skill",
		Content: content,
	})
	got, err := h.executeManage(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "skill created") {
		t.Errorf("unexpected result: %s", got)
	}

	// Verify on disk.
	data, err := os.ReadFile(filepath.Join(h.ws.SkillsDir(), "test-skill", skills.SkillFile))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != content {
		t.Errorf("disk content mismatch: %q", string(data))
	}
}

func TestExecuteManage_CreateWithCategory(t *testing.T) {
	h := newTestSkillsHandler(t)
	args, _ := json.Marshal(manageParams{
		Action:   "create",
		Name:     "review",
		Category: "coding",
		Content:  skillContent("review", "Review code", "body"),
	})
	got, err := h.executeManage(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "skill created") {
		t.Errorf("unexpected: %s", got)
	}
	data, err := os.ReadFile(filepath.Join(h.ws.SkillsDir(), "coding", "review", skills.SkillFile))
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Error("empty file")
	}
}

func TestExecuteManage_CreateDuplicateFails(t *testing.T) {
	h := newTestSkillsHandler(t)
	content := skillContent("dup", "D", "b")
	args, _ := json.Marshal(manageParams{Action: "create", Name: "dup", Content: content})
	if _, err := h.executeManage(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	_, err := h.executeManage(context.Background(), args)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("expected already-exists error, got: %v", err)
	}
}

func TestExecuteManage_CreateCategoryConflictsWithExistingSkill(t *testing.T) {
	h := newTestSkillsHandler(t)
	// Create a top-level skill named "coding".
	codingContent := skillContent("coding", "Coding skill", "body")
	codingArgs, _ := json.Marshal(manageParams{Action: "create", Name: "coding", Content: codingContent})
	if _, err := h.executeManage(context.Background(), codingArgs); err != nil {
		t.Fatal(err)
	}
	// Try to create a skill under category "coding" — should fail because
	// "coding" is already a skill and the scanner would never find nested skills.
	reviewContent := skillContent("review", "Review skill", "body")
	reviewArgs, _ := json.Marshal(manageParams{
		Action: "create", Name: "review", Category: "coding", Content: reviewContent,
	})
	_, err := h.executeManage(context.Background(), reviewArgs)
	if err == nil || !strings.Contains(err.Error(), "a skill with that name already exists") {
		t.Errorf("expected category-conflict error, got: %v", err)
	}
}

func TestExecuteManage_Edit(t *testing.T) {
	h := newTestSkillsHandler(t)
	// Create first.
	createArgs, _ := json.Marshal(manageParams{Action: "create", Name: "ed", Content: skillContent("ed", "Old", "v1")})
	h.executeManage(context.Background(), createArgs)

	// Edit.
	editArgs, _ := json.Marshal(manageParams{Action: "edit", Name: "ed", Content: skillContent("ed", "New", "v2")})
	got, err := h.executeManage(context.Background(), editArgs)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "skill updated") {
		t.Errorf("unexpected: %s", got)
	}
}

func TestExecuteManage_Patch(t *testing.T) {
	h := newTestSkillsHandler(t)
	createArgs, _ := json.Marshal(manageParams{Action: "create", Name: "pt", Content: skillContent("pt", "D", "hello world")})
	h.executeManage(context.Background(), createArgs)

	patchArgs, _ := json.Marshal(manageParams{
		Action: "patch", Name: "pt", OldString: "hello", NewString: "goodbye",
	})
	got, err := h.executeManage(context.Background(), patchArgs)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "skill patched") {
		t.Errorf("unexpected: %s", got)
	}
	data, _ := os.ReadFile(filepath.Join(h.ws.SkillsDir(), "pt", skills.SkillFile))
	if !strings.Contains(string(data), "goodbye world") {
		t.Errorf("patched content wrong: %q", string(data))
	}
}

func TestExecuteManage_PatchDeleteSubstring(t *testing.T) {
	h := newTestSkillsHandler(t)
	createArgs, _ := json.Marshal(manageParams{Action: "create", Name: "pd", Content: skillContent("pd", "D", "hello world")})
	h.executeManage(context.Background(), createArgs)

	// Patch with empty NewString deletes OldString.
	patchArgs, _ := json.Marshal(manageParams{
		Action: "patch", Name: "pd", OldString: "hello ", NewString: "",
	})
	got, err := h.executeManage(context.Background(), patchArgs)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "skill patched") {
		t.Errorf("unexpected: %s", got)
	}
	data, _ := os.ReadFile(filepath.Join(h.ws.SkillsDir(), "pd", skills.SkillFile))
	if !strings.Contains(string(data), "world") {
		t.Errorf("patched content wrong: %q", string(data))
	}
	if strings.Contains(string(data), "hello ") {
		t.Errorf("old_string not deleted: %q", string(data))
	}
}

func TestExecuteManage_PatchInvalidatesFrontmatter(t *testing.T) {
	h := newTestSkillsHandler(t)
	createArgs, _ := json.Marshal(manageParams{Action: "create", Name: "pi", Content: skillContent("pi", "D", "body")})
	h.executeManage(context.Background(), createArgs)

	// Patch that removes the frontmatter name should fail validation.
	patchArgs, _ := json.Marshal(manageParams{
		Action: "patch", Name: "pi", OldString: "name: pi", NewString: "name: wrong",
	})
	_, err := h.executeManage(context.Background(), patchArgs)
	if err == nil {
		t.Error("expected error for patched content with mismatched name")
	}
	if !strings.Contains(err.Error(), "does not match") {
		t.Errorf("expected name-mismatch error, got: %v", err)
	}
}

func TestExecuteManage_PatchOldStringNotFound(t *testing.T) {
	h := newTestSkillsHandler(t)
	createArgs, _ := json.Marshal(manageParams{Action: "create", Name: "pn", Content: skillContent("pn", "D", "body")})
	h.executeManage(context.Background(), createArgs)

	patchArgs, _ := json.Marshal(manageParams{
		Action: "patch", Name: "pn", OldString: "nonexistent", NewString: "x",
	})
	_, err := h.executeManage(context.Background(), patchArgs)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected not-found error, got: %v", err)
	}
}

func TestExecuteManage_Delete(t *testing.T) {
	h := newTestSkillsHandler(t)
	createArgs, _ := json.Marshal(manageParams{Action: "create", Name: "del", Content: skillContent("del", "D", "b")})
	h.executeManage(context.Background(), createArgs)

	delArgs, _ := json.Marshal(manageParams{Action: "delete", Name: "del"})
	got, err := h.executeManage(context.Background(), delArgs)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "skill deleted") {
		t.Errorf("unexpected: %s", got)
	}
	if _, err := os.Stat(filepath.Join(h.ws.SkillsDir(), "del")); !os.IsNotExist(err) {
		t.Error("expected directory to be removed")
	}
}

func TestExecuteManage_WriteAndRemoveFile(t *testing.T) {
	h := newTestSkillsHandler(t)
	createArgs, _ := json.Marshal(manageParams{Action: "create", Name: "wf", Content: skillContent("wf", "D", "b")})
	h.executeManage(context.Background(), createArgs)

	// Write file.
	writeArgs, _ := json.Marshal(manageParams{
		Action: "write_file", Name: "wf", FilePath: "references/doc.md", FileContent: "# Doc",
	})
	got, err := h.executeManage(context.Background(), writeArgs)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "file written") {
		t.Errorf("unexpected: %s", got)
	}
	data, _ := os.ReadFile(filepath.Join(h.ws.SkillsDir(), "wf", "references", "doc.md"))
	if string(data) != "# Doc" {
		t.Errorf("file content: %q", string(data))
	}

	// Remove file.
	removeArgs, _ := json.Marshal(manageParams{
		Action: "remove_file", Name: "wf", FilePath: "references/doc.md",
	})
	got, err = h.executeManage(context.Background(), removeArgs)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "file removed") {
		t.Errorf("unexpected: %s", got)
	}
}

func TestExecuteManage_WriteFile_SymlinkEscape(t *testing.T) {
	h := newTestSkillsHandler(t)
	createArgs, _ := json.Marshal(manageParams{Action: "create", Name: "sym", Content: skillContent("sym", "D", "b")})
	h.executeManage(context.Background(), createArgs)

	// Create a symlink inside references/ pointing outside the skill dir.
	skillDir := filepath.Join(h.ws.SkillsDir(), "sym")
	os.MkdirAll(filepath.Join(skillDir, "references"), 0755)
	outside := filepath.Join(t.TempDir(), "sensitive.txt")
	os.WriteFile(outside, []byte("secret"), 0644)
	link := filepath.Join(skillDir, "references", "escape.md")
	if err := os.Symlink(outside, link); err != nil {
		t.Skip(err)
	}

	// Attempting to write through the symlink should fail.
	writeArgs, _ := json.Marshal(manageParams{
		Action: "write_file", Name: "sym", FilePath: "references/escape.md", FileContent: "pwned",
	})
	_, err := h.executeManage(context.Background(), writeArgs)
	if err == nil {
		t.Error("expected error for symlink escape")
	}
	// Verify the outside file was NOT overwritten.
	data, _ := os.ReadFile(outside)
	if string(data) != "secret" {
		t.Errorf("outside file was modified: %q", string(data))
	}
}

func TestExecuteManage_ErrorCases(t *testing.T) {
	tests := []struct {
		name string
		p    manageParams
		sub  string
	}{
		{"no name", manageParams{Action: "create", Content: skillContent("x", "D", "b")}, "name is required"},
		{"no action", manageParams{Name: "ok-name"}, "action is required"},
		{"unknown action", manageParams{Name: "ok-name", Action: "bogus"}, "unknown action"},
		{"invalid name", manageParams{Name: "BAD", Action: "create"}, "invalid name"},
		{"create no content", manageParams{Name: "nc", Action: "create"}, "content is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args, _ := json.Marshal(tt.p)
			_, err := (&skillsHandler{ws: newTestWorkspace(t)}).executeManage(context.Background(), args)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.sub) {
				t.Errorf("error %q should contain %q", err.Error(), tt.sub)
			}
		})
	}
}

// --- executeView ---

func TestExecuteView(t *testing.T) {
	h := newTestSkillsHandler(t)
	content := skillContent("view-skill", "Viewable", "body content")
	skillDir := filepath.Join(h.ws.SkillsDir(), "view-skill")
	os.MkdirAll(skillDir, 0755)
	os.WriteFile(filepath.Join(skillDir, skills.SkillFile), []byte(content), 0644)

	args, _ := json.Marshal(map[string]string{"name": "view-skill"})
	got, err := h.executeView(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "view-skill") || !strings.Contains(got, "body content") {
		t.Errorf("view output: %q", got)
	}
}

func TestExecuteView_LinkedFile(t *testing.T) {
	h := newTestSkillsHandler(t)
	skillDir := filepath.Join(h.ws.SkillsDir(), "lf")
	os.MkdirAll(filepath.Join(skillDir, "references"), 0755)
	os.WriteFile(filepath.Join(skillDir, skills.SkillFile), []byte(skillContent("lf", "D", "b")), 0644)
	os.WriteFile(filepath.Join(skillDir, "references", "api.md"), []byte("api docs"), 0644)

	args, _ := json.Marshal(map[string]string{"name": "lf", "file_path": "references/api.md"})
	got, err := h.executeView(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if got != "api docs" {
		t.Errorf("got %q", got)
	}
}

func TestExecuteView_MissingName(t *testing.T) {
	h := newTestSkillsHandler(t)
	args, _ := json.Marshal(map[string]string{})
	_, err := h.executeView(context.Background(), args)
	if err == nil || !strings.Contains(err.Error(), "name is required") {
		t.Errorf("expected name-required error, got: %v", err)
	}
}

// --- executeList ---

func TestExecuteList(t *testing.T) {
	h := newTestSkillsHandler(t)
	skillDir := filepath.Join(h.ws.SkillsDir(), "list-skill")
	os.MkdirAll(skillDir, 0755)
	os.WriteFile(filepath.Join(skillDir, skills.SkillFile), []byte(skillContent("list-skill", "Listable", "b")), 0644)

	args, _ := json.Marshal(map[string]string{})
	got, err := h.executeList(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "list-skill") {
		t.Errorf("list output: %q", got)
	}
}

func TestExecuteList_CategoryFilter(t *testing.T) {
	h := newTestSkillsHandler(t)
	// Create skill in "coding" category.
	catSkillDir := filepath.Join(h.ws.SkillsDir(), "coding", "review")
	os.MkdirAll(catSkillDir, 0755)
	os.WriteFile(filepath.Join(catSkillDir, skills.SkillFile), []byte(skillContent("review", "Review skill", "b")), 0644)
	// Create top-level skill.
	topDir := filepath.Join(h.ws.SkillsDir(), "standalone")
	os.MkdirAll(topDir, 0755)
	os.WriteFile(filepath.Join(topDir, skills.SkillFile), []byte(skillContent("standalone", "Standalone skill", "b")), 0644)

	// Filter by "coding" category.
	args, _ := json.Marshal(map[string]string{"category": "coding"})
	got, err := h.executeList(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "review") {
		t.Errorf("expected review in filtered output: %q", got)
	}
	if strings.Contains(got, "standalone") {
		t.Errorf("standalone should be filtered out: %q", got)
	}

	// Filter by non-existent category returns empty list.
	args2, _ := json.Marshal(map[string]string{"category": "nonexistent"})
	got2, err := h.executeList(context.Background(), args2)
	if err != nil {
		t.Fatal(err)
	}
	if got2 != "[]" {
		t.Errorf("expected empty array, got: %q", got2)
	}
}

// --- appendLinkedFiles ---

func TestAppendLinkedFiles(t *testing.T) {
	t.Run("no linked files", func(t *testing.T) {
		dir := t.TempDir()
		var b strings.Builder
		appendLinkedFiles(&b, dir)
		if b.String() != "" {
			t.Errorf("expected empty, got %q", b.String())
		}
	})
	t.Run("with linked files", func(t *testing.T) {
		dir := t.TempDir()
		os.MkdirAll(filepath.Join(dir, "references"), 0755)
		os.WriteFile(filepath.Join(dir, "references", "a.md"), []byte("a"), 0644)
		os.WriteFile(filepath.Join(dir, "references", "b.md"), []byte("b"), 0644)
		var b strings.Builder
		appendLinkedFiles(&b, dir)
		got := b.String()
		if !strings.Contains(got, "## Linked Files") || !strings.Contains(got, "references/") {
			t.Errorf("linked files output: %q", got)
		}
	})
}
