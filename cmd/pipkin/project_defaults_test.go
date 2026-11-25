package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestProjectDefaultsCoverage(t *testing.T) {
	t.Setenv("PIPKIN_AGENT_PROFILE", "")
	t.Setenv("PIPKIN_OUTPUT_DIR", t.TempDir())
	python := os.Getenv("PIPKIN_TEST_PROJECT_LANGGRAPH")
	for _, project := range projectProfiles {
		t.Run(project.ID, func(t *testing.T) {
			root := t.TempDir()
			if err := saveProject(root, project.ID); err != nil {
				t.Fatal(err)
			}
			cfg := Config{Provider: "vllm", LangGraphPython: python}
			name, agent, err := cfg.deliveryAgentProfile(root)
			if err != nil || name != "project:"+project.ID || agent.ReviewPasses < 1 ||
				agent.DraftFocus == "" || agent.ReviewFocus == "" {
				t.Fatalf("incomplete graph defaults: %s, %+v, %v", name, agent, err)
			}
			sources, err := cfg.prepareInstructions(root)
			if err != nil {
				t.Fatal(err)
			}
			want := 1
			if project.ID != "general" {
				want = 2
			}
			found := 0
			for _, source := range sources {
				if strings.HasPrefix(source.Name, "Bundled skill: ") && len(source.SHA256) == 64 {
					found++
				}
			}
			if found != want {
				t.Fatalf("bundled sources: %+v", sources)
			}
			definition := deliveryStages(name, agent)
			expected := projectGraphs[project.ID]
			var wantStages []string
			if evidence, ok := evidenceStages[project.ID]; ok {
				wantStages = append(wantStages, evidence.Name)
			}
			wantStages = append(wantStages, expected.Plan, expected.Draft, expected.Audit)
			if project.ID != "general" {
				wantStages = append(wantStages, expected.SecondAudit, expected.Verify)
			}
			wantStages = append(wantStages, "validate_delivery")
			if python != "" {
				var stages []string
				err := runAgentGraph(context.Background(), python, definition, func(stage deliveryStage) (bool, error) {
					stages = append(stages, stage.Name)
					return false, nil
				})
				if err != nil || !slices.Equal(stages, wantStages) {
					t.Fatalf("graph stage order: %v, %v", stages, err)
				}
			}
			cfg.AgentProfile = "builtin"
			if name, _, _ := cfg.deliveryAgentProfile(root); name != "builtin" {
				t.Fatal("explicit agent selection did not override project default")
			}
			cfg.AgentProfile = "project"
			cfg.AgentProfiles = map[string]AgentProfile{fmt.Sprintf("project:%s", project.ID): {Runner: "builtin", ReviewPasses: 1, SystemPrompt: "Custom guidance"}}
			if _, custom, err := cfg.deliveryAgentProfile(root); err != nil || custom.SystemPrompt == "" {
				t.Fatalf("project override: %+v, %v", custom, err)
			}
		})
	}
}

func TestProjectRoutingRejectsMalformedMetadata(t *testing.T) {
	t.Setenv("PIPKIN_AGENT_PROFILE", "")
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, projectMetadata), []byte(`{"profile":"unknown"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := (Config{}).deliveryAgentProfile(root); err == nil {
		t.Fatal("auto routing accepted an unknown project profile")
	}
}

func TestResearchDeliveryUsesProjectVerification(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PIPKIN_AGENT_PROFILE", "")
	t.Setenv("PIPKIN_OUTPUT_DIR", t.TempDir())
	if err := saveProject(root, "research"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "findings.md"), []byte("Original findings"), 0600); err != nil {
		t.Fatal(err)
	}
	focuses := []string{"List supplied sources", "EVIDENCE INVENTORY", "Planning notes (advisory", "Audit each material claim", "Compare methods, dates, populations", "Check evidence provenance"}
	requests := make(chan string, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct{ Messages []chatMessage }
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil || len(input.Messages) < 2 {
			http.Error(w, "invalid input", http.StatusBadRequest)
			return
		}
		if !strings.Contains(input.Messages[0].Content, "Bundled skill: research-evidence") {
			t.Error("request missing bundled system guidance")
		}
		requests <- input.Messages[len(input.Messages)-1].Content
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"{\"changes\":[]}"}}]}`)
	}))
	defer server.Close()
	// The review returns no edits. The independent verification must still run.
	ctx := withWorkflowEvents(context.Background(), func(workflowEvent) {})
	cfg := Config{Provider: "vllm", BaseURL: server.URL, Model: "fixture", MaxContextBytes: 64000,
		LangGraphPython: os.Getenv("PIPKIN_TEST_PROJECT_LANGGRAPH")}
	if err := workflowSendConfigured(ctx, root, []*Node{{rel: "findings.md"}}, "Audit the findings.", true, cfg); err != nil {
		t.Fatal(err)
	}
	close(requests)
	if len(requests) != len(focuses) {
		t.Fatalf("got %d requests, want %d", len(requests), len(focuses))
	}
	for _, focus := range focuses {
		if !strings.Contains(<-requests, focus) {
			t.Errorf("missing stage focus %q", focus)
		}
	}
}
