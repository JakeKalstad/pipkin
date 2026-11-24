package main

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

//go:embed agent_graph.py
var agentGraphScript string

type AgentProfile struct {
	Runner       string `json:"runner"`
	ReviewPasses int    `json:"review_passes"`
	ReviewFocus  string `json:"review_focus,omitempty"`
}

func (c Config) activeAgentProfile() (string, AgentProfile, error) {
	name := c.AgentProfile
	if override := strings.TrimSpace(os.Getenv("PIPKIN_AGENT_PROFILE")); override != "" {
		name = override
	}
	if name == "" || name == "builtin" {
		return "builtin", AgentProfile{Runner: "builtin", ReviewPasses: 1}, nil
	}
	profile, ok := c.AgentProfiles[name]
	if !ok {
		return "", AgentProfile{}, fmt.Errorf("unknown agent profile %q", name)
	}
	if profile.Runner != "langgraph" {
		return "", AgentProfile{}, fmt.Errorf("agent profile %q must set runner to langgraph", name)
	}
	if profile.ReviewPasses < 1 || profile.ReviewPasses > 3 {
		return "", AgentProfile{}, fmt.Errorf("agent profile %q requires 1–3 review passes", name)
	}
	return name, profile, nil
}

func listAgentProfiles(w io.Writer, cfg Config) error {
	active, _, err := cfg.activeAgentProfile()
	if err != nil {
		return err
	}
	names := []string{"builtin"}
	for name := range cfg.AgentProfiles {
		if name != "builtin" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		status := ""
		if name == active {
			status = " (active)"
		}
		fmt.Fprintln(w, name+status)
	}
	return nil
}

func runAgentGraph(ctx context.Context, python string, passes int, stage func(string) error) error {
	if python == "" {
		python = "python3"
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "-u", "-c", agentGraphScript, strconv.Itoa(passes))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start LangGraph: %w", err)
	}
	scanner := bufio.NewScanner(stdout)
	count := 0
	var stageErr error
	for scanner.Scan() {
		var message struct{ Stage string }
		expected := "review"
		if count == 0 {
			expected = "draft"
		}
		if err := json.Unmarshal(scanner.Bytes(), &message); err != nil || message.Stage != expected || count > passes {
			stageErr = errors.New("LangGraph returned an unexpected stage")
			break
		}
		if err := stage(message.Stage); err != nil {
			stageErr = err
			break
		}
		count++
		if _, err := io.WriteString(stdin, "ok\n"); err != nil {
			stageErr = err
			break
		}
	}
	if stageErr == nil {
		stageErr = scanner.Err()
	}
	_ = stdin.Close()
	if stageErr != nil {
		cancel()
	}
	waitErr := cmd.Wait()
	if stageErr != nil {
		return stageErr
	}
	if waitErr != nil {
		return fmt.Errorf("LangGraph runner: %w: %s", waitErr, strings.TrimSpace(stderr.String()))
	}
	if count != passes+1 {
		return errors.New("LangGraph exited before finishing the delivery")
	}
	return nil
}
