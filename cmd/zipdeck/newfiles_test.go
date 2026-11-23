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

func TestLegacyLLMAddsFileToArchive(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "cmd", "zipdeck"), 0755)
	os.WriteFile(filepath.Join(root, "cmd", "zipdeck", "main.go"), []byte("package main\n"), 0644)
	os.WriteFile(filepath.Join(root, "cmd", "zipdeck", "private.go"), []byte("private"), 0644)
	files := []*Node{{rel: "cmd/zipdeck/main.go"}}
	input := filepath.Join(t.TempDir(), "input.zip")
	if err := writeZip(root, files, input); err != nil {
		t.Fatal(err)
	}
	makeServer := func(changes []Change) *httptest.Server {
		payload, _ := json.Marshal(Changes{Changes: changes})
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, `{"choices":[{"message":{"content":%q}}]}`, string(payload))
		}))
	}
	t.Setenv("ZIPDECK_TEST_KEY", "dummy")
	cases := []struct {
		name    string
		changes []Change
		wantErr bool
		wantNew bool
	}{
		{"add sibling", []Change{{Path: "cmd/zipdeck/api.go", Content: "package main\n"}}, false, true},
		{"add nested", []Change{{Path: "cmd/zipdeck/new/handler.go", Content: "package main\n"}}, false, true},
		{"modify unselected", []Change{{Path: "cmd/zipdeck/private.go", Content: "oops"}}, true, false},
		{"add unrelated", []Change{{Path: "another/file.go", Content: "oops"}}, true, false},
		{"add ignored", []Change{{Path: "cmd/zipdeck/.env", Content: "secret"}}, true, false},
		{"delete not supplied", []Change{{Path: "cmd/zipdeck/api.go", Delete: true}}, true, false},
		{"traversal", []Change{{Path: "../oops.go", Content: "no"}}, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := makeServer(tc.changes)
			defer srv.Close()
			output := filepath.Join(t.TempDir(), "out.zip")
			cfg := Config{BaseURL: srv.URL, Model: "test", APIKeyEnv: "ZIPDECK_TEST_KEY", MaxContextBytes: 100000}
			err := sendOpenAI(context.Background(), cfg, root, files, input, output, "test")
			if (err != nil) != tc.wantErr {
				t.Fatalf("got err %v wantErr %v", err, tc.wantErr)
			}
			if tc.wantNew {
				zr, e := zip.OpenReader(output)
				if e != nil {
					t.Fatal(e)
				}
				defer zr.Close()
				found := false
				for _, f := range zr.File {
					if f.Name == tc.changes[0].Path {
						found = true
					}
				}
				if !found {
					t.Fatalf("new file not in output ZIP: %s", tc.changes[0].Path)
				}
			}
			if tc.wantErr && err != nil && strings.TrimSpace(err.Error()) == "" {
				t.Fatal("empty error")
			}
		})
	}
}
