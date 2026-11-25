package main

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestWebProfileSettingsSaveAndConflict(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("PIPKIN_CONFIG", config)
	t.Setenv("PIPKIN_PROVIDER_PROFILE", "")
	t.Setenv("PIPKIN_AGENT_PROFILE", "")
	t.Setenv("PIPKIN_OUTPUT_DIR", t.TempDir())
	if err := os.WriteFile(config, []byte(`{"langgraph_python":"/custom/python","max_context_bytes":12345,"unknown":{"keep":true}}`), 0600); err != nil {
		t.Fatal(err)
	}
	handler := webHandlerForRoot(root)
	page := httptest.NewRecorder()
	handler.ServeHTTP(page, httptest.NewRequest("GET", "/profiles", nil))
	value := func(name string) string {
		t.Helper()
		match := regexp.MustCompile(`name="` + name + `" value="([^"]+)"`).FindStringSubmatch(page.Body.String())
		if len(match) != 2 {
			t.Fatalf("profile editor is missing %s", name)
		}
		return match[1]
	}
	token, revision := value("csrf"), value("revision")
	post := func(draft, token string) int {
		form := url.Values{"csrf": {token}, "revision": {revision}, "profiles": {draft}}
		req := httptest.NewRequest("POST", "/profiles", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response.Code
	}
	draft := `{
		"provider_profile":"local",
		"provider_profiles":{"local":{"provider":"vllm","model":"next-model","system_prompt":"Provider guidance","max_context_bytes":4096}},
		"agent_profile":"focused",
		"agent_profiles":{"focused":{"runner":"langgraph","review_passes":2,"system_prompt":"Agent guidance"}},
		"system_prompt":"Default guidance"
	}`
	if status := post(draft, "wrong-token"); status != 403 {
		t.Fatalf("unprotected update returned %d", status)
	}
	if status := post(strings.Replace(draft, `"review_passes":2`, `"review_passes":4`, 1), token); status != 422 {
		t.Fatalf("invalid review passes returned %d", status)
	}
	if status := post(draft, token); status != 303 {
		t.Fatalf("profile save returned %d", status)
	}
	if status := post(draft, token); status != 422 {
		t.Fatalf("stale profile save returned %d", status)
	}

	cfg := loadConfig()
	_, agent, err := cfg.activeAgentProfile()
	if cfg.configError != nil || cfg.Model != "next-model" || cfg.MaxContextBytes != 4096 ||
		cfg.LangGraphPython != "/custom/python" || err != nil || agent.ReviewPasses != 2 {
		t.Fatalf("profile settings were not reloaded: %v / %v", cfg.configError, err)
	}
	_, err = cfg.prepareInstructions(root, profileGuidance{"Agent system prompt", agent.SystemPrompt})
	if err != nil || !strings.Contains(cfg.instructionText, "Provider guidance") || !strings.Contains(cfg.instructionText, "Agent guidance") {
		t.Fatalf("system guidance not applied: %q / %v", cfg.instructionText, err)
	}
	data, err := os.ReadFile(config)
	var persisted map[string]json.RawMessage
	if err != nil || json.Unmarshal(data, &persisted) != nil || persisted["unknown"] == nil ||
		string(persisted["max_context_bytes"]) != "12345" || string(persisted["system_prompt"]) != `"Default guidance"` {
		t.Fatal("unrelated settings or the default prompt were lost")
	}
}

func TestProfileDraftShowsEffectivePromptsAndProjectGraphs(t *testing.T) {
	config := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("PIPKIN_CONFIG", config)
	if err := os.WriteFile(config, []byte(`{"provider_profiles":{"local":{"provider":"vllm","model":"local"},"hosted":{"provider":"openai","model":"fixture","system_prompt":"Custom prompt"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	draft, revision, err := profileDraft()
	var settings profileSettings
	if err != nil || json.Unmarshal([]byte(draft), &settings) != nil {
		t.Fatalf("invalid profile draft: %v", err)
	}
	if settings.AgentProfile != "project" || settings.SystemPrompt == "" ||
		settings.ProviderProfiles["local"].SystemPrompt == "" || settings.ProviderProfiles["hosted"].SystemPrompt != "Custom prompt" {
		t.Fatalf("provider defaults or overrides missing: %+v", settings.ProviderProfiles)
	}
	for _, project := range projectProfiles {
		graph := settings.AgentProfiles["project:"+project.ID]
		if graph.PlanFocus == "" || graph.SystemPrompt == "" || graph.Runner != "auto" {
			t.Errorf("missing editable graph defaults for %s", project.ID)
		}
	}
	if err := saveProfileDraft(draft, revision); err != nil {
		t.Fatal(err)
	}
}
