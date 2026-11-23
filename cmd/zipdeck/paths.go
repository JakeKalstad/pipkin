package main

import (
	"os"
	"path/filepath"
	"strings"
)

// outputDirectory is shared by the CLI, TUI and MCP server. Override with
// ZIPDECK_OUTPUT_DIR; otherwise use ~/zipdeck/outputs.
func outputDirectory() string {
	if custom := strings.TrimSpace(os.Getenv("ZIPDECK_OUTPUT_DIR")); custom != "" {
		if p, err := filepath.Abs(custom); err == nil {
			return p
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "zipdeck", "outputs")
	}
	return filepath.Join(home, "zipdeck", "outputs")
}
