package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestFreePromptHTTPAndMCP(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PIPKIN_OUTPUT_DIR", t.TempDir())
	t.Setenv("PIPKIN_MODEL", "test-model")
	t.Setenv("PIPKIN_PROVIDER_PROFILE", "")
	t.Setenv("PIPKIN_PROVIDER", "vllm")
	t.Setenv("LANGFUSE_BASE_URL", "")
	if err := saveStandardInstruction(root, "AGENTS.md", "Use targeted checks."); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "context.txt"), []byte("Reference content"), 0600); err != nil {
		t.Fatal(err)
	}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []struct{ Role, Content string }
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || len(request.Messages) != 2 && len(request.Messages) != 4 {
			t.Error("invalid model request")
			http.Error(w, "invalid model request", 400)
			return
		}
		system, user := request.Messages[0], request.Messages[len(request.Messages)-1]
		if len(request.Messages) == 4 && (request.Messages[1].Content != "Explain this." ||
			request.Messages[2].Role != "assistant" || request.Messages[2].Content != "<answer>") {
			t.Errorf("follow-up missing history: %+v", request.Messages)
		}
		attached := strings.Contains(user.Content, "\n\nWorkspace file context") &&
			strings.Contains(user.Content, `{"path":"context.txt","text":"Reference content"}`) &&
			strings.Contains(system.Content, "as reference data")
		wantAttachment := len(request.Messages) == 4 || strings.HasPrefix(user.Content, "Explain this file.")
		if system.Role != "system" || !strings.Contains(system.Content, "Use targeted checks.") ||
			user.Role != "user" || attached != wantAttachment || (!wantAttachment && user.Content != "Explain this.") {
			t.Errorf("unexpected messages: %+v", request.Messages)
		}
		fmt.Fprint(w, `{"choices":[{"message":{"content":"<answer>"}}]}`)
	}))
	defer provider.Close()
	t.Setenv("PIPKIN_BASE_URL", provider.URL)
	t.Setenv("PIPKIN_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	t.Setenv("OPENAI_API_KEY", "")
	h := webHandlerForRoot(root)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/prompt", nil))
	token := regexp.MustCompile(`name="csrf" value="([^"]+)"`).FindStringSubmatch(w.Body.String())
	if len(token) != 2 || !strings.Contains(w.Body.String(), `name="files" value="context.txt"`) ||
		strings.Contains(w.Body.String(), `value="context.txt" checked`) {
		t.Fatal("missing prompt form or unchecked context picker")
	}
	post := func(accept string, values url.Values) *httptest.ResponseRecorder {
		values.Set("csrf", token[1])
		req := httptest.NewRequest("POST", "/prompt", strings.NewReader(values.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Accept", accept)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	if blocked := post("application/json", url.Values{"prompt": {"Explain this."}, "files": {"../outside.txt"}}); blocked.Code != 422 {
		t.Fatalf("outside path returned %d", blocked.Code)
	}
	redirect := post("text/html", url.Values{"prompt": {"Explain this file."}, "files": {"context.txt"}})
	if redirect.Code != 303 {
		t.Fatalf("prompt returned %d", redirect.Code)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", redirect.Header().Get("Location"), nil))
	if w.Code != 200 || (!strings.Contains(w.Body.String(), "&lt;answer&gt;") || strings.Contains(w.Body.String(), "Reference content")) {
		t.Fatal("saved HTML response was not escaped")
	}
	created := post("application/json", url.Values{"prompt": {"Explain this."}})
	var payload map[string]string
	if err := json.Unmarshal(created.Body.Bytes(), &payload); err != nil || created.Code != 200 ||
		payload["reply"] != "<answer>" || payload["conversation"] == "" {
		t.Fatalf("JSON response: %v %v", payload, err)
	}
	id := payload["conversation"]
	args, _ := json.Marshal(mcpArgs{Root: root, Conversation: id, Prompt: "Explain this.", Paths: []string{"context.txt"}})
	result, err := mcpCallTool(mcpCall{Name: "pipkin_prompt", Arguments: args})
	if err != nil || result.(map[string]string)["reply"] != "<answer>" || result.(map[string]string)["conversation"] != id {
		t.Fatalf("MCP response: %v %v", result, err)
	}
	args, _ = json.Marshal(mcpArgs{Root: root, Action: "read", Conversation: id})
	result, err = mcpCallTool(mcpCall{Name: "pipkin_conversations", Arguments: args})
	if err != nil || len(result.(conversation).Messages) != 4 || result.(conversation).Messages[2].Content != "Explain this." {
		t.Fatalf("MCP transcript: %v %v", result, err)
	}
	deleted := post("application/json", url.Values{"action": {"delete"}, "conversation": {id}})
	if deleted.Code != 200 {
		t.Fatalf("delete: %s", deleted.Body.String())
	}
	if _, err := mcpCallTool(mcpCall{Name: "pipkin_conversations", Arguments: args}); !os.IsNotExist(err) {
		t.Fatalf("deleted transcript read: %v", err)
	}

	if _, err := os.Stat(filepath.Join(outputDirectory(), "jobs")); !os.IsNotExist(err) {
		t.Fatalf("free prompt created a delivery: %v", err)
	}
}

func TestMCPInstructionManagement(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PIPKIN_OUTPUT_DIR", t.TempDir())
	file := ".agents/skills/review/SKILL.md"
	markdown := "---\nname: review\ndescription: Review changes.\n---\nCheck callers."
	for _, action := range []string{"save", "toggle", "list", "read"} {
		args, _ := json.Marshal(mcpArgs{Root: root, Action: action, File: file, Markdown: &markdown})
		result, err := mcpCallTool(mcpCall{Name: "pipkin_instructions", Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", action, err)
		}
		if action == "read" && result.(map[string]string)["markdown"] != markdown {
			t.Fatalf("read: %v", result)
		}
	}
	settings, err := loadInstructionSettings(root)
	if err != nil || len(settings.Files) != 1 || settings.Files[0] != file {
		t.Fatalf("enabled skill: %+v %v", settings, err)
	}
}

func TestConversationRejectsStaleWrites(t *testing.T) {
	dir := t.TempDir()
	thread := conversation{
		conversationSummary: conversationSummary{ID: "0123456789abcdef0123456789abcdef", Title: "Test"},
		Messages:            []chatMessage{{"user", "one"}, {"assistant", "two"}},
	}
	if err := saveConversation(dir, thread, 0); err != nil {
		t.Fatal(err)
	}
	thread.Messages = append(thread.Messages, chatMessage{"user", "three"}, chatMessage{"assistant", "four"})
	if err := saveConversation(dir, thread, 2); err != nil {
		t.Fatal(err)
	}
	if err := saveConversation(dir, thread, 2); err == nil {
		t.Fatal("stale conversation update was accepted")
	}
	if err := deleteConversation(dir, thread.ID); err != nil {
		t.Fatal(err)
	}
	if err := saveConversation(dir, thread, 4); !os.IsNotExist(err) {
		t.Fatalf("deleted conversation was recreated: %v", err)
	}
}
