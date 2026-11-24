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

type llmTraceKey struct{}
type llmStageKey struct{}
type llmCall struct {
	name                  string
	start, end            time.Time
	input, output, cached int
	failed                bool
	request, reply        string
}
type llmTrace struct {
	calls []llmCall
}

const traceTextLimit = 64 << 10

func captureTraceIO(cfg Config) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("PIPKIN_LANGFUSE_CAPTURE_IO"))) {
	case "1", "true":
		return langfuseURL(cfg) != ""
	default:
		return false
	}
}

func boundedTraceText(text string) string {
	if len(text) <= traceTextLimit {
		return text
	}
	return strings.ToValidUTF8(text[:traceTextLimit], "") + "\n[truncated]"
}

func recordLLMCall(ctx context.Context, cfg Config, operation string, job *WorkflowJob, err *error, input string, output *string) func() {
	call := llmCall{name: operation, start: time.Now()}
	capture := captureTraceIO(cfg)
	if capture {
		call.request = boundedTraceText(input)
	}
	if stage, ok := ctx.Value(llmStageKey{}).(string); ok {
		call.name = stage + "/" + operation
	}
	if job != nil {
		call.input, call.output, call.cached = -job.InputTokens, -job.OutputTokens, -job.CachedTokens
	}
	return func() {
		call.end, call.failed = time.Now(), *err != nil
		if capture && output != nil {
			call.reply = boundedTraceText(*output)
		}
		if job != nil {
			call.input += job.InputTokens
			call.output += job.OutputTokens
			call.cached += job.CachedTokens
		}
		if trace, ok := ctx.Value(llmTraceKey{}).(*llmTrace); ok {
			trace.calls = append(trace.calls, call)
			return
		}
		if exportErr := reportLangfuse(cfg, WorkflowJob{Provider: cfg.Provider, Model: cfg.Model}, call); exportErr != nil {
			fmt.Fprintln(os.Stderr, "Langfuse export failed:", exportErr)
		}
	}
}

// reportLangfuse sends OTLP/HTTP spans to Langfuse v4. Text capture is opt-in.
// Tracing is optional, and a failed export must not fail a project run.
func reportLangfuse(cfg Config, job WorkflowJob, calls ...llmCall) error {
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
	container := os.Getenv("PIPKIN_WEB_CONTAINER") == "1" &&
		os.Getenv("PIPKIN_LANGFUSE_CONTAINER_HTTP") == "1" && u.Host == "langfuse-web:3000"
	if u.Scheme != "https" && !(u.Scheme == "http" && (loopback || container)) {
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
	name := "pipkin-workflow"
	if job.Operation == "snippet" {
		name = "pipkin-snippet"
	}
	makeSpan := func(spanName, kind string, start, end time.Time, failed bool, input, output, cached int) map[string]any {
		attrs := []any{attr("langfuse.observation.type", kind), attr("langfuse.trace.name", name), attr("pipkin.job_id", job.ID), attr("pipkin.provider", job.Provider)}
		if kind == "generation" {
			attrs = append(attrs, attr("gen_ai.operation.name", "chat"), attr("gen_ai.request.model", job.Model),
				usageAttr("gen_ai.usage.input_tokens", input), usageAttr("gen_ai.usage.output_tokens", output), usageAttr("pipkin.cached_tokens", cached))
		}
		code, status := 1, "completed"
		if failed {
			code, status = 2, "failed"
		}
		attrs = append(attrs, attr("pipkin.status", status))
		return map[string]any{"traceId": hex.EncodeToString(traceIDBytes), "spanId": hex.EncodeToString(spanIDBytes), "name": spanName, "startTimeUnixNano": fmt.Sprint(start.UnixNano()), "endTimeUnixNano": fmt.Sprint(end.UnixNano()), "attributes": attrs, "status": map[string]any{"code": code}}
	}
	generation := func(spanName string, call llmCall) map[string]any {
		span := makeSpan(spanName, "generation", call.start, call.end, call.failed, call.input, call.output, call.cached)
		attrs := span["attributes"].([]any)
		if call.request != "" {
			attrs = append(attrs, attr("langfuse.observation.input", call.request))
		}
		if call.reply != "" {
			attrs = append(attrs, attr("langfuse.observation.output", call.reply))
		}
		span["attributes"] = attrs
		return span
	}
	var spans []any
	switch {
	case len(calls) == 0:
		spans = append(spans, makeSpan(name, "span", start, end, job.Error != "", 0, 0, 0))
	case len(calls) == 1 && (job.Operation == "snippet" || job.ID == ""):
		call := calls[0]
		spans = append(spans, generation(name, call))
	default:
		root := makeSpan(name, "span", start, end, job.Error != "", 0, 0, 0)
		spans = append(spans, root)
		for _, call := range calls {
			if _, err := rand.Read(spanIDBytes); err != nil {
				return err
			}
			span := generation(call.name, call)
			span["parentSpanId"] = root["spanId"]
			spans = append(spans, span)
		}
	}
	payload := map[string]any{"resourceSpans": []any{map[string]any{"resource": map[string]any{"attributes": []any{attr("service.name", "pipkin")}}, "scopeSpans": []any{map[string]any{"scope": map[string]any{"name": "pipkin"}, "spans": spans}}}}}
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
