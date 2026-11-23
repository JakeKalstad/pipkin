package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOutputDirectory(t *testing.T) {
	t.Setenv("ZIPDECK_OUTPUT_DIR", t.TempDir())
	if outputDirectory() != os.Getenv("ZIPDECK_OUTPUT_DIR") {
		t.Fatal("output override ignored")
	}
}
func TestMCPHandshakeAndPack(t *testing.T) {
	root := t.TempDir()
	dest := t.TempDir()
	t.Setenv("ZIPDECK_OUTPUT_DIR", dest)
	if err := os.WriteFile(filepath.Join(root, "hello.go"), []byte("package hello"), 0600); err != nil {
		t.Fatal(err)
	}
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"zipdeck_pack","arguments":{"root":` + string(mustJSON(root)) + `,"paths":["hello.go"]}}}`,
	}, "\n") + "\n"
	var out bytes.Buffer
	if err := serveMCP(strings.NewReader(input), &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("unexpected MCP stdout lines: %s", out.String())
	}
	var response struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(lines[2]), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Result.Content) != 1 {
		t.Fatal("missing package result")
	}
	var packed struct {
		Zip string `json:"zip"`
	}
	if err := json.Unmarshal([]byte(response.Result.Content[0].Text), &packed); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(packed.Zip, dest) {
		t.Fatalf("ZIP outside output directory: %s", packed.Zip)
	}
	if err := validateZipStrict(packed.Zip); err != nil {
		t.Fatal(err)
	}
}
func TestMCPApplyIsOptIn(t *testing.T) {
	t.Setenv("ZIPDECK_MCP_ALLOW_APPLY", "")
	_, err := mcpCallTool(mcpCall{Name: "zipdeck_apply", Arguments: []byte(`{"job":"/tmp/example","file":"hello.go"}`)})
	if err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("expected disabled apply: %v", err)
	}
}
func mustJSON(s string) []byte { b, _ := json.Marshal(s); return b }
