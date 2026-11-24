package main

import (
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestWebPickerDefaultsAndCSRF(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"main.go", ".env", "README.md"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("test"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	h := webHandlerForRoot(root)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	body := w.Body.String()
	if w.Code != 200 || !strings.Contains(body, `value="main.go"`) || strings.Contains(body, `value=".env"`) {
		t.Fatalf("picker response %d", w.Code)
	}
	re := regexp.MustCompile(`name="csrf" value="([a-f0-9]+)"`)
	matches := re.FindStringSubmatch(body)
	if len(matches) != 2 {
		t.Fatal("missing csrf token")
	}
	form := url.Values{"csrf": {"invalid"}, "prompt": {"fix"}, "files": {"main.go"}}
	req := httptest.NewRequest("POST", "/submit", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r := httptest.NewRecorder()
	h.ServeHTTP(r, req)
	if r.Code != 403 {
		t.Fatalf("invalid token allowed: %d", r.Code)
	}
	form.Set("csrf", matches[1])
	form.Set("files", "../outside")
	req = httptest.NewRequest("POST", "/submit", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r = httptest.NewRecorder()
	h.ServeHTTP(r, req)
	if r.Code != 400 {
		t.Fatalf("unsafe selection allowed: %d: %s", r.Code, r.Body.String())
	}
}

func TestWebDecisionsAndCopy(t *testing.T) {
	root, source := testReviewJob(t)
	base := t.TempDir()
	t.Setenv("PIPKIN_OUTPUT_DIR", base)
	dir := filepath.Join(base, "jobs", "sample", "job-test")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"input.zip", "result.zip", "job.json"} {
		b, err := os.ReadFile(filepath.Join(source, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	h := webHandlerForRoot(root)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/review?job=sample/job-test", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "main.go") {
		t.Fatalf("review: %d %s", w.Code, w.Body.String())
	}
	token := regexp.MustCompile(`name="csrf" value="([a-f0-9]+)"`).FindStringSubmatch(w.Body.String())[1]
	post := func(path string, values url.Values) int {
		values.Set("csrf", token)
		req := httptest.NewRequest("POST", path, strings.NewReader(values.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if code := post("/copy", url.Values{"job": {"sample/job-test"}, "confirm": {"COPY"}}); code != 409 {
		t.Fatalf("should require decisions, got %d", code)
	}
	for path, decision := range map[string]string{"main.go": "approve", "added.go": "reject"} {
		if code := post("/decision", url.Values{"job": {"sample/job-test"}, "path": {path}, "action": {decision}}); code != 303 {
			t.Fatalf("decision status %d", code)
		}
	}
	if code := post("/copy", url.Values{"job": {"sample/job-test"}, "confirm": {"COPY"}}); code != 303 {
		t.Fatalf("copy status %d", code)
	}
	b, _ := os.ReadFile(filepath.Join(root, "main.go"))
	if string(b) != "new\n" {
		t.Fatalf("not copied: %s", b)
	}
	if _, err := os.Stat(filepath.Join(root, "added.go")); !os.IsNotExist(err) {
		t.Fatal("rejected file created")
	}
}
