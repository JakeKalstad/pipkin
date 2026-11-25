package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const projectMetadata = ".pipkin-project.json"

type projectProfile struct {
	ID       string
	Label    string
	Guidance string
	Starter  string
}

var projectProfiles = []projectProfile{
	{"general", "General", "Define success in terms of the requested deliverable. Verify its completeness and label any unconfirmed assumptions.", "# Project\n\nDescribe the goal, expected outputs, and how to verify them.\n"},
	{"software", "Existing software", "Work through normal entry points. Preserve compatibility and data, and check the changed behavior with focused regression coverage.", "# Software project\n\nDocument setup, build, test, and deployment commands.\n"},
	{"software-new", "New software", "Define a small first milestone and a runnable entry point. Choose the stack from the user's constraints. Include a minimal check and document how to run it.", "# New software project\n\n## Goal\n\n## First runnable milestone\n\n## Setup and checks\n"},
	{"research", "Research", "State the research question and scope. Track each claim with its source, date, method, and uncertainty. Distinguish evidence, interpretation, and open questions. Record enough detail to reproduce calculations.", "# Research project\n\n## Question\n\n## Sources and evidence\n\n## Method\n\n## Open questions\n"},
	{"pitch", "Grant, marketing, seed, or sales package", "Identify the audience, decision, and submission constraints. Lead with the problem, evidence, proposed outcome, and specific ask. Label assumptions and projections. Check claims, numbers, and eligibility against the supplied material.", "# Pitch or application\n\n## Audience and decision\n\n## Problem and evidence\n\n## Proposal and ask\n\n## Deadlines and requirements\n"},
	{"job-search", "Job search", "Compare roles against location eligibility, remote policy, compensation, and industry constraints. Record the source, date, and application status. Tailor materials to verified experience and flag gaps without inventing qualifications.", "# Job search\n\n## Role and location filters\n\n## Compensation and exclusions\n\n## Opportunities\n\n## Follow-ups\n"},
}

type projectInfo struct {
	Profile string `json:"profile"`
	Source  string `json:"source"`
}

func profileByID(id string) (projectProfile, bool) {
	for _, profile := range projectProfiles {
		if profile.ID == id {
			return profile, true
		}
	}
	return projectProfile{}, false
}

func detectProject(root string) string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return "general"
	}
	for _, entry := range entries {
		switch entry.Name() {
		case "go.mod", "Cargo.toml", "package.json", "mix.exs", "pyproject.toml", "setup.py", "requirements.txt", "pom.xml", "build.gradle", "build.gradle.kts", "Gemfile", "composer.json", "CMakeLists.txt", "deno.json", "pubspec.yaml", "Package.swift":
			if entry.Type().IsRegular() {
				return "software"
			}
		}
		if entry.Type().IsRegular() && slices.Contains([]string{".sln", ".csproj", ".go", ".rs", ".ex", ".py", ".ts", ".js"}, filepath.Ext(entry.Name())) {
			return "software"
		}
	}
	// These workspace names are hints only when no software marker was found.
	name := strings.ToLower(filepath.Base(root))
	for _, hint := range []struct {
		terms   []string
		profile string
	}{
		{[]string{"job-search", "job-hunt", "applications"}, "job-search"},
		{[]string{"grant", "pitch", "fundraise", "marketing", "sales"}, "pitch"},
		{[]string{"research", "literature-review", "study"}, "research"},
	} {
		for _, term := range hint.terms {
			if name == term || strings.HasPrefix(name, term+"-") || strings.HasSuffix(name, "-"+term) {
				return hint.profile
			}
		}
	}
	return "general"
}

func readProject(root string) (projectInfo, error) {
	path := filepath.Join(root, projectMetadata)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return projectInfo{detectProject(root), "detected"}, nil
	}
	if err != nil {
		return projectInfo{}, err
	}
	if !info.Mode().IsRegular() {
		return projectInfo{}, errors.New("project metadata must be a regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return projectInfo{}, err
	}
	defer f.Close()
	data, err := readLimited(f, 4096)
	if err != nil {
		return projectInfo{}, err
	}
	var meta projectInfo
	if err := json.Unmarshal(data, &meta); err != nil {
		return meta, fmt.Errorf("invalid %s: %w", projectMetadata, err)
	}
	if _, ok := profileByID(meta.Profile); !ok {
		return meta, fmt.Errorf("unknown project profile %q", meta.Profile)
	}
	return meta, nil
}

func saveProject(root, id string) error {
	if _, ok := profileByID(id); !ok {
		return fmt.Errorf("unknown project profile %q", id)
	}
	return writeProject(root, projectInfo{id, "selected"})
}

func writeProject(root string, info projectInfo) error {
	path := filepath.Join(root, projectMetadata)
	f, err := os.CreateTemp(root, ".pipkin-project-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	err = json.NewEncoder(f).Encode(info)
	if err = errors.Join(err, f.Close()); err != nil {
		return err
	}
	if info.Source == "detected" {
		// Exclusive publication preserves a concurrent explicit selection.
		err := os.Link(f.Name(), path)
		if errors.Is(err, os.ErrExist) {
			return nil
		}
		return err
	}
	return os.Rename(f.Name(), path)
}

func ensureProject(root string) error {
	if _, err := os.Lstat(filepath.Join(root, projectMetadata)); !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return writeProject(root, projectInfo{detectProject(root), "detected"})
}

func initializeProject(root, id string) error {
	profile, ok := profileByID(id)
	if !ok {
		return fmt.Errorf("unknown project profile %q", id)
	}
	if err := os.MkdirAll(root, 0755); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	inside := exec.CommandContext(ctx, "git", "rev-parse", "--is-inside-work-tree")
	inside.Dir = root
	result, err := inside.Output()
	if err != nil || strings.TrimSpace(string(result)) != "true" {
		cmd := exec.CommandContext(ctx, "git", "init")
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git init: %w %s", err, strings.TrimSpace(string(out)))
		}
	}
	if err := saveProject(root, id); err != nil {
		return err
	}
	readme, err := os.OpenFile(filepath.Join(root, "README.md"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = readme.WriteString(profile.Starter)
	return errors.Join(err, readme.Close())
}

func (a *App) projectTUI(reader *bufio.Reader) {
	meta, err := readProject(a.cwd)
	if err != nil {
		a.status = safeDisplayLine(err.Error())
	}
	index := slices.IndexFunc(projectProfiles, func(p projectProfile) bool { return p.ID == meta.Profile })
	index = max(0, index)
	for {
		lines := []string{" PIPKIN / PROJECT PROFILE", " ↑↓ choose  Enter save profile  i initialise repository  q back", ""}
		for i, p := range projectProfiles {
			marker := "   "
			if i == index {
				marker = " › "
			}
			label := p.Label
			if p.ID == meta.Profile {
				label += " (" + meta.Source + ")"
			}
			lines = append(lines, marker+safeDisplayLine(label))
		}
		lines = append(lines, "", " "+safeDisplayLine(a.status))
		screenLinesANSI(lines)
		switch key := readKey(reader); key {
		case "q", "esc":
			return
		case "j", "down":
			index = min(index+1, len(projectProfiles)-1)
		case "k", "up":
			index = max(0, index-1)
		case "enter", "i":
			keyProfile := projectProfiles[index].ID
			// Both actions use the same persisted profile as the web and CLI.
			// Initialisation also creates Git metadata and a starter README if needed.
			action := saveProject
			if key == "i" {
				action = initializeProject
			}
			if err := action(a.cwd, keyProfile); err != nil {
				a.status = err.Error()
				continue
			}
			meta = projectInfo{keyProfile, "selected"}
			a.status = "Saved " + projectProfiles[index].Label
		}
	}
}
