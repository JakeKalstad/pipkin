package main

import (
	"os"
	"path/filepath"
	"strings"
)

// outputDirectory is shared by the CLI, TUI and MCP server. Override with
// PIPKIN_OUTPUT_DIR; otherwise use ~/pipkin/outputs.
func hiddenName(name string) bool {
	return strings.HasPrefix(name, ".") && name != "."
}

func protectedName(name string, dir bool) bool {
	lower := strings.ToLower(name)
	switch lower {
	case ".git", ".pipkin", ".env":
		return true
	case ".env.example", ".env.sample", ".env.template":
		return dir
	default:
		return strings.HasPrefix(lower, ".env.")
	}
}

func hiddenPathAllowed(rel string, includeHidden bool) bool {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	for i, part := range parts {
		if protectedName(part, i < len(parts)-1) || (!includeHidden && hiddenName(part)) {
			return false
		}
	}
	return true
}

func outputDirectory() string {
	if custom := strings.TrimSpace(os.Getenv("PIPKIN_OUTPUT_DIR")); custom != "" {
		if p, err := filepath.Abs(custom); err == nil {
			return p
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "pipkin", "outputs")
	}
	return filepath.Join(home, "pipkin", "outputs")
}
