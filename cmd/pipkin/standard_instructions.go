package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

const projectInstructionsFile = "AGENTS.md"
const skillsDirectory = ".agents/skills"

var skillNamePattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

const legacyProjectInstructionsTemplate = `# Project instructions

## Build and test

- Record the build command and the smallest relevant test command.
- Report which checks ran and which could not run.

## Working in this project

- Follow existing architecture, naming, and formatting conventions.
- Keep changes focused and preserve existing behaviour unless the task requires a change.
- Update nearby documentation when a public interface changes.

## Review

- Check for missed requirements, regressions, and unrelated edits.
`

const projectInstructionsTemplate = `# Project instructions

## Understand the task and the supplied context

- Identify the expected behaviour and the entry points, callers, and tests affected by the request.
- Follow the project's existing patterns. Use conventions and commands found in the supplied README, build files, CI configuration, and tests.
- If a missing file or requirement blocks a reliable answer, name what you need and explain why. Keep assumptions explicit when you can proceed.

## Make focused changes

- Reuse existing helpers and interfaces. Preserve compatibility for callers and saved data unless the task explicitly changes it.
- Keep edits within the requested scope. Update tests, API or MCP schemas, and usage documentation when their behaviour changes.
- Handle failure paths and retries where relevant. Keep credentials out of source, examples, and output.

## Verify and review

- Run the smallest relevant check documented by the project when the environment allows it.
- Trace user-facing changes through their normal entry points. Review the patch for regressions, incomplete wiring, and unrelated changes.
- Report the outcome, the checks actually run and their results, and anything that still needs verification.
`

func projectGuidance(root string) (text, source string, err error) {
	text, err = readInstructionFile(root, projectInstructionsFile)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", "", err
	}
	normalised := strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	if normalised == "" || normalised == strings.TrimSpace(legacyProjectInstructionsTemplate) {
		return projectInstructionsTemplate, "Built-in default", nil
	}
	return text, projectInstructionsFile, nil
}

func standardInstructionPath(rel string) (string, bool) {
	if rel == projectInstructionsFile {
		return "", true
	}
	parts := strings.Split(rel, "/")
	if len(parts) == 4 && parts[0]+"/"+parts[1] == skillsDirectory && parts[3] == "SKILL.md" &&
		len(parts[2]) <= 64 && skillNamePattern.MatchString(parts[2]) {
		return parts[2], true
	}
	return "", false
}

func standardInstructionDraft(root, rel string) (string, error) {
	name, ok := standardInstructionPath(rel)
	if !ok {
		return "", errors.New("choose AGENTS.md or .agents/skills/<name>/SKILL.md")
	}
	if name == "" {
		text, _, err := projectGuidance(root)
		return text, err
	}
	text, err := readInstructionFile(root, rel)
	if !errors.Is(err, os.ErrNotExist) {
		return text, err
	}
	return fmt.Sprintf("---\nname: %s\ndescription: Apply the %s workflow when a task explicitly calls for it.\n---\n\n# %s\n\n## Workflow\n\n1. Identify the trigger, expected outcome, and relevant files.\n2. Inspect the normal entry point and its callers before changing behaviour.\n3. Check the primary path and one relevant failure or boundary case.\n4. Report findings with file references, checks run, and any missing evidence.\n", name, name, name), nil
}

func workspaceSkills(root string) []string {
	path := filepath.Join(root, skillsDirectory)
	info, err := statSelectedPath(root, skillsDirectory)
	if err != nil || !info.IsDir() {
		return nil
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil
	}
	var skills []string
	for _, entry := range entries {
		rel := skillsDirectory + "/" + entry.Name() + "/SKILL.md"
		if _, ok := standardInstructionPath(rel); !ok || !entry.IsDir() {
			continue
		}
		if fi, err := statSelectedPath(root, rel); err == nil && fi.Mode().IsRegular() {
			skills = append(skills, rel)
		}
	}
	return skills
}

func saveStandardInstruction(root, rel, text string) error {
	if _, ok := standardInstructionPath(rel); !ok {
		return errors.New("invalid instruction path")
	}
	if len(text) > maxInstructionBytes || !utf8.ValidString(text) || strings.ContainsRune(text, 0) {
		return errors.New("instructions must be UTF-8 text within 32 KiB")
	}
	// Walk one component at a time so an existing symlink is never used as a directory.
	target := root
	parts := strings.Split(rel, "/")
	for _, part := range parts[:len(parts)-1] {
		target = filepath.Join(target, part)
		if err := os.Mkdir(target, 0755); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		info, err := os.Lstat(target)
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return errors.New("instruction directory must be a real directory")
		}
	}
	path := filepath.Join(target, parts[len(parts)-1])
	mode := os.FileMode(0644)
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return errors.New("instruction target must be a regular file")
		}
		mode = info.Mode().Perm()
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f, err := os.CreateTemp(target, ".pipkin-instructions-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	err = f.Chmod(mode)
	if err == nil {
		_, err = f.WriteString(text)
	}
	if err = errors.Join(err, f.Close()); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func toggleStandardSkill(root, rel string) error {
	name, ok := standardInstructionPath(rel)
	if !ok || name == "" {
		return errors.New("choose a skill to toggle")
	}
	settings, err := loadInstructionSettings(root)
	if err != nil {
		return err
	}
	if i := slices.Index(settings.Files, rel); i >= 0 {
		settings.Files = slices.Delete(settings.Files, i, i+1)
	} else {
		settings.Files = append(settings.Files, rel)
	}
	return saveInstructionSettings(root, settings)
}

func (a *App) editStandardInstructions(reader *bufio.Reader, restore func()) error {
	paths := append([]string{projectInstructionsFile}, workspaceSkills(a.cwd)...)
	settings, err := loadInstructionSettings(a.cwd)
	if err != nil {
		return err
	}
	index, offset := 0, 0
	var name, notice string
	naming := false
	for {
		height, _ := size()
		rows := max(1, height-5)
		offset = max(min(offset, index), index-rows+1)
		lines := []string{" PIPKIN / INSTRUCTIONS", " ↑↓ choose  Enter edit  Space enable/disable  n new skill  q back", " " + notice}
		for i := offset; i < min(len(paths), offset+rows); i++ {
			marker := "   "
			if i == index {
				marker = " › "
			}
			label := paths[i]
			if i > 0 {
				label, _ = standardInstructionPath(paths[i])
				if slices.Contains(settings.Files, paths[i]) {
					label += " (active)"
				} else {
					label += " (disabled)"
				}
			}
			lines = append(lines, marker+label)
		}
		if naming {
			lines = append(lines, " New skill name (Enter to create, Esc to cancel): "+name)
		}
		screenLinesANSI(lines)
		key := readKey(reader)
		rel := ""
		if naming {
			switch key {
			case "esc":
				naming, name = false, ""
			case "enter":
				rel = skillsDirectory + "/" + name + "/SKILL.md"
				if _, ok := standardInstructionPath(rel); !ok {
					notice = "Use lowercase words separated by hyphens."
					continue
				}
			case "\x7f", "\b":
				if len(name) > 0 {
					name = name[:len(name)-1]
				}
			default:
				if len(name) < 64 && len(key) == 1 && strings.Contains("abcdefghijklmnopqrstuvwxyz0123456789-", key) {
					name += key
				}
			}
		} else {
			switch key {
			case "q", "esc":
				return nil
			case "up", "k":
				index = max(0, index-1)
			case "down", "j":
				index = min(len(paths)-1, index+1)
			case " ":
				if index > 0 {
					if err := toggleStandardSkill(a.cwd, paths[index]); err != nil {
						return err
					}
					settings, err = loadInstructionSettings(a.cwd)
					if err != nil {
						return err
					}
				}
			case "n":
				naming = true
			case "enter":
				rel = paths[index]
			}
		}
		if rel == "" {
			continue
		}
		saved, err := editInstructionInTerminal(a.cwd, rel, restore)
		naming, name = false, ""
		if err != nil {
			notice = safeDisplayLine(err.Error())
			continue
		}
		paths = append([]string{projectInstructionsFile}, workspaceSkills(a.cwd)...)
		if found := slices.Index(paths, rel); found >= 0 {
			index = found
		}
		notice = "No changes to " + rel
		if saved {
			notice = "Saved " + rel
		}
	}
}

func editInstructionInTerminal(root, rel string, restore func()) (bool, error) {
	original, readErr := readInstructionFile(root, rel)
	draft, err := standardInstructionDraft(root, rel)
	if err != nil {
		return false, err
	}
	text, err := editTerminalDocument(root, "*.md", draft, maxInstructionBytes, restore)
	if err != nil || (readErr == nil && string(text) == original) {
		return false, err
	}
	err = saveStandardInstruction(root, rel, string(text))
	return err == nil, err
}

func configuredEditor(name string) ([]string, error) {
	switch name {
	case "", "auto":
		return nil, nil
	case "vscodium":
		return []string{"codium", "--wait"}, nil
	default:
		return nil, fmt.Errorf("unknown editor %q (use auto or vscodium)", name)
	}
}

func terminalEditor() ([]string, error) {
	data, err := readProfileConfig(configPath())
	if err != nil {
		return nil, err
	}
	var settings struct {
		Editor string `json:"editor"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, err
	}
	editor, err := configuredEditor(settings.Editor)
	if err != nil || len(editor) > 0 {
		return editor, err
	}
	for _, name := range []string{"VISUAL", "EDITOR"} {
		if command := strings.Fields(os.Getenv(name)); len(command) > 0 {
			return command, nil
		}
	}
	return []string{"vi"}, nil
}

func editTerminalDocument(root, pattern, draft string, limit int, restore func()) ([]byte, error) {
	f, err := os.CreateTemp("", "pipkin-"+pattern)
	if err != nil {
		return nil, err
	}
	defer os.Remove(f.Name())
	_, err = f.WriteString(draft)
	if err = errors.Join(err, f.Close()); err != nil {
		return nil, err
	}
	editor, err := terminalEditor()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(editor[0], append(editor[1:], f.Name())...)
	if cmd.Err != nil {
		return nil, fmt.Errorf("launch editor %q: %w", editor[0], cmd.Err)
	}
	cmd.Dir, cmd.Stdin, cmd.Stdout, cmd.Stderr = root, os.Stdin, os.Stdout, os.Stderr
	restore()
	err = cmd.Run()
	_, rawErr := stty("raw", "-echo")
	fmt.Print("\x1b[?25l")
	if err = errors.Join(err, rawErr); err != nil {
		return nil, err
	}
	edited, err := os.Open(f.Name())
	if err != nil {
		return nil, err
	}
	defer edited.Close()
	return readLimited(edited, limit)
}
