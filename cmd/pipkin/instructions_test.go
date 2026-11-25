package main

import (
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func TestWorkspaceInstructionsWebAndMetadata(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PIPKIN_OUTPUT_DIR", t.TempDir())
	t.Setenv("OPENAI_API_KEY", "private-key-never-render")
	t.Setenv("PIPKIN_VERSION", "test-version")
	path := ".claude/skills/review/SKILL.md"
	abs := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(abs), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte("Review named callers."), 0600); err != nil {
		t.Fatal(err)
	}
	h := webHandlerForRoot(root)
	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		return w
	}
	about := get("/about")
	if about.Code != 200 || !strings.Contains(about.Body.String(), "test-version") ||
		strings.Contains(about.Body.String(), "private-key-never-render") {
		t.Fatal("unexpected About page metadata")
	}
	help := get("/help")
	if help.Code != 200 || !strings.Contains(help.Body.String(), "pipkin_submit") ||
		!strings.Contains(help.Body.String(), "POST /instructions") {
		t.Fatal("help reference missing")
	}
	formPage := get("/instructions")
	token := regexp.MustCompile(`name="csrf" value="([^"]+)"`).FindStringSubmatch(formPage.Body.String())
	if len(token) != 2 {
		t.Fatal("instruction editor did not render")
	}
	post := func(csrf, prompt, files string) int {
		data := url.Values{"csrf": {csrf}, "system_prompt": {prompt}, "instruction_files": {files}}
		r := httptest.NewRequest("POST", "/instructions", strings.NewReader(data.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	if code := post("invalid", "Reject", path); code != 403 {
		t.Fatalf("invalid token returned %d", code)
	}
	if code := post(token[1], "Reject", "../AGENTS.md"); code != 422 {
		t.Fatalf("unsafe path returned %d", code)
	}
	if code := post(token[1], "Prefer small edits.", path); code != 303 {
		t.Fatalf("save returned %d", code)
	}
	if err := os.WriteFile(abs, []byte("Review updated callers."), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := Config{Provider: "vllm", instructionRequest: instructionSettings{
		SystemPrompt: "Check the API boundary.", Files: []string{path},
	}}
	sources, err := cfg.prepareInstructions(root)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, source := range sources {
		names = append(names, source.Name)
	}
	if !reflect.DeepEqual(names, []string{"Workspace system prompt", "Built-in default", "Project profile: General", "Bundled skill: deliverable-review", path, "Delivery system prompt"}) ||
		!strings.Contains(cfg.instructionText, "Review updated callers.") {
		t.Fatalf("resolved guidance: %v, %q", names, cfg.instructionText)
	}
	other := t.TempDir()
	settings, err := loadInstructionSettings(other)
	if err != nil || settings.SystemPrompt != "" || len(settings.Files) != 0 {
		t.Fatal("workspace guidance leaked across projects")
	}
	if code := post(token[1], "", ""); code != 303 {
		t.Fatalf("clear returned %d", code)
	}
	cfg.instructionRequest = instructionSettings{}
	if sources, err := cfg.prepareInstructions(root); err != nil || len(sources) != 3 || sources[0].Name != "Built-in default" {
		t.Fatal("clearing settings did not restore the built-in default")
	}
}
