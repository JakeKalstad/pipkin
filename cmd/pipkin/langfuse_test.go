package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLangfuseOTLPMetadataOnly(t *testing.T) {
	var received map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/public/otel/v1/traces" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		user, pass, ok := r.BasicAuth()
		if !ok || user != "pk-test" || pass != "sk-test" {
			t.Error("bad auth")
		}
		if r.Header.Get("x-langfuse-ingestion-version") != "4" {
			t.Error("missing ingestion header")
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Error(err)
		}
		w.WriteHeader(200)
	}))
	defer server.Close()
	t.Setenv("LANGFUSE_PUBLIC_KEY", "pk-test")
	t.Setenv("LANGFUSE_SECRET_KEY", "sk-test")
	job := WorkflowJob{ID: "job-1", Created: "2026-11-23T00:00:00Z", Provider: "openai-archive", Model: "test", Status: "completed", InputTokens: 12, OutputTokens: 5}
	now := time.Now()
	calls := []llmCall{
		{name: "draft/responses", start: now.Add(-time.Second), end: now, failed: true},
		{name: "draft/chat-completions", start: now, end: now, input: 12, output: 5, cached: 8},
	}
	if err := reportLangfuse(Config{LangfuseURL: server.URL}, job, calls...); err != nil {
		t.Fatal(err)
	}
	if len(received) == 0 {
		t.Fatal("no trace received")
	}
	resource := received["resourceSpans"].([]any)[0].(map[string]any)
	scope := resource["scopeSpans"].([]any)[0].(map[string]any)
	spans := scope["spans"].([]any)
	if len(spans) != 3 {
		t.Fatalf("expected workflow root and two model calls, got %d spans", len(spans))
	}
	root := spans[0].(map[string]any)
	for index, call := range calls {
		child := spans[index+1].(map[string]any)
		if child["name"] != call.name || child["traceId"] != root["traceId"] ||
			child["parentSpanId"] != root["spanId"] || child["spanId"] == root["spanId"] {
			t.Fatalf("unexpected child span: %v", child)
		}
	}

}
func TestLangfuseDisabled(t *testing.T) {
	t.Setenv("LANGFUSE_BASE_URL", "")
	if err := reportLangfuse(Config{}, WorkflowJob{}); err != nil {
		t.Fatal(err)
	}
}
