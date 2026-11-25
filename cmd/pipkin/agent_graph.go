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
	VerificationFocus  string `json:"verification_focus,omitempty"`
	SystemPrompt       string `json:"system_prompt,omitempty"`
	Runner             string `json:"runner"`
	ReviewPasses       int    `json:"review_passes"`
	DraftFocus         string `json:"draft_focus,omitempty"`
	ReviewContextBytes int    `json:"review_context_bytes,omitempty"`
	ReviewFocus        string `json:"review_focus,omitempty"`
}

func (c Config) activeAgentProfile() (string, AgentProfile, error) {
	name := c.AgentProfile
	if override := strings.TrimSpace(os.Getenv("PIPKIN_AGENT_PROFILE")); override != "" {
		name = override
	}
	if name == "" || name == "project" {
		name = "builtin"
	}
	profile, err := c.agentProfileByName(name)
	return name, profile, err
}

func (c Config) agentProfileByName(name string) (AgentProfile, error) {
	profile, ok := c.AgentProfiles[name]
	if !ok {
		switch name {
		case "builtin":
			profile, ok = AgentProfile{Runner: "builtin", ReviewPasses: 1}, true
		case "focused":
			profile, ok = AgentProfile{Runner: "langgraph", ReviewPasses: 1, ReviewContextBytes: 4096}, true
		case "thorough":
			profile, ok = AgentProfile{Runner: "langgraph", ReviewPasses: 2, ReviewContextBytes: 8192}, true
		}
	}
	if !ok {
		return AgentProfile{}, fmt.Errorf("unknown agent profile %q", name)
	}
	if name == "builtin" {
		if profile.Runner != "builtin" || profile.ReviewPasses != 1 {
			return AgentProfile{}, errors.New("builtin requires runner builtin and one review pass")
		}
	} else if profile.Runner != "langgraph" && !(strings.HasPrefix(name, "project:") && profile.Runner == "builtin") {
		return AgentProfile{}, fmt.Errorf("agent profile %q must set runner to langgraph", name)
	}
	if profile.ReviewPasses < 1 || profile.ReviewPasses > 3 {
		return AgentProfile{}, fmt.Errorf("agent profile %q requires 1–3 review passes", name)
	}
	if profile.VerificationFocus != "" && profile.ReviewPasses < 2 {
		return AgentProfile{}, fmt.Errorf("agent profile %q requires at least two passes for verification_focus", name)
	}
	if profile.ReviewContextBytes < 0 || profile.ReviewContextBytes > 16384 {
		return AgentProfile{}, fmt.Errorf("agent profile %q requires review_context_bytes between 0 and 16384", name)
	}
	return profile, nil
}

func listAgentProfiles(w io.Writer, cfg Config) error {
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	active, _, err := cfg.deliveryAgentProfile(root)
	if err != nil {
		return err
	}
	names := []string{"builtin", "focused", "project", "thorough"}
	for name := range cfg.AgentProfiles {
		if name != "builtin" && name != "focused" && name != "thorough" && name != "project" {
			names = append(names, name)
		}
	}
	if strings.HasPrefix(active, "project:") {
		if _, custom := cfg.AgentProfiles[active]; !custom {
			names = append(names, active)
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

func runAgentGraph(ctx context.Context, python string, passes int, verify bool, stage func(string) (bool, error)) error {
	if python == "" {
		python = "python3"
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "-u", "-c", agentGraphScript, strconv.Itoa(passes), strconv.FormatBool(verify))
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
	finished := false
	for scanner.Scan() {
		var message struct{ Stage string }
		expected := "review"
		if count == 0 {
			expected = "draft"
		} else if verify && count == passes {
			expected = "verify"
		}
		if err := json.Unmarshal(scanner.Bytes(), &message); err != nil || message.Stage != expected || count > passes || finished {
			stageErr = errors.New("LangGraph returned an unexpected stage")
			break
		}
		stop, err := stage(message.Stage)
		if err != nil {
			stageErr = err
			break
		}
		count++
		reply := "ok\n"
		if stop {
			reply = "stop\n"
			finished = true
		}
		if _, err := io.WriteString(stdin, reply); err != nil {
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
	if count != passes+1 && !finished {
		return errors.New("LangGraph exited before finishing the delivery")
	}
	return nil
}
