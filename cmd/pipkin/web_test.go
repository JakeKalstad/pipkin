package main

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestListenLocalOnly(t *testing.T) {
	for _, tc := range []struct {
		url string
		ok  bool
	}{{"http://127.0.0.1:8787", true}, {"localhost:8787", true}, {"http://0.0.0.0:8787", false}, {"http://example.com:8080", false}, {"https://localhost:8787", false}} {
		_, e := parseListen(tc.url)
		if (e == nil) != tc.ok {
			t.Errorf("%q: %v", tc.url, e)
		}
	}
}
func TestEnvPrecedence(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(p, []byte("PIPKIN_TEST=from_file\n#comment\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PIPKIN_TEST", "from_process")
	if err := loadDotEnv(p); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("PIPKIN_TEST") != "from_process" {
		t.Fatal("overrode existing environment")
	}
}
func TestWebHomepage(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	webHandler().ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Pipkin") {
		t.Fatalf("status=%d", w.Code)
	}
}
func TestWebInvalidJob(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/review?job=../../etc", nil)
	webHandler().ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatalf("status=%d", w.Code)
	}
}
