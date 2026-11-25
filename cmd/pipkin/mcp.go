package main

import (
	"bufio"
	"bytes"
	"context"
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
	ProjectProfile   string          `json:"profile"`
	Output           string          `json:"output"`
	Edits            json.RawMessage `json:"edits"`
	Conversation     string          `json:"conversation"`
	Action           string          `json:"action"`
	Markdown         *string         `json:"markdown"`
	ProviderProfile  string          `json:"provider_profile"`
	SystemPrompt     string          `json:"system_prompt"`
	InstructionFiles []string        `json:"instruction_files"`
	Root             string          `json:"root"`
	Paths            []string        `json:"paths"`
	Agentic          bool            `json:"agentic"`
	Prompt           string          `json:"prompt"`
	Job              string          `json:"job"`
	File             string          `json:"file"`
}

func mcpTool(name, description string, properties map[string]any, required ...string) map[string]any {
	return map[string]any{"name": name, "description": description, "inputSchema": map[string]any{"type": "object", "properties": properties, "required": required}}
}
func mcpTools() []map[string]any {
	root := map[string]any{"type": "string", "description": "Absolute path of local project root"}
	paths := map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Explicit project-relative file or directory paths"}
	job := map[string]any{"type": "string", "description": "Absolute path to a Pipkin saved job directory"}
	text := map[string]any{"type": "string"}
	instructionFiles := map[string]any{"type": "array", "items": text}
	return []map[string]any{
		mcpTool("pipkin_mkdocs", "Create an MkDocs source ZIP from selected project files and a documentation brief", map[string]any{
			"root": root, "paths": paths, "prompt": text, "provider_profile": text,
			"system_prompt": text, "instruction_files": instructionFiles,
		}, "root", "paths", "prompt"),
		mcpTool("pipkin_project", "Show, select, or initialise the workspace project profile", map[string]any{
			"root": root, "action": map[string]any{"type": "string", "enum": []string{"show", "set", "init"}},
			"profile": text,
		}, "root", "action"),
		mcpTool("pipkin_workbook", "Inspect XLSX sheets or apply explicit cell edits to a separate workbook copy", map[string]any{
			"root": root, "file": text, "output": text,
			"edits": map[string]any{"type": "array", "items": map[string]any{"type": "object",
				"properties": map[string]any{"sheet": text, "cell": text, "value": map[string]any{},
					"kind": map[string]any{"type": "string", "enum": []string{"value", "formula"}}},
				"required": []string{"sheet", "cell", "value"}}},
		}, "root", "file"),
		mcpTool("pipkin_prompt", "Send a text-only prompt using workspace instructions. Returns the response without creating a delivery or attaching project files", map[string]any{
			"root": root, "prompt": text, "system_prompt": text, "instruction_files": instructionFiles, "provider_profile": text, "conversation": text,
		}, "root", "prompt"),
		mcpTool("pipkin_conversations", "List, read, or delete saved free-prompt conversations. Confirm deletion with the user", map[string]any{
			"root":         root,
			"action":       map[string]any{"type": "string", "enum": []string{"list", "read", "delete"}},
			"conversation": text,
		}, "root", "action"),
		mcpTool("pipkin_instructions", "List or read project instructions and skills, or save Markdown and toggle a skill after explicit approval", map[string]any{
			"root": root, "action": map[string]any{"type": "string", "enum": []string{"list", "read", "save", "toggle"}},
			"file": map[string]any{"type": "string", "description": "AGENTS.md or .agents/skills/<name>/SKILL.md, required except for list"}, "markdown": text,
		}, "root", "action"),
		mcpTool("pipkin_pack", "Create a ZIP from explicitly selected files and directories, without changing the repository", map[string]any{"root": root, "paths": paths}, "root", "paths"),
		mcpTool("pipkin_submit", "Submit selected files as a ZIP with an instruction prompt. This invokes the configured external LLM and incurs API usage. Saves input, result and metadata separately, without applying changes", map[string]any{"root": root, "paths": paths, "prompt": map[string]any{"type": "string"}, "agentic": map[string]any{"type": "boolean", "description": "Run a draft and review with a supported text or archive provider"}, "system_prompt": map[string]any{"type": "string", "description": "Explicit guidance appended to the system instructions for this delivery"}, "instruction_files": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Ordered project-relative Markdown paths, including hidden skill paths"}}, "root", "paths", "prompt"),
		mcpTool("pipkin_jobs", "List saved Pipkin sessions for a project", map[string]any{"root": root}, "root"),
		mcpTool("pipkin_review", "Review changed files in a completed Pipkin session, without applying them", map[string]any{"job": job}, "job"),
		mcpTool("pipkin_apply", "Apply ONE reviewed file to the working tree after explicit approval. Disabled by default; enable using PIPKIN_MCP_ALLOW_APPLY=1", map[string]any{"job": job, "file": map[string]any{"type": "string", "description": "Project-relative file to apply"}}, "job", "file"),
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
	case "pipkin_mkdocs":
		root, err := mcpProject(a.Root)
		if err != nil {
			return nil, err
		}
		files, err := filesFromPaths(root, a.Paths)
		if err != nil {
			return nil, err
		}
		cfg := loadProviderConfig(a.ProviderProfile)
		cfg.instructionRequest = instructionSettings{SystemPrompt: a.SystemPrompt, Files: a.InstructionFiles}
		ctx := withWorkflowEvents(context.Background(), func(workflowEvent) {})
		return exportMkDocs(ctx, root, files, a.Prompt, cfg)
	case "pipkin_project", "pipkin_workbook":
		root, err := mcpProject(a.Root)
		if err != nil {
			if call.Name != "pipkin_project" || a.Action != "init" || !filepath.IsAbs(a.Root) || !errors.Is(err, os.ErrNotExist) {
				return nil, err
			}
			root = filepath.Clean(a.Root)
		}
		if call.Name == "pipkin_workbook" {
			return workbookOperation(context.Background(), root, a.File, a.Output, a.Edits)
		}
		switch a.Action {
		case "show":
		case "set":
			err = saveProject(root, a.ProjectProfile)
		case "init":
			err = initializeProject(root, a.ProjectProfile)
		default:
			return nil, fmt.Errorf("unknown project action %q", a.Action)
		}
		if err != nil {
			return nil, err
		}
		return readProject(root)
	case "pipkin_prompt", "pipkin_instructions", "pipkin_conversations":
		root, err := mcpProject(a.Root)
		if err != nil {
			return nil, err
		}
		if call.Name == "pipkin_instructions" {
			return mcpInstructions(root, a)
		}
		if call.Name == "pipkin_conversations" {
			dir, err := conversationDirectory(root)
			if err != nil {
				return nil, err
			}
			switch a.Action {
			case "list":
				return listConversations(dir)
			case "read":
				return readConversation(dir, a.Conversation)
			case "delete":
				return map[string]string{"deleted": a.Conversation}, deleteConversation(dir, a.Conversation)
			default:
				return nil, fmt.Errorf("unknown conversation action %q", a.Action)
			}
		}
		cfg := loadProviderConfig(a.ProviderProfile)
		cfg.instructionRequest = instructionSettings{SystemPrompt: a.SystemPrompt, Files: a.InstructionFiles}
		thread, reply, err := sendConversation(context.Background(), root, a.Conversation, a.Prompt, cfg)
		return map[string]string{"reply": reply, "conversation": thread.ID}, err
	case "pipkin_pack", "pipkin_submit":
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
		if call.Name == "pipkin_pack" {
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
		cfg := loadConfig()
		cfg.instructionRequest = instructionSettings{SystemPrompt: a.SystemPrompt, Files: a.InstructionFiles}
		var jobPath string
		ctx := withWorkflowEvents(context.Background(), func(event workflowEvent) {
			writeWorkflowEvent(os.Stderr, event)
			if event.Type == "job" {
				jobPath = event.Job
			}
		})
		if err := workflowSendConfigured(ctx, root, files, a.Prompt, a.Agentic, cfg); err != nil {
			return nil, err
		}
		if jobPath == "" {
			return nil, errors.New("workflow finished without saved session")
		}
		return map[string]any{"job": jobPath, "result": filepath.Join(jobPath, "result.zip")}, nil
	case "pipkin_jobs":
		root, err := mcpProject(a.Root)
		if err != nil {
			return nil, err
		}
		return mcpListJobs(root)
	case "pipkin_review":
		job, err := mcpJob(a.Job)
		if err != nil {
			return nil, err
		}
		return workflowReview(job)
	case "pipkin_apply":
		if os.Getenv("PIPKIN_MCP_ALLOW_APPLY") != "1" {
			return nil, errors.New("MCP apply disabled. Review changes and use the CLI or VSCodium to approve; set PIPKIN_MCP_ALLOW_APPLY=1 only when you explicitly want agents to apply files")
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
	return selectPaths(root, paths, false)
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
			result = map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{"tools": map[string]any{"listChanged": false}}, "serverInfo": map[string]any{"name": "pipkin", "version": buildMetadata().Version}}
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

func mcpInstructions(root string, args mcpArgs) (any, error) {
	switch args.Action {
	case "list":
		settings, err := loadInstructionSettings(root)
		if err != nil {
			return nil, err
		}
		return map[string]any{"project": projectInstructionsFile, "skills": workspaceSkills(root), "enabled_files": settings.Files, "skip_project_instructions": settings.SkipProjectInstructions}, nil
	case "read":
		text, err := standardInstructionDraft(root, args.File)
		return map[string]string{"file": args.File, "markdown": text}, err
	case "save":
		if args.Markdown == nil {
			return nil, errors.New("markdown is required when saving")
		}
		return map[string]string{"file": args.File}, saveStandardInstruction(root, args.File, *args.Markdown)
	case "toggle":
		return map[string]string{"file": args.File}, toggleStandardSkill(root, args.File)
	default:
		return nil, fmt.Errorf("unknown instruction action %q", args.Action)
	}
}
