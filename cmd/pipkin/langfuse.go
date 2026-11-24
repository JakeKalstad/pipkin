package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

func langfuseURL(cfg Config) string {
	if configured := strings.TrimSpace(cfg.LangfuseURL); configured != "" {
		return configured
	}
	return strings.TrimSpace(os.Getenv("LANGFUSE_BASE_URL"))
}

// reportLangfuse sends a metadata-only OTLP/HTTP trace to Langfuse v4.
// No prompts, project contents, archive bytes, or API secrets are exported.
// Tracing is optional, and a failed export must not fail a project run.
func reportLangfuse(cfg Config, job WorkflowJob) error {
	base := langfuseURL(cfg)
	publicEnv := cfg.LangfusePublicKeyEnv
	if publicEnv == "" {
		publicEnv = "LANGFUSE_PUBLIC_KEY"
	}
	secretEnv := cfg.LangfuseSecretKeyEnv
	if secretEnv == "" {
		secretEnv = "LANGFUSE_SECRET_KEY"
	}
	public, secret := os.Getenv(publicEnv), os.Getenv(secretEnv)
	if base == "" {
		if public != "" || secret != "" {
			return fmt.Errorf("set LANGFUSE_BASE_URL or langfuse_url")
		}
		return nil
	}
	if public == "" || secret == "" {
		return fmt.Errorf("set %s and %s", publicEnv, secretEnv)
	}
	u, err := url.Parse(base)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("invalid Langfuse base URL")
	}
	ip := net.ParseIP(u.Hostname())
	loopback := strings.EqualFold(u.Hostname(), "localhost") || ip != nil && ip.IsLoopback()
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
		return fmt.Errorf("Langfuse URL must use HTTPS (except loopback)")
	}
	traceIDBytes := make([]byte, 16)
	spanIDBytes := make([]byte, 8)
	if _, err := rand.Read(traceIDBytes); err != nil {
		return err
	}
	if _, err := rand.Read(spanIDBytes); err != nil {
		return err
	}
	end := time.Now().UTC()
	start := end.Add(-time.Millisecond)
	if t, err := time.Parse(time.RFC3339, job.Created); err == nil && t.Before(end) {
		start = t
	}
	attr := func(key, value string) map[string]any {
		return map[string]any{"key": key, "value": map[string]any{"stringValue": value}}
	}
	usageAttr := func(key string, value int) map[string]any {
		return map[string]any{"key": key, "value": map[string]any{"intValue": fmt.Sprint(value)}}
	}
	attrs := []any{attr("langfuse.observation.type", "generation"), attr("langfuse.trace.name", "pipkin-workflow"), attr("gen_ai.operation.name", "chat"), attr("gen_ai.request.model", job.Model), attr("pipkin.job_id", job.ID), attr("pipkin.provider", job.Provider), attr("pipkin.status", job.Status), usageAttr("gen_ai.usage.input_tokens", job.InputTokens), usageAttr("gen_ai.usage.output_tokens", job.OutputTokens), usageAttr("pipkin.cached_tokens", job.CachedTokens)}
	span := map[string]any{"traceId": hex.EncodeToString(traceIDBytes), "spanId": hex.EncodeToString(spanIDBytes), "name": "pipkin-workflow", "startTimeUnixNano": fmt.Sprint(start.UnixNano()), "endTimeUnixNano": fmt.Sprint(end.UnixNano()), "attributes": attrs}
	if job.Error != "" {
		span["status"] = map[string]any{"code": 2}
	} else {
		span["status"] = map[string]any{"code": 1}
	}
	payload := map[string]any{"resourceSpans": []any{map[string]any{"resource": map[string]any{"attributes": []any{attr("service.name", "pipkin")}}, "scopeSpans": []any{map[string]any{"scope": map[string]any{"name": "pipkin"}, "spans": []any{span}}}}}}
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	endpoint := strings.TrimRight(base, "/") + "/api/public/otel/v1/traces"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.SetBasicAuth(public, secret)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-langfuse-ingestion-version", "4")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return fmt.Errorf("Langfuse HTTP %d: %s", resp.StatusCode, body)
	}
	return nil
}
