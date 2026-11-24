package main

import (
	"context"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitHistoryViews(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("Git unavailable")
	}
	root := t.TempDir()
	ctx := context.Background()
	run := func(args ...string) string {
		t.Helper()
		out, err := readGit(ctx, root, args...)
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(out)
	}
	write := func(rel, text string) {
		t.Helper()
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	run("init", "-b", "main")
	run("config", "user.name", "Pipkin Test")
	run("config", "user.email", "pipkin@example.test")
	run("config", "commit.gpgsign", "false")
	path := "pkg/a[b]&.go"
	write(path, "base")
	write("pkg/ab&.go", "base")
	write("outside.txt", "base")
	run("add", ".")
	run("commit", "-m", "Base")
	run("update-ref", "refs/remotes/origin/main", "HEAD")
	write(path, "<local>")
	write("pkg/ab&.go", "glob match")
	write("outside.txt", "outside")
	run("add", ".")
	run("commit", "-m", "Local changes")
	write(path, "working")
	h, err := openGitHistory(ctx, filepath.Join(root, "pkg"), "a[b]&.go")
	if err != nil {
		t.Fatal(err)
	}
	if h.scope != path || h.upstreamLabel() != "origin/main" {
		t.Fatalf("unexpected Git scope and upstream: %+v", h)
	}
	commits, err := h.log(ctx, true)
	if err != nil || len(commits) != 1 || commits[0].Subject != "Local changes" {
		t.Fatalf("local log: %+v, %v", commits, err)
	}
	for mode, wanted := range map[string]string{"commit": "+<local>", "upstream": "+<local>", "working": "+working"} {
		patch, err := h.diff(ctx, mode, commits[0].Hash)
		if err != nil || !strings.Contains(patch, wanted) || strings.Contains(patch, "glob match") || strings.Contains(patch, "outside") {
			t.Fatalf("%s diff: %s (%v)", mode, patch, err)
		}
	}
	web := webHandlerForRoot(root)
	w := httptest.NewRecorder()
	web.ServeHTTP(w, httptest.NewRequest("GET", "/git?diff=upstream&path="+url.QueryEscape(path), nil))
	if body := w.Body.String(); w.Code != 200 || !strings.Contains(body, "&#43;&lt;local&gt;") ||
		!strings.Contains(body, "origin/main") || strings.Contains(body, "glob match") || strings.Contains(body, "<form") {
		t.Fatalf("Git page did not contain the scoped read-only patch (status %d)", w.Code)
	}
}
