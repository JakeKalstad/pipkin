package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const maxInstructionBytes = 32 * 1024
const maxInstructionFiles = 16

type instructionSettings struct {
	SkipProjectInstructions bool     `json:"skip_project_instructions,omitempty"`
	SystemPrompt            string   `json:"system_prompt,omitempty"`
	Files                   []string `json:"instruction_files,omitempty"`
}

type instructionSource struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}

func instructionSettingsPath(root string) (string, error) {
	path, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.Join(outputDirectory(), ".pipkin", "instructions", fmt.Sprintf("%x.json", sha256.Sum256([]byte(path)))), nil
}

func readLimited(reader io.Reader, limit int) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, int64(limit)+1))
	if err == nil && len(data) > limit {
		err = fmt.Errorf("exceeds the %d-byte limit", limit)
	}
	return data, err
}

func loadInstructionSettings(root string) (instructionSettings, error) {
	var settings instructionSettings
	path, err := instructionSettingsPath(root)
	if err != nil {
		return settings, err
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return settings, nil
	}
	if err != nil {
		return settings, err
	}
	defer f.Close()
	data, err := readLimited(f, 1<<20)
	if err == nil {
		err = json.Unmarshal(data, &settings)
	}
	if err != nil {
		return settings, fmt.Errorf("read saved instructions: %w", err)
	}
	return settings, nil
}

func saveInstructionSettings(root string, settings instructionSettings) error {
	if _, _, err := resolveInstructions(root, settings, instructionSettings{}); err != nil {
		return err
	}
	path, err := instructionSettingsPath(root)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "instructions-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	err = json.NewEncoder(f).Encode(settings)
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		return errors.Join(err, closeErr)
	}
	return os.Rename(f.Name(), path)
}

func readInstructionFile(root, rel string) (string, error) {
	ext := strings.ToLower(filepath.Ext(rel))
	if !safeRel(rel) || filepath.ToSlash(filepath.Clean(rel)) != rel ||
		!hiddenPathAllowed(rel, true) || (ext != ".md" && ext != ".markdown") {
		return "", fmt.Errorf("instruction file must be a project-relative Markdown path: %q", rel)
	}
	info, err := statSelectedPath(root, rel)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("instruction file must be a regular file")
	}
	f, err := os.Open(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return "", err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return "", errors.New("instruction file changed while opening")
	}
	data, err := readLimited(f, maxInstructionBytes)
	return string(data), err
}

func resolveInstructions(root string, saved, request instructionSettings) (string, []instructionSource, error) {
	if len(saved.Files)+len(request.Files) > maxInstructionFiles {
		return "", nil, fmt.Errorf("use at most %d instruction files per delivery", maxInstructionFiles)
	}
	var result strings.Builder
	var sources []instructionSource
	add := func(name, body string) error {
		body = strings.TrimSpace(body)
		if !utf8.ValidString(body) || strings.ContainsRune(body, 0) {
			return fmt.Errorf("instruction source %q must contain UTF-8 text", name)
		}
		if body == "" {
			return nil
		}
		if len(body) > maxInstructionBytes-result.Len() {
			return fmt.Errorf("combined instructions exceed %d bytes", maxInstructionBytes)
		}
		fmt.Fprintf(&result, "\n\n--- %s ---\n%s", name, body)
		if result.Len() > maxInstructionBytes {
			return fmt.Errorf("combined instructions exceed %d bytes", maxInstructionBytes)
		}
		sources = append(sources, instructionSource{Name: name, SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(body)))})
		return nil
	}
	if err := add("Workspace system prompt", saved.SystemPrompt); err != nil {
		return "", nil, err
	}
	seen := make(map[string]bool)
	if !saved.SkipProjectInstructions {
		body, err := readInstructionFile(root, projectInstructionsFile)
		if err == nil {
			if err := add(projectInstructionsFile, body); err != nil {
				return "", nil, err
			}
			seen[projectInstructionsFile] = true
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", nil, fmt.Errorf("read %s: %w", projectInstructionsFile, err)
		}
	}
	for _, files := range [][]string{saved.Files, request.Files} {
		for _, rel := range files {
			if seen[rel] {
				continue
			}
			seen[rel] = true
			body, err := readInstructionFile(root, rel)
			if err != nil {
				return "", nil, fmt.Errorf("read instruction file %q: %w", rel, err)
			}
			if err := add(rel, body); err != nil {
				return "", nil, err
			}
		}
	}
	if err := add("Delivery system prompt", request.SystemPrompt); err != nil {
		return "", nil, err
	}
	return result.String(), sources, nil
}

func (c *Config) prepareInstructions(root string) ([]instructionSource, error) {
	settings, err := loadInstructionSettings(root)
	if err != nil {
		return nil, err
	}
	if !c.supportsAgentic() {
		settings.SkipProjectInstructions = true
	}
	guidance, sources, err := resolveInstructions(root, settings, c.instructionRequest)
	if err != nil {
		return nil, err
	}
	if guidance != "" && !c.supportsAgentic() {
		return nil, errors.New("system instructions require a text model or hosted archive provider")
	}
	c.instructionText = guidance
	return sources, nil
}

func (c Config) withInstructions(base string) string {
	if c.instructionText == "" {
		return base
	}
	return base + "\n\nFollow the explicitly supplied project guidance below. Pipkin's output format, file scope, and tool constraints above take precedence if guidance conflicts." + c.instructionText
}
