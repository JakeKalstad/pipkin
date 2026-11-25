package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type hostedPromptTransport func(*http.Request) (*http.Response, error)

func (f hostedPromptTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestHostedFreePromptTools(t *testing.T) {
	t.Setenv("PIPKIN_TEST_HOSTED_KEY", "fixture")
	t.Setenv("PIPKIN_OUTPUT_DIR", t.TempDir())
	old := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = old })
	http.DefaultTransport = hostedPromptTransport(func(r *http.Request) (*http.Response, error) {
		var request struct {
			Tools        []struct{ Type string }
			Instructions string
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if r.URL.Path != "/v1/responses" || len(request.Tools) != 2 ||
			request.Tools[0].Type != "web_search" || request.Tools[1].Type != "code_interpreter" ||
			!strings.Contains(request.Instructions, "Bundled skill: python-execution") {
			t.Errorf("unexpected hosted request: %+v", request)
		}
		body := `{"status":"completed","output":[{"type":"web_search_call"},{"type":"message","content":[{"type":"output_text","text":"Verified.","annotations":[{"type":"url_citation","url":"https://example.org/source"}]}]}]}`
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})
	cfg := Config{Provider: "openai", Model: "fixture", BaseURL: "https://api.openai.com/v1",
		APIKeyEnv: "PIPKIN_TEST_HOSTED_KEY", FreePromptTools: []string{"web_search", "code_interpreter"}}
	reply, err := freePrompt(context.Background(), t.TempDir(), "Check this.", cfg, nil)
	if err != nil || reply != "Verified.\n\nSources\nhttps://example.org/source" || len(freePromptSources(reply)) != 1 {
		t.Fatalf("response: %q, %v", reply, err)
	}
	cfg.BaseURL = "https://example.org/v1"
	if _, err := freePrompt(context.Background(), t.TempDir(), "Check this.", cfg, nil); err == nil {
		t.Fatal("tool request accepted a non-OpenAI endpoint")
	}
}
