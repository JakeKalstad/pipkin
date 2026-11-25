package main

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func outputFolder(path string) string {
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		return path
	}
	return filepath.Dir(path)
}

func outputFolderURL(path string) string {
	abs, err := filepath.Abs(outputFolder(path))
	if err != nil {
		return ""
	}
	return (&url.URL{Scheme: "file", Path: strings.TrimSuffix(filepath.ToSlash(abs), "/") + "/"}).String()
}

func terminalLink(label, target string) string {
	if target == "" {
		return label
	}
	return "\x1b]8;;" + target + "\x1b\\" + label + "\x1b]8;;\x1b\\"
}

func openOutputFolder(path string) error {
	dir, err := filepath.Abs(outputFolder(path))
	if err != nil {
		return err
	}
	opener := "xdg-open"
	if runtime.GOOS == "darwin" {
		opener = "open"
	}
	cmd := exec.Command(opener, dir)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("open output folder with %s: %w", opener, err)
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
