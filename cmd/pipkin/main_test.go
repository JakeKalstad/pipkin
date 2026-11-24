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
	if len(files) != 2 || files[0].rel != filepath.Join("node_modules", "lib.js") || files[1].rel != filepath.Join("src", "main.go") {
		t.Fatalf("files: %+v", files)
	}
	if err := os.WriteFile(manifest, []byte(`["../bad"]`), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := filesFromJSON(root, manifest); err == nil {
		t.Fatal("expected traversal rejection")
	}
}

func TestHiddenPathsAndExplicitOptIn(t *testing.T) {
	dir := t.TempDir()
	paths := []string{
		"src/main.go", "src/.configure/settings", "src/.github/workflows/ci.yml",
		"src/.github/.env.local", "src/.git/config", "src/.env", "src/.env.example",
	}
	for _, path := range paths {
		full := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, show := range []bool{false, true} {
		files, err := selectPaths(dir, []string{"."}, show)
		if err != nil || len(files) != 1 || files[0].rel != "src/main.go" {
			t.Fatalf("bulk selection with hidden=%t: %v (%v)", show, files, err)
		}
		dirs, err := buildWebDirTree("", dir, show)
		if err != nil {
			t.Fatal(err)
		}
		for _, child := range dirs[0].Children {
			if child.Name == ".git" || child.Name == ".env" ||
				(hiddenName(child.Name) && (!show || child.Selected)) {
				t.Fatalf("unexpected picker item: %+v", child)
			}
		}
	}
	files, err := selectPaths(dir, []string{"src/.github", "src/.git/config", "src/.env", "src/.env.example"}, true)
	if err != nil || len(files) != 2 || files[0].rel != paths[6] || files[1].rel != paths[2] {
		t.Fatalf("explicit opt-in: %v (%v)", files, err)
	}
	files = append(files, &Node{rel: paths[1]}, &Node{rel: paths[3], includeHidden: true})
	dest := filepath.Join(t.TempDir(), "context.zip")
	if err := writeZip(dir, files, dest); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.OpenReader(dest)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	if len(zr.File) != 2 || zr.File[0].Name != paths[6] || zr.File[1].Name != paths[2] {
		t.Fatalf("ZIP contains unexpected paths: %v", zr.File)
	}
}
