package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestMkDocsWebExport(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PIPKIN_OUTPUT_DIR", t.TempDir())
	t.Setenv("PIPKIN_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	t.Setenv("PIPKIN_PROVIDER", "vllm")
	t.Setenv("PIPKIN_PROVIDER_PROFILE", "")
	t.Setenv("PIPKIN_MODEL", "test-model")
	t.Setenv("LANGFUSE_BASE_URL", "")
	if err := os.WriteFile(filepath.Join(root, "research.md"), []byte("Source evidence: 15 observations."), 0600); err != nil {
		t.Fatal(err)
	}
	if err := saveProject(root, "research"); err != nil {
		t.Fatal(err)
	}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		for _, term := range []string{"Source evidence: 15 observations.", "Write a finding summary", "Track each claim with its source"} {
			if !strings.Contains(string(raw), term) {
				t.Errorf("provider request missing %q", term)
			}
		}
		site := mkdocsSite{Title: "Research: “Sample”", Pages: []mkdocsPage{
			{"index", "Overview", "# Overview\n\nSee [findings](findings.md)."},
			{"findings", "Findings", "# Findings\n\nThe source records 15 observations."},
		}}
		data, _ := json.Marshal(site)
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{
			map[string]any{"message": map[string]string{"content": string(data)}},
		}})
	}))
	defer provider.Close()
	t.Setenv("PIPKIN_BASE_URL", provider.URL)
	handler := webHandlerForRoot(root)
	page := httptest.NewRecorder()
	handler.ServeHTTP(page, httptest.NewRequest("GET", "/", nil))
	token := regexp.MustCompile(`name="csrf" value="([^"]+)"`).FindStringSubmatch(page.Body.String())
	if len(token) != 2 {
		t.Fatal("missing file-picker token")
	}
	form := url.Values{"csrf": {token[1]}, "prompt": {"Write a finding summary"}, "files": {"research.md"}}
	req := httptest.NewRequest("POST", "/mkdocs", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != 200 || res.Header().Get("Content-Type") != "application/zip" {
		t.Fatalf("export returned %d: %s", res.Code, res.Body.String())
	}
	archive, err := zip.NewReader(bytes.NewReader(res.Body.Bytes()), int64(res.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	extracted := t.TempDir()
	expected := map[string]bool{"mkdocs.yml": true, "requirements.txt": true, "README.md": true, "docs/index.md": true, "docs/findings.md": true}
	for _, entry := range archive.File {
		if !expected[entry.Name] {
			t.Fatalf("unexpected member %q", entry.Name)
		}
		delete(expected, entry.Name)
		file, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		content, err := io.ReadAll(file)
		file.Close()
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(extracted, entry.Name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, content, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if len(expected) != 0 {
		t.Fatalf("missing members: %v", expected)
	}
	if err := exec.Command("python3", "-m", "mkdocs", "--version").Run(); err == nil {
		build := exec.Command("python3", "-m", "mkdocs", "build", "--strict")
		build.Dir = extracted
		if out, err := build.CombinedOutput(); err != nil {
			t.Fatalf("MkDocs strict build: %v %s", err, out)
		}
		if _, err := os.Stat(filepath.Join(extracted, "site", "findings", "index.html")); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMkDocsRejectsUnsafePageNames(t *testing.T) {
	for _, slug := range []string{"../outside", "index", "guide.yml", "nested/guide"} {
		site := mkdocsSite{"Site", []mkdocsPage{{"index", "Home", "# Home"}, {slug, "Guide", "# Guide"}}}
		if err := validateMkDocsSite(site); err == nil {
			t.Errorf("accepted unsafe or duplicate slug %q", slug)
		}
	}
}
