package main

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

func TestIgnoresAndZip(t *testing.T) {
	dir := t.TempDir()
	for _, p := range []string{"main.go", "sub/ok.py", "sub/.venv/secret.py", "target/cache.txt", ".env", "README.md"} {
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("hello"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	root := &Node{dir: true, selected: true, expanded: true}
	if err := scan(root, dir); err != nil {
		t.Fatal(err)
	}
	app := App{root: root, cwd: dir}
	selected, _ := app.selectedFiles()
	dest := filepath.Join(t.TempDir(), "out.zip")
	if err := writeZip(dir, selected, dest); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.OpenReader(dest)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	actual := map[string]bool{}
	for _, f := range zr.File {
		actual[f.Name] = true
	}
	for _, p := range []string{"main.go", "sub/ok.py", "README.md"} {
		if !actual[p] {
			t.Errorf("missing %s", p)
		}
	}
	for _, p := range []string{"sub/.venv/secret.py", "target/cache.txt", ".env"} {
		if actual[p] {
			t.Errorf("incorrectly included %s", p)
		}
	}
}
func TestSafeRel(t *testing.T) {
	for _, p := range []string{"../foo", "/tmp/x", "a/../../b", "foo\\bar"} {
		if safeRel(p) {
			t.Errorf("accepted unsafe path %q", p)
		}
	}
	for _, p := range []string{"src/main.go", "README.md"} {
		if !safeRel(p) {
			t.Errorf("rejected %q", p)
		}
	}
}

func TestFilesFromJSON(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "node_modules"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"src/main.go", "node_modules/lib.js", ".env"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("test"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	manifest := filepath.Join(t.TempDir(), "files.json")
	if err := os.WriteFile(manifest, []byte(`["src","node_modules",".env"]`), 0644); err != nil {
		t.Fatal(err)
	}
	files, err := filesFromJSON(root, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 || files[0].rel != ".env" || files[1].rel != filepath.Join("node_modules", "lib.js") || files[2].rel != filepath.Join("src", "main.go") {
		t.Fatalf("files: %+v", files)
	}
	if err := os.WriteFile(manifest, []byte(`["../bad"]`), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := filesFromJSON(root, manifest); err == nil {
		t.Fatal("expected traversal rejection")
	}
}
