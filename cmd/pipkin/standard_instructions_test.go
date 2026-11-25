package main

import (
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestStandardInstructionsEditor(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PIPKIN_OUTPUT_DIR", t.TempDir())
	h := webHandlerForRoot(root)
	path := ".agents/skills/review-migrations/SKILL.md"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/instructions/edit?skill=review-migrations", nil))
	token := regexp.MustCompile(`name="csrf" value="([^"]+)"`).FindStringSubmatch(w.Body.String())
	if w.Code != 200 || len(token) != 2 || !strings.Contains(w.Body.String(), "name: review-migrations") {
		t.Fatal("skill template missing")
	}
	post := func(rel, action, markdown string) int {
		form := url.Values{"csrf": {token[1]}, "path": {rel}, "markdown": {markdown}, "action": {action}}
		req := httptest.NewRequest("POST", "/instructions/edit", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w.Code
	}
	if code := post("AGENTS.md", "", "# Project rules"); code != 303 {
		t.Fatalf("save AGENTS.md: %d", code)
	}
	if code := post(path, "", "---\nname: review-migrations\ndescription: Check migrations.\n---\nCheck rollback."); code != 303 {
		t.Fatalf("save skill: %d", code)
	}
	cfg := Config{Provider: "vllm"}
	sources, err := cfg.prepareInstructions(root)
	if err != nil || len(sources) != 4 || sources[1].Name != "AGENTS.md" {
		t.Fatalf("defaults: %v %v", sources, err)
	}
	if code := post(path, "toggle", ""); code != 303 {
		t.Fatalf("enable skill: %d", code)
	}
	sources, err = cfg.prepareInstructions(root)
	if err != nil || len(sources) != 5 || sources[4].Name != path || !strings.Contains(cfg.instructionText, "Check rollback.") {
		t.Fatalf("active skill: %v %v", sources, err)
	}
	if code := post("../AGENTS.md", "", "invalid"); code != 422 {
		t.Fatalf("traversal returned %d", code)
	}
	outside := t.TempDir()
	skillDir := filepath.Join(root, skillsDirectory, "linked")
	if err := os.Symlink(outside, skillDir); err != nil {
		t.Fatal(err)
	}
	if code := post(".agents/skills/linked/SKILL.md", "", "invalid"); code != 422 {
		t.Fatalf("symlink returned %d", code)
	}
}

func TestEffectiveProjectGuidance(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PIPKIN_OUTPUT_DIR", t.TempDir())
	for _, content := range []string{"", legacyProjectInstructionsTemplate, "# Custom project notes"} {
		if err := saveStandardInstruction(root, projectInstructionsFile, content); err != nil {
			t.Fatal(err)
		}
		want := "Built-in default"
		if strings.HasPrefix(content, "# Custom") {
			want = projectInstructionsFile
		}
		cfg := Config{Provider: "vllm"}
		sources, err := cfg.prepareInstructions(root)
		if err != nil || len(sources) != 4 || sources[1].Name != want {
			t.Fatalf("effective sources: %v %v", sources, err)
		}
		w := httptest.NewRecorder()
		webHandlerForRoot(root).ServeHTTP(w, httptest.NewRequest("GET", "/instructions", nil))
		preview := "# Custom project notes"
		if want == "Built-in default" {
			preview = "Understand the task and the supplied context"
		}
		if w.Code != 200 || !strings.Contains(w.Body.String(), preview) {
			t.Fatalf("instructions page did not show active guidance for %s", want)
		}
	}
	if err := saveInstructionSettings(root, instructionSettings{SkipProjectInstructions: true}); err != nil {
		t.Fatal(err)
	}
	cfg := Config{Provider: "vllm"}
	if sources, err := cfg.prepareInstructions(root); err != nil || len(sources) != 0 {
		t.Fatalf("opted-out source: %v %v", sources, err)
	}
}

func TestBundledSkillOverrideAndToggle(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PIPKIN_OUTPUT_DIR", t.TempDir())
	path := bundledSkillPath("deliverable-review")
	draft, err := standardInstructionDraft(root, path)
	if err != nil || !strings.Contains(draft, "name: deliverable-review") {
		t.Fatalf("bundled draft unavailable: %v", err)
	}
	if err := saveStandardInstruction(root, path, "Workspace override text."); err != nil {
		t.Fatal(err)
	}
	cfg := Config{Provider: "vllm"}
	check := func(active bool) {
		t.Helper()
		if _, err := cfg.prepareInstructions(root); err != nil ||
			strings.Contains(cfg.instructionText, "Workspace override text.") != active ||
			strings.Contains(cfg.instructionText, "Scoping, verification, and final handoff.") {
			t.Fatalf("skill active=%t: %v", active, err)
		}
	}
	check(true)
	for _, active := range []bool{false, true} {
		if err := toggleStandardSkill(root, path); err != nil {
			t.Fatal(err)
		}
		check(active)
	}
}
