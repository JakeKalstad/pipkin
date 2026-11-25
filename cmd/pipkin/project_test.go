package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectDetectionAndOverride(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ensureProject(root); err != nil {
		t.Fatal(err)
	}
	got, err := readProject(root)
	if err != nil || got.Profile != "software" || got.Source != "detected" {
		t.Fatalf("detected %+v (%v)", got, err)
	}
	if err := saveProject(root, "research"); err != nil {
		t.Fatal(err)
	}
	if err := ensureProject(root); err != nil {
		t.Fatal(err)
	}
	cfg := Config{Provider: "vllm"}
	if _, err := cfg.prepareInstructions(root); err != nil || !strings.Contains(cfg.instructionText, "Track each claim with its source") {
		t.Fatalf("research guidance missing: %v", err)
	}
	got, err = readProject(root)
	if err != nil || got.Profile != "research" || got.Source != "selected" {
		t.Fatalf("overridden %+v (%v)", got, err)
	}
}

func TestInitialiseProjectPreservesReadme(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("Git unavailable")
	}
	root := filepath.Join(t.TempDir(), "seed-round")
	if err := initializeProject(root, "pitch"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "README.md")
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), "Proposal and ask") {
		t.Fatalf("starter README: %v", err)
	}
	if err := initializeProject(root, "research"); err != nil {
		t.Fatal(err)
	}
	updated, err := os.ReadFile(path)
	if err != nil || string(updated) != string(data) {
		t.Fatal("initialisation replaced the existing README")
	}
}
