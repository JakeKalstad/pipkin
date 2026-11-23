package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testReviewJob(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("old\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "keep.go"), []byte("unchanged\n"), 0644); err != nil {
		t.Fatal(err)
	}
	mkzip := func(p string, entries map[string]string) {
		f, e := os.Create(p)
		if e != nil {
			t.Fatal(e)
		}
		w := zip.NewWriter(f)
		for name, body := range entries {
			out, e := w.Create(name)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = out.Write([]byte(body)); e != nil {
				t.Fatal(e)
			}
		}
		if e := w.Close(); e != nil {
			t.Fatal(e)
		}
		if e := f.Close(); e != nil {
			t.Fatal(e)
		}
	}
	mkzip(filepath.Join(dir, "input.zip"), map[string]string{"main.go": "old\n", "keep.go": "unchanged\n"})
	mkzip(filepath.Join(dir, "result.zip"), map[string]string{"main.go": "new\n", "keep.go": "unchanged\n", "added.go": "package main\n"})
	sum := sha256.Sum256([]byte("old\n"))
	job := WorkflowJob{ID: "job-test", Root: root, Status: "completed", Files: map[string]string{"main.go": hex.EncodeToString(sum[:])}}
	if err := saveJob(dir, job); err != nil {
		t.Fatal(err)
	}
	return root, dir
}
func TestReviewCopyApprovedAndReject(t *testing.T) {
	root, dir := testReviewJob(t)
	review, err := workflowReview(dir)
	if err != nil {
		t.Fatal(err)
	}
	files := reviewableFiles(review)
	if len(files) != 2 {
		t.Fatalf("expected 2 changes; got %d: %#v", len(files), files)
	}
	decisions := reviewDecisions{Decisions: map[string]string{"main.go": decisionApproved, "added.go": decisionRejected}, Applied: map[string]bool{}}
	if err = saveDecisions(dir, decisions); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadDecisions(dir)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Decisions["added.go"] != decisionRejected {
		t.Fatal("rejection not persisted")
	}
	n, err := copyApprovedChanges(dir, files, &loaded)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("copied %d", n)
	}
	contents, _ := os.ReadFile(filepath.Join(root, "main.go"))
	if string(contents) != "new\n" {
		t.Fatalf("incorrect modification %q", contents)
	}
	if _, err = os.Stat(filepath.Join(root, "added.go")); !os.IsNotExist(err) {
		t.Fatal("rejected file was applied")
	}
	if !loaded.Applied["main.go"] {
		t.Fatal("applied state missing")
	}
	n, err = copyApprovedChanges(dir, files, &loaded)
	if err != nil || n != 0 {
		t.Fatalf("copy retry: %d %v", n, err)
	}
}
func TestReviewCopyPreflightConflictPreventsPartialApply(t *testing.T) {
	root, dir := testReviewJob(t)
	review, err := workflowReview(dir)
	if err != nil {
		t.Fatal(err)
	}
	files := reviewableFiles(review)
	decisions := reviewDecisions{Decisions: map[string]string{"main.go": decisionApproved, "added.go": decisionApproved}, Applied: map[string]bool{}}
	if err = os.WriteFile(filepath.Join(root, "main.go"), []byte("local edit"), 0644); err != nil {
		t.Fatal(err)
	}
	n, err := copyApprovedChanges(dir, files, &decisions)
	if n != 0 || err == nil || !strings.Contains(err.Error(), "local changes") {
		t.Fatalf("expected conflict got n=%d err=%v", n, err)
	}
	if _, err = os.Stat(filepath.Join(root, "added.go")); !os.IsNotExist(err) {
		t.Fatal("partial apply occurred")
	}
}
func TestReviewNewFileAndDiff(t *testing.T) {
	root, dir := testReviewJob(t)
	review, err := workflowReview(dir)
	if err != nil {
		t.Fatal(err)
	}
	files := reviewableFiles(review)
	decisions := reviewDecisions{Decisions: map[string]string{"main.go": decisionRejected, "added.go": decisionApproved}, Applied: map[string]bool{}}
	n, err := copyApprovedChanges(dir, files, &decisions)
	if err != nil || n != 1 {
		t.Fatalf("copy: %d %v", n, err)
	}
	b, err := os.ReadFile(filepath.Join(root, "added.go"))
	if err != nil || string(b) != "package main\n" {
		t.Fatal("new file missing", err)
	}
	for _, f := range files {
		if f.Path == "main.go" && !strings.Contains(strings.Join(diffLines(f, "submitted"), "\n"), "+new") {
			t.Fatal("diff does not show replacement")
		}
	}
}

func TestImportExistingZipPair(t *testing.T) {
	root, dir := testReviewJob(t)
	output := t.TempDir()
	t.Setenv("PIPKIN_OUTPUT_DIR", output)
	imported, err := importOutputPair(root, filepath.Join(dir, "input.zip"), filepath.Join(dir, "result.zip"))
	if err != nil {
		t.Fatal(err)
	}
	review, err := workflowReview(imported)
	if err != nil {
		t.Fatal(err)
	}
	if len(reviewableFiles(review)) != 2 {
		t.Fatalf("import review: %#v", review.Files)
	}
	sessions, err := reviewSessionPaths(root)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("import session discovery: %v %v", sessions, err)
	}
}
