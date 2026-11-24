package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func TestAgenticWorkflowReviewsDraft(t *testing.T) {
	root := t.TempDir()
	output := t.TempDir()
	source := filepath.Join(root, "main.go")
	if err := os.WriteFile(source, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	drafts := make(map[string][]byte)
	for name, contents := range map[string]string{"draft": "draft text", "reviewed": "reviewed text"} {
		path := filepath.Join(output, name+".zip")
		zipFixture(t, path, map[string]string{"main.go": contents})
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		drafts[name] = data
	}
	passes := make(chan string, 2)
	var projectUploads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/files":
			f, header, err := r.FormFile("file")
			if err != nil {
				t.Error(err)
				http.Error(w, "missing input ZIP", 400)
				return
			}
			defer f.Close()
			if header.Filename == "project.zip" {
				projectUploads.Add(1)
			}
			fmt.Fprintf(w, `{"id":%q}`, header.Filename)
		case r.URL.Path == "/responses":
			var body struct {
				Input        string
				Instructions string
				Tools        []struct {
					Container struct {
						FileIDs []string `json:"file_ids"`
					}
				}
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Tools) != 1 {
				t.Errorf("invalid provider request: %+v, %v", body, err)
				http.Error(w, "bad request", 400)
				return
			}
			id := "draft"
			want := []string{"project.zip"}
			if strings.Contains(body.Instructions, "draft.zip") {
				id = "reviewed"
				want = append(want, "draft.zip")
			}
			if !reflect.DeepEqual(body.Tools[0].Container.FileIDs, want) || !strings.Contains(body.Input, "Fix main") {
				t.Errorf("unexpected %s request: %+v", id, body)
			}
			passes <- id
			fmt.Fprintf(w, "data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":10},\"output\":[{\"type\":\"code_interpreter_call\",\"container_id\":%q}]}}\n\ndata: [DONE]\n\n", id)
		case strings.HasSuffix(r.URL.Path, "/files"):
			fmt.Fprint(w, `{"data":[{"id":"input","path":"/mnt/data/project.zip","source":"user"},{"id":"draft","path":"/mnt/data/draft.zip","source":"user"},{"id":"result","path":"/mnt/data/revised-project.zip","source":"assistant"}]}`)
		case strings.HasSuffix(r.URL.Path, "/files/result/content"):
			_, _ = w.Write(drafts[strings.Split(r.URL.Path, "/")[2]])
		case r.Method == "DELETE":
			fmt.Fprint(w, `{"deleted":true}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("PIPKIN_OUTPUT_DIR", output)
	t.Setenv("PIPKIN_FAKE_KEY", "test")
	config := filepath.Join(output, "config.json")
	t.Setenv("PIPKIN_CONFIG", config)
	t.Setenv("LANGFUSE_BASE_URL", "")
	if err := os.WriteFile(config, []byte(fmt.Sprintf(`{"provider":"openai-archive","base_url":%q,"api_key_env":"PIPKIN_FAKE_KEY"}`, server.URL)), 0600); err != nil {
		t.Fatal(err)
	}
	var jobDir string
	ctx := withWorkflowEvents(context.Background(), func(event workflowEvent) {
		if event.Type == "job" {
			jobDir = event.Job
		}
	})
	if err := workflowSendFiles(ctx, root, []*Node{{name: "main.go", rel: "main.go"}}, "Fix main", true); err != nil {
		t.Fatal(err)
	}
	if count := projectUploads.Load(); count != 1 {
		t.Fatalf("uploaded the original project %d times", count)
	}
	close(passes)
	var got []string
	for pass := range passes {
		got = append(got, pass)
	}
	if !reflect.DeepEqual(got, []string{"draft", "reviewed"}) {
		t.Fatalf("passes: %v", got)
	}
	data, err := os.ReadFile(filepath.Join(jobDir, "job.json"))
	if err != nil {
		t.Fatal(err)
	}
	var job WorkflowJob
	if err := json.Unmarshal(data, &job); err != nil || !job.Agentic || job.Status != "completed" || job.InputTokens != 20 {
		t.Fatalf("unexpected saved session: %+v (%v)", job, err)
	}
	review, err := workflowReview(jobDir)
	if err != nil || len(review.Files) != 1 {
		t.Fatalf("review: %+v (%v)", review, err)
	}
	result, err := os.ReadFile(review.Files[0].After)
	if err != nil || string(result) != "reviewed text" {
		t.Fatalf("reviewed file: %q (%v)", result, err)
	}
	current, err := os.ReadFile(source)
	if err != nil || string(current) != "original" {
		t.Fatalf("working tree: %q (%v)", current, err)
	}
}

func TestVLLMAgenticDelivery(t *testing.T) {
	root := t.TempDir()
	output := t.TempDir()
	input := filepath.Join(output, "input.zip")
	result := filepath.Join(output, "result.zip")
	zipFixture(t, input, map[string]string{"README.md": "starting README"})
	t.Setenv("PIPKIN_MODEL", "")
	t.Setenv("OPENAI_MODEL", "")
	t.Setenv("OPENAI_API_KEY", "must-not-be-sent")
	config := filepath.Join(output, "config.json")
	t.Setenv("PIPKIN_CONFIG", config)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "" {
			t.Error("unexpected local endpoint or authentication")
		}
		var body struct {
			Model    string
			Messages []struct{ Content string }
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Messages) != 2 || body.Model != "local-model" {
			t.Errorf("unexpected local request: %+v (%v)", body, err)
			http.Error(w, "invalid request", 400)
			return
		}
		calls.Add(1)
		request := body.Messages[1].Content
		if os.Getenv("PIPKIN_TEST_LANGGRAPH") == "1" {
			if strings.Contains(request, "Original request:") {
				if !strings.Contains(request, "--- ORIGINAL FILE: README.md ---\nstarting README") ||
					!strings.Contains(request, "Additional review focus: compatibility") {
					t.Error("text reviewer did not receive review focus and original changed file")
				}
			} else if !strings.Contains(request, "Additional implementation focus: preserve callers") {
				t.Error("draft did not receive implementation focus")
			}
		}
		if strings.Index(request, "PROJECT FILES") != 0 || strings.Index(request, "USER REQUEST:") < strings.Index(request, "--- FILE: README.md ---") {
			t.Errorf("file context must precede request: %s", request)
		}
		var replacement string
		switch {
		case strings.Contains(request, "reviewed README") && strings.Contains(request, "Additional review focus: compatibility"):
			replacement = "reviewed README"
		case strings.Contains(request, "draft README") && strings.Contains(request, "Original request"):
			replacement = "reviewed README"
		case strings.Contains(request, "starting README") && strings.Contains(request, "Update README"):
			replacement = "draft README"
		default:
			t.Errorf("unexpected request: %s", request)
			http.Error(w, "invalid prompt", 400)
			return
		}
		payload, _ := json.Marshal(Changes{Changes: []Change{{Path: "README.md", Content: replacement}}})
		fmt.Fprintf(w, `{"usage":{"prompt_tokens":20,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":8}},"choices":[{"message":{"content":%q}}]}`, payload)
	}))
	defer server.Close()
	if err := os.WriteFile(config, []byte(fmt.Sprintf(`{"provider":"vllm","base_url":%q,"model":"local-model"}`, server.URL+"/v1")), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := loadConfig()
	if os.Getenv("PIPKIN_TEST_LANGGRAPH") == "1" {
		cfg.AgentProfile = "integration"
		cfg.AgentProfiles = map[string]AgentProfile{"integration": {Runner: "langgraph", ReviewPasses: 3, ReviewFocus: "compatibility", DraftFocus: "preserve callers", ReviewContextBytes: 4096}}
	}
	job := WorkflowJob{}
	if err := agenticDelivery(context.Background(), cfg, root, input, result, "Update README", &job); err != nil {
		t.Fatal(err)
	}
	wantCalls := int32(2)
	if os.Getenv("PIPKIN_TEST_LANGGRAPH") == "1" {
		wantCalls = 3 // draft, changed review, unchanged review. The last review is skipped.
	}
	if got := calls.Load(); got != wantCalls {
		t.Fatalf("model requests: got %d, want %d", got, wantCalls)
	}
	if job.InputTokens != int(wantCalls)*20 || job.OutputTokens != int(wantCalls)*5 || job.CachedTokens != int(wantCalls)*8 {
		t.Fatalf("unexpected usage: %+v", job)
	}
	files, err := readArchive(result)
	if err != nil || string(files["README.md"]) != "reviewed README" {
		t.Fatalf("unexpected result: %v (%v)", files, err)
	}
}

func TestNormalizeTar(t *testing.T) {
	for _, name := range []string{"./README.md", "../README.md"} {
		t.Run(name, func(t *testing.T) {
			var source bytes.Buffer
			gzipWriter := gzip.NewWriter(&source)
			writer := tar.NewWriter(gzipWriter)
			if err := writer.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: 7}); err != nil {
				t.Fatal(err)
			}
			if _, err := writer.Write([]byte("updated")); err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			if err := gzipWriter.Close(); err != nil {
				t.Fatal(err)
			}
			output := filepath.Join(t.TempDir(), "result.zip")
			err := normalizeTar(&source, "tar.gz", output)
			if name == "../README.md" {
				if err == nil {
					t.Fatal("accepted an unsafe entry")
				}
				if _, statErr := os.Stat(output); !os.IsNotExist(statErr) {
					t.Fatalf("partial ZIP left behind: %v", statErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			files, err := readArchive(output)
			if err != nil || string(files["README.md"]) != "updated" {
				t.Fatalf("unexpected archive: %v (%v)", files, err)
			}
		})
	}
}

func TestHostedDraftFallsBackToText(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "input.zip")
	result := filepath.Join(root, "result.zip")
	zipFixture(t, input, map[string]string{"README.md": "original"})
	t.Setenv("PIPKIN_TEST_KEY", "dummy")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/files":
			fmt.Fprint(w, `{"id":"input"}`)
		case "/files/input":
			fmt.Fprint(w, `{"deleted":true}`)
		case "/responses":
			fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"output\":[{\"type\":\"code_interpreter_call\",\"container_id\":\"container\"}]}}\n\n")
		case "/containers/container/files":
			fmt.Fprint(w, `{"data":[{"id":"input","path":"/mnt/data/project.zip","source":"user"}]}`)
		case "/chat/completions":
			fmt.Fprint(w, `{"choices":[{"message":{"content":"{\"changes\":[{\"path\":\"README.md\",\"content\":\"text fallback\",\"delete\":false}]}"}}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	cfg := Config{Provider: "openai-archive", BaseURL: server.URL, Model: "test", APIKeyEnv: "PIPKIN_TEST_KEY", MaxContextBytes: 160000}
	var job WorkflowJob
	trace := &llmTrace{}
	ctx := context.WithValue(context.Background(), llmTraceKey{}, trace)
	if err := agenticDelivery(ctx, cfg, root, input, result, "Edit README", &job); err != nil {
		t.Fatal(err)
	}
	if len(trace.calls) != 3 || trace.calls[0].name != "draft/responses" || !trace.calls[0].failed ||
		trace.calls[1].name != "draft/chat-completions" || trace.calls[1].failed ||
		trace.calls[2].name != "review-1/chat-completions" || trace.calls[2].failed {
		t.Fatalf("unexpected trace calls: %+v", trace.calls)
	}

	files, err := readArchive(result)
	if err != nil || !job.TextFallback || string(files["README.md"]) != "text fallback" {
		t.Fatalf("fallback did not finish: %v, %v, %v", job.TextFallback, files, err)
	}
}
