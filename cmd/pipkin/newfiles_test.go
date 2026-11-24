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
	os.MkdirAll(filepath.Join(root, "cmd", "pipkin"), 0755)
	os.WriteFile(filepath.Join(root, "cmd", "pipkin", "main.go"), []byte("package main\n"), 0644)
	os.WriteFile(filepath.Join(root, "cmd", "pipkin", "private.go"), []byte("private"), 0644)
	files := []*Node{{rel: "cmd/pipkin/main.go"}}
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
	t.Setenv("PIPKIN_TEST_KEY", "dummy")
	cases := []struct {
		name    string
		changes []Change
		wantErr bool
		wantNew bool
	}{
		{"add sibling", []Change{{Path: "cmd/pipkin/api.go", Content: "package main\n"}}, false, true},
		{"add nested", []Change{{Path: "cmd/pipkin/new/handler.go", Content: "package main\n"}}, false, true},
		{"modify unselected", []Change{{Path: "cmd/pipkin/private.go", Content: "oops"}}, true, false},
		{"add unrelated", []Change{{Path: "another/file.go", Content: "oops"}}, true, false},
		{"add ignored", []Change{{Path: "cmd/pipkin/.env", Content: "secret"}}, true, false},
		{"delete not supplied", []Change{{Path: "cmd/pipkin/api.go", Delete: true}}, true, false},
		{"traversal", []Change{{Path: "../oops.go", Content: "no"}}, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := makeServer(tc.changes)
			defer srv.Close()
			output := filepath.Join(t.TempDir(), "out.zip")
			cfg := Config{BaseURL: srv.URL, Model: "test", APIKeyEnv: "PIPKIN_TEST_KEY", MaxContextBytes: 100000}
			err := sendOpenAI(context.Background(), cfg, root, input, output, "test", nil, nil)
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

func TestLegacyLLMReportsOmittedContext(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "input.zip")
	zipFixture(t, input, map[string]string{"main.go": "package main", "README.md": strings.Repeat("x", 50001)})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []struct{ Content string }
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if len(request.Messages) != 2 || !strings.Contains(request.Messages[1].Content, "package main") || strings.Contains(request.Messages[1].Content, "README.md") {
			t.Error("unexpected model context")
		}
		fmt.Fprint(w, `{"choices":[{"message":{"content":"{\"changes\":[]}"}}]}`)
	}))
	defer server.Close()
	t.Setenv("PIPKIN_TEST_KEY", "dummy")
	cfg := Config{BaseURL: server.URL, Model: "test", APIKeyEnv: "PIPKIN_TEST_KEY", MaxContextBytes: 160000}
	var omitted []string
	if err := sendOpenAI(context.Background(), cfg, root, input, filepath.Join(root, "result.zip"), "Edit docs", &omitted, nil); err != nil {
		t.Fatal(err)
	}
	if len(omitted) != 1 || omitted[0] != "README.md" {
		t.Fatalf("omitted paths: %v", omitted)
	}
}

func TestConfigModelEnvironmentOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"provider":"openai-archive","model":"configured-model"}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PIPKIN_CONFIG", path)
	t.Setenv("PIPKIN_MODEL", "")
	t.Setenv("OPENAI_MODEL", "openai-env-model")
	if cfg := loadConfig(); cfg.Model != "openai-env-model" || cfg.Provider != "openai-archive" {
		t.Fatalf("model and provider: %q, %q", cfg.Model, cfg.Provider)
	}
	t.Setenv("PIPKIN_MODEL", "pipkin-env-model")
	if got := loadConfig().Model; got != "pipkin-env-model" {
		t.Fatalf("model: %q", got)
	}
}
