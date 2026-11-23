package main

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func zipFixture(t *testing.T, path string, files map[string]string) {
	t.Helper()
	f, e := os.Create(path)
	if e != nil {
		t.Fatal(e)
	}
	w := zip.NewWriter(f)
	for k, v := range files {
		p, e := w.Create(k)
		if e != nil {
			t.Fatal(e)
		}
		_, e = p.Write([]byte(v))
		if e != nil {
			t.Fatal(e)
		}
	}
	if e = w.Close(); e != nil {
		t.Fatal(e)
	}
	if e = f.Close(); e != nil {
		t.Fatal(e)
	}
}
func TestArchiveResponseAndUsage(t *testing.T) {
	tmp := t.TempDir()
	input := filepath.Join(tmp, "in.zip")
	output := filepath.Join(tmp, "out.zip")
	result := filepath.Join(tmp, "expected.zip")
	zipFixture(t, input, map[string]string{"main.go": "old"})
	zipFixture(t, result, map[string]string{"main.go": "new"})
	resultBytes, _ := os.ReadFile(result)
	var uploads int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/files" && r.Method == "POST":
			uploads++
			if e := r.ParseMultipartForm(100 << 20); e != nil {
				t.Error(e)
			}
			fmt.Fprint(w, `{"id":"file-123"}`)
		case r.URL.Path == "/responses":
			var payload map[string]any
			_ = json.NewDecoder(r.Body).Decode(&payload)
			if payload["stream"] != true {
				t.Error("expected stream")
			}
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"type\":\"response.output_item.added\"}\n\n")
			fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":150,\"output_tokens\":70,\"input_tokens_details\":{\"cached_tokens\":12}},\"output\":[{\"type\":\"code_interpreter_call\",\"container_id\":\"cntr-1\"}]}}\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
		case r.URL.Path == "/containers/cntr-1/files":
			fmt.Fprint(w, `{"data":[{"id":"cfile-1","path":"/mnt/data/pipkin-result.zip"}]}`)
		case r.URL.Path == "/containers/cntr-1/files/cfile-1/content":
			w.Write(resultBytes)
		case r.URL.Path == "/files/file-123":
			fmt.Fprint(w, `{"deleted":true}`)
		default:
			http.Error(w, "unknown", 404)
		}
	}))
	defer srv.Close()
	t.Setenv("PIPKIN_FAKE_KEY", "test")
	job := WorkflowJob{}
	e := openAIArchive(context.Background(), Config{APIKeyEnv: "PIPKIN_FAKE_KEY", Model: "test-model", BaseURL: srv.URL}, input, output, "fix it", &job)
	if e != nil {
		t.Fatal(e)
	}
	if uploads != 1 {
		t.Fatalf("uploads: %d", uploads)
	}
	if job.InputTokens != 150 || job.OutputTokens != 70 || job.CachedTokens != 12 {
		t.Fatalf("usage: %+v", job)
	}
	if e = validateZipStrict(output); e != nil {
		t.Fatal(e)
	}
}
func TestReviewApplyAndConflict(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".pipkin", "jobs", "job-1")
	if e := os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(root, "main.go"), []byte("old"), 0600); e != nil {
		t.Fatal(e)
	}
	input := filepath.Join(dir, "input.zip")
	result := filepath.Join(dir, "result.zip")
	zipFixture(t, input, map[string]string{"main.go": "old"})
	zipFixture(t, result, map[string]string{"main.go": "new", "extra.go": "fresh"})
	hashes, e := zipChecksums(input)
	if e != nil {
		t.Fatal(e)
	}
	j := WorkflowJob{Root: root, Status: "completed", Files: hashes}
	if e = saveJob(dir, j); e != nil {
		t.Fatal(e)
	}
	review, e := workflowReview(dir)
	if e != nil {
		t.Fatal(e)
	}
	if len(review.Files) != 2 {
		t.Fatalf("changes %v", review.Files)
	}
	if e = workflowApply(dir, "main.go"); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(filepath.Join(root, "main.go"))
	if string(b) != "new" {
		t.Fatalf("applied: %q", b)
	}
	if e = workflowApply(dir, "main.go"); e == nil || !strings.Contains(e.Error(), "changed since submission") {
		t.Fatalf("expected conflict, got %v", e)
	}
	if e = workflowApply(dir, "extra.go"); e != nil {
		t.Fatal(e)
	}
}
func TestStrictZIPRejectsSymlinkAndTraversal(t *testing.T) {
	tmp := t.TempDir()
	bad := filepath.Join(tmp, "bad.zip")
	zipFixture(t, bad, map[string]string{"../outside": "bad"})
	if e := validateZipStrict(bad); e == nil {
		t.Fatal("accepted traversal")
	}
}

func TestReviewCurrentWorkspaceDifferences(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(t.TempDir(), "job-1")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	zipFixture(t, filepath.Join(dir, "input.zip"), map[string]string{"same.go": "archive", "changed.go": "old"})
	zipFixture(t, filepath.Join(dir, "result.zip"), map[string]string{"same.go": "archive", "changed.go": "new"})
	os.WriteFile(filepath.Join(root, "same.go"), []byte("local edit"), 0600)
	os.WriteFile(filepath.Join(root, "changed.go"), []byte("old"), 0600)
	hashes, _ := zipChecksums(filepath.Join(dir, "input.zip"))
	if err := saveJob(dir, WorkflowJob{Root: root, Status: "completed", Files: hashes}); err != nil {
		t.Fatal(err)
	}
	review, err := workflowReview(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(review.Files) != 2 {
		t.Fatalf("expected 2 file differences got %+v", review.Files)
	}
	m := map[string]ReviewFile{}
	for _, f := range review.Files {
		m[f.Path] = f
	}
	if m["same.go"].Status != "workspace_only" || m["same.go"].WorkspaceStatus != "changed" {
		t.Fatalf("unexpected local-only diff: %+v", m["same.go"])
	}
	if m["changed.go"].WorkspaceStatus != "unchanged_since_submission" {
		t.Fatalf("unexpected current status: %+v", m["changed.go"])
	}
}
