package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// loadDotEnv reads KEY=value pairs without replacing existing process variables.
// No shell execution, expansion, or third-party packages.
func loadDotEnv(path string) error {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 4096), 1024*1024)
	for line := 1; s.Scan(); line++ {
		raw := strings.TrimSpace(s.Text())
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		raw = strings.TrimSpace(strings.TrimPrefix(raw, "export "))
		key, value, ok := strings.Cut(raw, "=")
		if !ok {
			return fmt.Errorf("%s:%d: expected KEY=value", path, line)
		}
		key = strings.TrimSpace(key)
		if !validEnvKey(key) {
			return fmt.Errorf("%s:%d: invalid environment variable name", path, line)
		}
		value = strings.TrimSpace(value)
		if len(value) > 1 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
			value = value[1 : len(value)-1]
		} else if i := strings.Index(value, " #"); i >= 0 {
			value = strings.TrimSpace(value[:i])
		}
		if _, exists := os.LookupEnv(key); !exists {
			if err := os.Setenv(key, value); err != nil {
				return err
			}
		}
	}
	return s.Err()
}
func validEnvKey(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if !(r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || (i > 0 && r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}
func loadEnvironment() error {
	// A custom file is explicit. Otherwise load the global config and then the
	// project .env. Existing process values always win, so project values cannot
	// override global configuration or an exported secret.
	if p := strings.TrimSpace(os.Getenv("PIPKIN_ENV_FILE")); p != "" {
		return loadDotEnv(p)
	}
	home, _ := os.UserHomeDir()
	if err := loadDotEnv(filepath.Join(home, ".config", "pipkin", ".env")); err != nil {
		return err
	}
	return loadDotEnv(".env")
}
