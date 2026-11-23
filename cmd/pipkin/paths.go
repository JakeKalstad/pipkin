package main

import (
	"os"
	"path/filepath"
	"strings"
)

// outputDirectory is shared by the CLI, TUI and MCP server. Override with
// PIPKIN_OUTPUT_DIR; otherwise use ~/pipkin/outputs.
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
