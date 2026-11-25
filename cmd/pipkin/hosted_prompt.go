package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strings"
)

func validateFreePromptTools(names []string) error {
	for i, name := range names {
		if (name != "web_search" && name != "code_interpreter") || slices.Contains(names[:i], name) {
			return fmt.Errorf("invalid or duplicate free_prompt_tools entry %q", name)
		}
	}
	return nil
}

func hostedFreePrompt(ctx context.Context, cfg Config, system string, messages []chatMessage, job *WorkflowJob) (_ string, err error) {
	if err := validateFreePromptTools(cfg.FreePromptTools); err != nil {
		return "", err
	}
	base, err := url.Parse(cfg.BaseURL)
	if err != nil || base.Scheme != "https" || base.Host != "api.openai.com" || base.User != nil ||
		(base.Path != "/v1" && base.Path != "/v1/") || base.RawQuery != "" || base.Fragment != "" ||
		(cfg.Provider != "openai" && cfg.Provider != "openai-archive") {
		return "", fmt.Errorf("free_prompt_tools requires an OpenAI profile using https://api.openai.com/v1")
	}
	key := os.Getenv(cfg.APIKeyEnv)
	if key == "" {
		return "", fmt.Errorf("missing %s", cfg.APIKeyEnv)
	}
	tools := make([]map[string]any, 0, len(cfg.FreePromptTools))
	for _, name := range cfg.FreePromptTools {
		tool := map[string]any{"type": name}
		if name == "code_interpreter" {
			tool["container"] = map[string]string{"type": "auto"}
		}
		tools = append(tools, tool)
	}
	payload := map[string]any{"model": cfg.Model, "instructions": system, "input": messages,
		"store": false, "max_tool_calls": 8, "tools": tools}
	if cfg.MaxOutputTokens > 0 {
		payload["max_output_tokens"] = cfg.MaxOutputTokens
	}
	var response struct {
		Status string
		Output []struct {
			Type    string
			Content []struct {
				Type, Text  string
				Annotations []struct{ Type, URL string }
			}
		}
		Usage struct {
			Input  int `json:"input_tokens"`
			Output int `json:"output_tokens"`
		}
	}
	var reply string
	defer recordLLMCall(ctx, cfg, "responses", job, &err, "", &reply)()
	if err := apiJSON(ctx, "POST", strings.TrimRight(cfg.BaseURL, "/")+"/responses", key, payload, &response); err != nil {
		return "", err
	}
	job.InputTokens, job.OutputTokens = response.Usage.Input, response.Usage.Output
	var parts, sources []string
	for _, item := range response.Output {
		if item.Type != "message" {
			continue
		}
		for _, content := range item.Content {
			if content.Type != "output_text" {
				continue
			}
			parts = append(parts, content.Text)
			for _, citation := range content.Annotations {
				if citation.Type == "url_citation" && isSourceURL(citation.URL) && !slices.Contains(sources, citation.URL) {
					sources = append(sources, citation.URL)
				}
			}
		}
	}
	reply = strings.TrimSpace(strings.Join(parts, "\n\n"))
	if reply == "" || response.Status != "completed" {
		return "", fmt.Errorf("hosted free prompt did not complete with text (status %q)", response.Status)
	}
	if len(sources) > 0 {
		reply += "\n\nSources\n" + strings.Join(sources, "\n")
	}
	return reply, nil
}

func isSourceURL(value string) bool {
	u, err := url.Parse(value)
	return err == nil && u.Hostname() != "" && u.User == nil && !strings.ContainsAny(value, " \r\n\t") &&
		(u.Scheme == "http" || u.Scheme == "https")
}

func freePromptSources(reply string) []string {
	_, sources, ok := strings.Cut(reply, "\n\nSources\n")
	if !ok {
		return nil
	}
	var links []string
	for _, line := range strings.Split(sources, "\n") {
		if isSourceURL(line) {
			links = append(links, line)
		}
	}
	return links
}
