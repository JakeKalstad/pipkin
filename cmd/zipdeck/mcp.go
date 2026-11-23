package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// MCP runs over stdio. Only JSON-RPC responses go to stdout; all workflow
// progress is routed to stderr to avoid corrupting the MCP transport.
type mcpRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}
type mcpCall struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}
type mcpArgs struct {
	Root   string   `json:"root"`
	Paths  []string `json:"paths"`
	Prompt string   `json:"prompt"`
	Job    string   `json:"job"`
	File   string   `json:"file"`
}

func mcpTool(name, description string, properties map[string]any, required ...string) map[string]any {
	return map[string]any{"name": name, "description": description, "inputSchema": map[string]any{"type": "object", "properties": properties, "required": required}}
}
func mcpTools() []map[string]any {
	root := map[string]any{"type": "string", "description": "Absolute path of local project root"}
	paths := map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Explicit project-relative file or directory paths"}
	job := map[string]any{"type": "string", "description": "Absolute path to a Zipdeck saved job directory"}
	return []map[string]any{
		mcpTool("zipdeck_pack", "Create a ZIP from explicitly selected files and directories, without changing the repository", map[string]any{"root": root, "paths": paths}, "root", "paths"),
		mcpTool("zipdeck_submit", "Submit selected files as a ZIP with an instruction prompt. This invokes the configured external LLM and incurs API usage. Saves input, result and metadata separately, without applying changes", map[string]any{"root": root, "paths": paths, "prompt": map[string]any{"type": "string"}}, "root", "paths", "prompt"),
		mcpTool("zipdeck_jobs", "List saved Zipdeck sessions for a project", map[string]any{"root": root}, "root"),
		mcpTool("zipdeck_review", "Review changed files in a completed Zipdeck session, without applying them", map[string]any{"job": job}, "job"),
		mcpTool("zipdeck_apply", "Apply ONE reviewed file to the working tree after explicit approval. Disabled by default; enable using ZIPDECK_MCP_ALLOW_APPLY=1", map[string]any{"job": job, "file": map[string]any{"type": "string", "description": "Project-relative file to apply"}}, "job", "file"),
	}
}
func mcpResult(v any, err error) map[string]any {
	if err != nil {
		return map[string]any{"isError": true, "content": []any{map[string]any{"type": "text", "text": err.Error()}}}
	}
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		b = []byte(fmt.Sprint(v))
	}
	return map[string]any{"content": []any{map[string]any{"type": "text", "text": string(b)}}}
}
func mcpProject(root string) (string, error) {
	if !filepath.IsAbs(root) {
		return "", errors.New("root must be an absolute path")
	}
	root = filepath.Clean(root)
	st, e := os.Stat(root)
	if e != nil {
		return "", e
	}
	if !st.IsDir() {
		return "", errors.New("root is not a directory")
	}
	return root, nil
}
func mcpJob(job string) (string, error) {
	if !filepath.IsAbs(job) {
		return "", errors.New("job must be an absolute path")
	}
	job = filepath.Clean(job)
	// A job is only valid beneath the configured output directory.
	base := filepath.Join(outputDirectory(), "jobs")
	rel, err := filepath.Rel(base, job)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("job outside configured output directory")
	}
	// Do not follow a symlink supplied as the job directory.
	st, err := os.Lstat(job)
	if err != nil {
		return "", err
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("invalid job directory")
	}
	return job, nil
}
func mcpCallTool(call mcpCall) (any, error) {
	var a mcpArgs
	if err := json.Unmarshal(call.Arguments, &a); err != nil {
		return nil, err
	}
	switch call.Name {
	case "zipdeck_pack", "zipdeck_submit":
		root, err := mcpProject(a.Root)
		if err != nil {
			return nil, err
		}
		if len(a.Paths) == 0 {
			return nil, errors.New("at least one selected path is required")
		}
		files, err := filesFromPaths(root, a.Paths)
		if err != nil {
			return nil, err
		}
		if call.Name == "zipdeck_pack" {
			if err = os.MkdirAll(outputDirectory(), 0700); err != nil {
				return nil, err
			}
			f, err := os.CreateTemp(outputDirectory(), filepath.Base(root)+"-selected-*.zip")
			if err != nil {
				return nil, err
			}
			dest := f.Name()
			f.Close()
			if err = writeZip(root, files, dest); err != nil {
				os.Remove(dest)
				return nil, err
			}
			return map[string]any{"zip": dest, "files": len(files)}, nil
		}
		if strings.TrimSpace(a.Prompt) == "" {
			return nil, errors.New("prompt is required")
		}
		// Reuse the standard workflow and preserve its per-job artifacts.
		tmp, err := os.MkdirTemp("", "zipdeck-mcp-")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(tmp)
		mb, _ := json.Marshal(a.Paths)
		mf := filepath.Join(tmp, "files.json")
		pf := filepath.Join(tmp, "prompt.txt")
		if err = os.WriteFile(mf, mb, 0600); err != nil {
			return nil, err
		}
		if err = os.WriteFile(pf, []byte(a.Prompt), 0600); err != nil {
			return nil, err
		}
		// Capture this call's job event rather than guessing which concurrent job is newest.
		var events bytes.Buffer
		previous := eventOutput
		eventOutput = io.MultiWriter(os.Stderr, &events)
		err = workflowSend(root, mf, pf)
		eventOutput = previous
		if err != nil {
			return nil, err
		}
		var jobPath string
		for _, line := range strings.Split(events.String(), "\n") {
			var ev workflowEvent
			if json.Unmarshal([]byte(line), &ev) == nil && ev.Type == "job" {
				jobPath = ev.Job
				break
			}
		}
		if jobPath == "" {
			return nil, errors.New("workflow finished without saved session")
		}
		return map[string]any{"job": jobPath, "result": filepath.Join(jobPath, "result.zip")}, nil
	case "zipdeck_jobs":
		root, err := mcpProject(a.Root)
		if err != nil {
			return nil, err
		}
		return mcpListJobs(root)
	case "zipdeck_review":
		job, err := mcpJob(a.Job)
		if err != nil {
			return nil, err
		}
		return workflowReview(job)
	case "zipdeck_apply":
		if os.Getenv("ZIPDECK_MCP_ALLOW_APPLY") != "1" {
			return nil, errors.New("MCP apply disabled. Review changes and use the CLI or VSCodium to approve; set ZIPDECK_MCP_ALLOW_APPLY=1 only when you explicitly want agents to apply files")
		}
		job, err := mcpJob(a.Job)
		if err != nil {
			return nil, err
		}
		if a.File == "" || !safeRel(a.File) {
			return nil, errors.New("valid file path required")
		}
		if err = workflowApply(job, a.File); err != nil {
			return nil, err
		}
		return map[string]any{"applied": a.File}, nil
	default:
		return nil, fmt.Errorf("unknown tool: %s", call.Name)
	}
}
func filesFromPaths(root string, paths []string) ([]*Node, error) {
	tmp, err := os.CreateTemp("", "zipdeck-manifest-*.json")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name())
	if err = json.NewEncoder(tmp).Encode(paths); err != nil {
		tmp.Close()
		return nil, err
	}
	if err = tmp.Close(); err != nil {
		return nil, err
	}
	return filesFromJSON(root, tmp.Name())
}
func mcpListJobs(root string) ([]string, error) {
	base := filepath.Join(outputDirectory(), "jobs", filepath.Base(root))
	entries, err := os.ReadDir(base)
	if os.IsNotExist(err) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	jobs := []string{}
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "job-") {
			jobs = append(jobs, filepath.Join(base, e.Name()))
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(jobs)))
	return jobs, nil
}
func serveMCP(r io.Reader, w io.Writer) error {
	old := eventOutput
	eventOutput = os.Stderr
	defer func() { eventOutput = old }()
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 4096), 8<<20)
	encoder := json.NewEncoder(w)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var req mcpRequest
		if err := json.Unmarshal(line, &req); err != nil {
			_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": nil, "error": map[string]any{"code": -32700, "message": "invalid JSON"}})
			continue
		}
		if len(req.ID) == 0 || string(req.ID) == "null" {
			continue
		} // notifications
		var result any
		var callErr error
		switch req.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{"tools": map[string]any{"listChanged": false}}, "serverInfo": map[string]any{"name": "zipdeck", "version": "0.3.0"}}
		case "ping":
			result = map[string]any{}
		case "tools/list":
			result = map[string]any{"tools": mcpTools()}
		case "tools/call":
			var params mcpCall
			if err := json.Unmarshal(req.Params, &params); err != nil {
				callErr = err
			} else {
				v, e := mcpCallTool(params)
				result = mcpResult(v, e)
			}
		default:
			callErr = fmt.Errorf("method not found: %s", req.Method)
		}
		msg := map[string]any{"jsonrpc": "2.0", "id": req.ID}
		if callErr != nil {
			msg["error"] = map[string]any{"code": -32601, "message": callErr.Error()}
		} else {
			msg["result"] = result
		}
		if err := encoder.Encode(msg); err != nil {
			return err
		}
	}
	return sc.Err()
}
