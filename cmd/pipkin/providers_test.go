package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestNamedProviderProfiles(t *testing.T) {
	config := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("PIPKIN_CONFIG", config)
	t.Setenv("PIPKIN_PROVIDER", "openai")
	t.Setenv("OPENAI_MODEL", "old-model")
	t.Setenv("PIPKIN_BASE_URL", "https://example.invalid")
	t.Setenv("PIPKIN_API_KEY_ENV", "OLD_KEY")
	if err := os.WriteFile(config, []byte(`{
		"provider":"openai", "provider_profile":"claude",
		"provider_profiles":{
			"claude":{"provider":"anthropic","model":"claude-account-model"},
			"grok":{"provider":"xai","model":"xai-account-model"},
			"do":{"provider":"digitalocean","model":"do-account-model"}
		}
	}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, base, key, model string }{
		{"claude", "https://api.anthropic.com/v1", "ANTHROPIC_API_KEY", "claude-account-model"},
		{"grok", "https://api.x.ai/v1", "XAI_API_KEY", "xai-account-model"},
		{"do", "https://inference.do-ai.run/v1", "DIGITALOCEAN_INFERENCE_KEY", "do-account-model"},
	} {
		t.Setenv("PIPKIN_PROVIDER_PROFILE", tc.name)
		cfg := loadConfig()
		if cfg.configError != nil || cfg.BaseURL != tc.base || cfg.APIKeyEnv != tc.key || cfg.Model != tc.model ||
			!cfg.supportsAgentic() || cfg.hostedArchive(true) {
			t.Fatalf("%s: wrong routing: %+v", tc.name, cfg)
		}
	}
	t.Setenv("PIPKIN_PROVIDER_PROFILE", "missing")
	if cfg := loadConfig(); cfg.configError == nil {
		t.Fatal("missing profile silently accepted")
	}
}

func TestProviderTextRequests(t *testing.T) {
	t.Setenv("PIPKIN_TEST_PROVIDER_KEY", "test-key")
	for _, provider := range []string{"xai", "digitalocean", "anthropic"} {
		t.Run(provider, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var payload map[string]any
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				if payload["model"] != "test-model" {
					t.Error("wrong model")
				}
				if provider == "anthropic" {
					if r.URL.Path != "/v1/messages" || r.Header.Get("x-api-key") != "test-key" ||
						r.Header.Get("anthropic-version") != "2023-06-01" || r.Header.Get("Authorization") != "" ||
						payload["system"] != "system rules" || payload["max_tokens"] != float64(200) {
						t.Error("wrong Anthropic request")
					}
					fmt.Fprint(w, `{"content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn","usage":{"input_tokens":3,"output_tokens":2,"cache_read_input_tokens":4,"cache_creation_input_tokens":1}}`)
					return
				}
				if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer test-key" {
					t.Error("wrong chat request")
				}
				fmt.Fprint(w, `{"choices":[{"message":{"content":"hello"}}],"usage":{"prompt_tokens":8,"completion_tokens":2,"prompt_tokens_details":{"cached_tokens":4}}}`)
			}))
			defer server.Close()
			trace := &llmTrace{}
			ctx := context.WithValue(context.Background(), llmTraceKey{}, trace)
			job := WorkflowJob{}
			cfg := Config{Provider: provider, BaseURL: server.URL + "/v1", Model: "test-model", APIKeyEnv: "PIPKIN_TEST_PROVIDER_KEY", MaxOutputTokens: 200}
			answer, err := chatCompletion(ctx, cfg, "system rules", "question", &job)
			if err != nil || answer != "hello" || len(trace.calls) != 1 || job.InputTokens != 8 || job.OutputTokens != 2 || job.CachedTokens != 4 {
				t.Fatalf("unexpected answer or usage: %q, %v, %+v, %+v", answer, err, job, trace.calls)
			}
		})
	}
}
