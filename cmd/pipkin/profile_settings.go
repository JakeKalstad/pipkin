package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const maxProfileConfigBytes = 512 << 10

// Expose profiles and the global editor preference while preserving unrelated config.
type profileSettings struct {
	FreePromptTools  []string                   `json:"free_prompt_tools"`
	Editor           string                     `json:"editor"`
	ProviderProfile  string                     `json:"provider_profile"`
	ProviderProfiles map[string]ProviderProfile `json:"provider_profiles"`
	AgentProfile     string                     `json:"agent_profile"`
	AgentProfiles    map[string]AgentProfile    `json:"agent_profiles"`
	SystemPrompt     string                     `json:"system_prompt"`
}

func readProfileConfig(path string) ([]byte, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return []byte("{}"), nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return readLimited(f, maxProfileConfigBytes)
}

func profileRevision(data []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func profileDraft() (string, string, error) {
	data, err := readProfileConfig(configPath())
	if err != nil {
		return "", "", err
	}
	var settings profileSettings
	if err := json.Unmarshal(data, &settings); err != nil {
		return "", "", err
	}
	if settings.ProviderProfiles == nil {
		settings.ProviderProfiles = map[string]ProviderProfile{}
	}
	if settings.AgentProfiles == nil {
		settings.AgentProfiles = map[string]AgentProfile{}
	}
	if strings.TrimSpace(settings.SystemPrompt) == "" {
		settings.SystemPrompt = defaultProviderSystemPrompt
	}
	for name, profile := range settings.ProviderProfiles {
		if strings.TrimSpace(profile.SystemPrompt) == "" {
			profile.SystemPrompt = settings.SystemPrompt
			settings.ProviderProfiles[name] = profile
		}
	}
	cfg := Config{AgentProfiles: settings.AgentProfiles}
	if settings.AgentProfile == "" {
		settings.AgentProfile = "project"
	}
	for _, project := range projectProfiles {
		name := "project:" + project.ID
		if _, exists := settings.AgentProfiles[name]; !exists {
			settings.AgentProfiles[name] = defaultProjectAgent(project.ID)
		}
	}
	for _, name := range []string{"builtin", "focused", "thorough"} {
		if _, exists := settings.AgentProfiles[name]; !exists {
			settings.AgentProfiles[name], _ = cfg.agentProfileByName(name)
		}
	}
	for name, profile := range settings.AgentProfiles {
		if strings.TrimSpace(profile.SystemPrompt) == "" {
			profile.SystemPrompt = defaultAgentSystemPrompt
			settings.AgentProfiles[name] = profile
		}
	}
	draft, err := json.MarshalIndent(settings, "", "  ")
	return string(draft), profileRevision(data), err
}

func validateProfileSettings(settings profileSettings) error {
	if err := validateFreePromptTools(settings.FreePromptTools); err != nil {
		return err
	}
	if _, err := configuredEditor(settings.Editor); err != nil {
		return err
	}
	validPrompt := func(prompt string) bool {
		return len(prompt) <= maxInstructionBytes && utf8.ValidString(prompt) && !strings.ContainsRune(prompt, 0)
	}
	if !validPrompt(settings.SystemPrompt) {
		return errors.New("default system prompt must contain at most 32768 bytes of UTF-8 text")
	}
	if settings.ProviderProfile != "" {
		if _, exists := settings.ProviderProfiles[settings.ProviderProfile]; !exists {
			return fmt.Errorf("default provider profile %q is missing", settings.ProviderProfile)
		}
	}
	for name, profile := range settings.ProviderProfiles {
		if err := validateFreePromptTools(profile.FreePromptTools); err != nil {
			return fmt.Errorf("provider profile %q: %w", name, err)
		}
		provider := Config{Provider: profile.Provider}
		if strings.TrimSpace(name) == "" || strings.TrimSpace(profile.Model) == "" ||
			(!provider.supportsAgentic() && profile.Provider != "multipart") {
			return fmt.Errorf("provider profile %q requires a name, supported provider, and model", name)
		}
		if profile.BaseURL != "" {
			base, err := url.Parse(profile.BaseURL)
			if err != nil || base.Hostname() == "" || base.User != nil || (base.Scheme != "http" && base.Scheme != "https") {
				return fmt.Errorf("provider profile %q needs an HTTP or HTTPS base URL without credentials", name)
			}
		}
		if profile.MaxContextBytes < 0 || profile.MaxOutputTokens < 0 || !validPrompt(profile.SystemPrompt) {
			return fmt.Errorf("invalid context limit, output limit, or system prompt in provider profile %q", name)
		}
	}
	cfg := Config{AgentProfiles: settings.AgentProfiles}
	if settings.AgentProfile != "" && settings.AgentProfile != "project" {
		if _, err := cfg.agentProfileByName(settings.AgentProfile); err != nil {
			return err
		}
	}
	for name, profile := range settings.AgentProfiles {
		if strings.TrimSpace(name) == "" || !validPrompt(profile.SystemPrompt) || !validPrompt(profile.VerificationFocus) || !validPrompt(profile.PlanFocus) || !validPrompt(profile.SecondAuditFocus) {
			return fmt.Errorf("invalid name or system prompt in agent profile %q", name)
		}
		if _, err := cfg.agentProfileByName(name); err != nil {
			return err
		}
	}
	return nil
}

func saveProfileDraft(draft, revision string) error {
	if len(draft) > maxProfileConfigBytes || !utf8.ValidString(draft) || !strings.HasPrefix(strings.TrimSpace(draft), "{") {
		return errors.New("profile settings must be a UTF-8 JSON object of at most 512 KiB")
	}
	var settings profileSettings
	decoder := json.NewDecoder(strings.NewReader(draft))
	decoder.DisallowUnknownFields()
	if !json.Valid([]byte(draft)) {
		return errors.New("invalid profile settings JSON")
	}
	if err := decoder.Decode(&settings); err != nil {
		return err
	}
	if err := validateProfileSettings(settings); err != nil {
		return err
	}
	path := configPath()
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		path = resolved
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return withFileLock(path+".lock", func() error {
		return writeProfileSettings(path, revision, settings)
	})
}

func writeProfileSettings(path, revision string, settings profileSettings) error {
	current, err := readProfileConfig(path)
	if err != nil {
		return err
	}
	if profileRevision(current) != revision {
		return errors.New("config changed since opening the editor. Reload profiles before saving")
	}
	merged := make(map[string]json.RawMessage)
	if err := json.Unmarshal(current, &merged); err != nil {
		return err
	}
	if merged == nil {
		merged = make(map[string]json.RawMessage)
	}
	profileJSON, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(profileJSON, &fields); err != nil {
		return err
	}
	for key, value := range fields {
		merged[key] = value
	}
	data, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return err
	}
	if len(data)+1 > maxProfileConfigBytes {
		return errors.New("merged config exceeds 512 KiB")
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".pipkin-config-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(append(data, '\n'))
	if err := errors.Join(err, f.Close()); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func (a *App) editProfiles(reader *bufio.Reader, restore func()) string {
	draft, revision, err := profileDraft()
	if err != nil {
		return safeDisplayLine(err.Error())
	}
	original := draft
	for {
		edited, err := editTerminalDocument(a.cwd, "*.json", draft, maxProfileConfigBytes, restore)
		if err != nil {
			return safeDisplayLine(err.Error())
		}
		if string(edited) == original {
			return ""
		}
		draft = string(edited)
		if err = saveProfileDraft(draft, revision); err == nil {
			return "Profiles saved"
		}
		screenLinesANSI([]string{" PIPKIN / PROFILES", " " + safeDisplayLine(err.Error()), " e Edit draft  Esc cancel"})
		if readKey(reader) != "e" {
			return ""
		}
	}
}
