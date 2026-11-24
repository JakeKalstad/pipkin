package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
)

type webSession struct{ ID, Project, Status, Model, Created string }
type webLine struct {
	Text string
	Kind string
}
type webFile struct {
	Path, Status, WorkspaceStatus, Decision string
	Applied                                 bool
	Diff                                    []webLine
}
type webDir struct {
	Name, Rel              string
	Dir, Ignored, Selected bool
	Children               []webDir
}
type webPage struct {
	Title, Mode, Job, Error, Notice, Root, Token string
	Sessions                                     []webSession
	Files                                        []webFile
	Dirs                                         []webDir
	JobInfo                                      WorkflowJob
	Prompt                                       string
	Links                                        []serviceLink
	AgenticAvailable                             bool
	AgentProfile                                 string
	Approved, Rejected, Pending, Applied         int
}

var reviewHTML = template.Must(template.ParseFS(webTemplateFiles, "web.html"))

type serviceLink struct {
	Label string
	URL   string
}

func configuredLinks(cfg Config) []serviceLink {
	var links []serviceLink
	if raw := strings.TrimSpace(os.Getenv("LISTEN_URL")); raw != "" {
		if addr, err := parseListen(raw); err == nil {
			links = append(links, serviceLink{"Web UI", "http://" + addr})
		}
	}
	raw := langfuseURL(cfg)
	if display := strings.TrimSpace(os.Getenv("PIPKIN_LANGFUSE_DISPLAY_URL")); display != "" && raw != "" {
		raw = display
	}
	if u, err := url.Parse(raw); err == nil && u.Hostname() != "" && u.User == nil &&
		(u.Scheme == "http" || u.Scheme == "https") && strings.IndexFunc(raw, unicode.IsControl) == -1 {
		links = append(links, serviceLink{"Langfuse", u.String()})
	}
	return links
}

func serveWebFromEnv() error {
	raw := strings.TrimSpace(os.Getenv("LISTEN_URL"))
	if raw == "" {
		return errors.New("set LISTEN_URL=http://127.0.0.1:8787 to enable the web UI")
	}
	return serveWeb(raw)
}
func parseListen(raw string) (string, error) {
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if u.Scheme != "http" || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("LISTEN_URL must be an http://loopback-host:port URL")
	}
	host, port, err := net.SplitHostPort(u.Host)
	if err != nil {
		return "", err
	}
	ip := net.ParseIP(host)
	if !(strings.EqualFold(host, "localhost") || ip != nil && ip.IsLoopback()) {
		return "", errors.New("web UI has no remote authentication; bind only to loopback")
	}
	if port == "" {
		return "", errors.New("LISTEN_URL requires port")
	}
	return u.Host, nil
}
func listWebSessions() []webSession {
	base := filepath.Join(outputDirectory(), "jobs")
	projects, err := os.ReadDir(base)
	if err != nil {
		return nil
	}
	var sessions []webSession
	for _, proj := range projects {
		if !proj.IsDir() || proj.Type()&os.ModeSymlink != 0 {
			continue
		}
		jobs, err := os.ReadDir(filepath.Join(base, proj.Name()))
		if err != nil {
			continue
		}
		for _, job := range jobs {
			if !job.IsDir() || job.Type()&os.ModeSymlink != 0 || !strings.HasPrefix(job.Name(), "job-") {
				continue
			}
			b, err := os.ReadFile(filepath.Join(base, proj.Name(), job.Name(), "job.json"))
			if err != nil {
				continue
			}
			var info WorkflowJob
			if json.Unmarshal(b, &info) != nil {
				continue
			}
			sessions = append(sessions, webSession{ID: job.Name(), Project: proj.Name(), Status: info.Status, Model: info.Model, Created: info.Created})
		}
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].Created > sessions[j].Created })
	return sessions
}
func webDiff(f ReviewFile, mode string) []webLine {
	before := f.Before
	if mode == "current" {
		before = f.Current
	}
	for _, path := range []string{before, f.After} {
		if info, err := os.Stat(path); err == nil && info.Size() > 256*1024 {
			return []webLine{{Text: "File too large for browser preview."}}
		}
	}
	var output []webLine
	inHunk := false
	for _, line := range diffLines(f, mode) {
		kind := ""
		switch {
		case !inHunk && (strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "+++ ") || strings.HasPrefix(line, "index ")):
			// The file heading already identifies the path.
			continue
		case strings.HasPrefix(line, "+"):
			kind = "added"
		case strings.HasPrefix(line, "-"):
			kind = "removed"
		case strings.HasPrefix(line, "@@"):
			inHunk = true
			kind = "hunk"
		}
		output = append(output, webLine{Text: line, Kind: kind})
	}
	return output
}

func buildWebDirTree(root, base string) ([]webDir, error) {
	entries, err := os.ReadDir(base)
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir() != entries[j].IsDir() {
			return entries[i].IsDir()
		}
		return strings.ToLower(entries[i].Name()) < strings.ToLower(entries[j].Name())
	})
	out := []webDir{}
	for _, e := range entries {
		if e.Type()&os.ModeSymlink != 0 {
			continue
		}
		name := e.Name()
		rel := filepath.Join(root, name)
		ig := ignored(name, e.IsDir())
		item := webDir{Name: name, Rel: filepath.ToSlash(rel), Dir: e.IsDir(), Ignored: ig, Selected: !ig}
		if e.IsDir() && !ig {
			if children, err := buildWebDirTree(rel, filepath.Join(base, name)); err == nil {
				item.Children = children
			}
		}
		out = append(out, item)
	}
	return out, nil
}
func webJobDir(rel string) (string, error) {
	parts := strings.Split(rel, "/")
	if len(parts) != 2 || parts[0] == "" || !strings.HasPrefix(parts[1], "job-") {
		return "", errors.New("invalid job")
	}
	for _, p := range parts {
		if p == "." || p == ".." || filepath.Base(p) != p || strings.Contains(p, "\\") {
			return "", errors.New("invalid job")
		}
	}
	base := filepath.Join(outputDirectory(), "jobs")
	dir := filepath.Join(base, parts[0], parts[1])
	for _, p := range []string{filepath.Join(base, parts[0]), dir} {
		fi, err := os.Lstat(p)
		if err != nil || !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("unknown job")
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "job.json")); err != nil {
		return "", errors.New("unknown job")
	}
	return dir, nil
}
func webHandler() http.Handler { cwd, _ := os.Getwd(); return webHandlerForRoot(cwd) }
func webHandlerForRoot(root string) http.Handler {
	root, _ = filepath.Abs(root)
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		panic(err)
	}
	token := hex.EncodeToString(tokenBytes)
	mux := http.NewServeMux()
	render := func(w http.ResponseWriter, page webPage) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
		page.Token = token
		cfg := loadConfig()
		page.Links = configuredLinks(cfg)
		page.AgenticAvailable = cfg.supportsAgentic()
		page.AgentProfile, _, _ = cfg.activeAgentProfile()
		if err := reviewHTML.ExecuteTemplate(w, "web.html", page); err != nil {
			fmt.Fprintln(os.Stderr, "web template:", err)
		}
	}
	denyMethod := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method != "POST" {
			http.Error(w, "POST required", 405)
			return true
		}
		return false
	}
	protected := func(w http.ResponseWriter, r *http.Request) bool {
		if denyMethod(w, r) {
			return false
		}
		r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form", 400)
			return false
		}
		if r.PostForm.Get("csrf") != token {
			http.Error(w, "invalid request token", 403)
			return false
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			u, e := url.Parse(origin)
			if e != nil || u.Host != r.Host || u.Scheme != "http" {
				http.Error(w, "cross-origin request refused", 403)
				return false
			}
		}
		if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			http.Error(w, "cross-site request refused", 403)
			return false
		}
		return true
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		if r.Method != "GET" {
			http.Error(w, "GET required", 405)
			return
		}
		dirs, err := buildWebDirTree("", root)
		page := webPage{Title: "Pack a parcel", Root: root, Sessions: listWebSessions(), Dirs: dirs, Notice: r.URL.Query().Get("notice")}
		if err != nil {
			page.Error = err.Error()
		}
		render(w, page)
	})
	mux.HandleFunc("/submit", func(w http.ResponseWriter, r *http.Request) {
		if !protected(w, r) {
			return
		}
		prompt := strings.TrimSpace(r.PostForm.Get("prompt"))
		selected := r.PostForm["files"]
		if prompt == "" || len(selected) == 0 || len(selected) > 20000 {
			http.Error(w, "select files and provide a prompt", 400)
			return
		}
		// Reuse the CLI's path validation and recursive ignore rules; never trust form paths.
		manifest, err := os.CreateTemp("", "pipkin-web-files-*.json")
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		defer os.Remove(manifest.Name())
		if err = json.NewEncoder(manifest).Encode(selected); err != nil {
			manifest.Close()
			http.Error(w, err.Error(), 500)
			return
		}
		manifest.Close()
		files, err := filesFromJSON(root, manifest.Name())
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		agentic := r.PostForm.Get("agentic") == "1"
		if agentic && !loadConfig().supportsAgentic() {
			http.Error(w, "agentic mode requires a text or hosted archive provider", 400)
			return
		}
		// Persistent jobs continue independently of browser connections.
		go func() {
			ctx := withWorkflowEvents(context.Background(), func(event workflowEvent) {
				if event.Type != "delta" {
					writeWorkflowEvent(os.Stderr, event)
				}
			})
			if err := workflowSendFiles(ctx, root, files, prompt, agentic); err != nil {
				fmt.Fprintln(os.Stderr, "Pipkin web submission:", err)
			}
		}()
		http.Redirect(w, r, "/?notice="+url.QueryEscape("Delivery started. Refresh to see its saved session and status."), http.StatusSeeOther)
	})
	mux.HandleFunc("/review", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			http.Error(w, "GET required", 405)
			return
		}
		rel := r.URL.Query().Get("job")
		dir, err := webJobDir(rel)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		data, err := os.ReadFile(filepath.Join(dir, "job.json"))
		if err != nil {
			http.Error(w, "unknown job", 404)
			return
		}
		var info WorkflowJob
		if json.Unmarshal(data, &info) != nil {
			http.Error(w, "invalid job", 500)
			return
		}
		mode := r.URL.Query().Get("mode")
		if mode != "current" {
			mode = "submitted"
		}
		page := webPage{Title: "Review", Job: rel, JobInfo: info, Prompt: savedPrompt(dir), Mode: mode, Root: info.Root, Notice: r.URL.Query().Get("notice")}
		if info.Status == "running" {
			page.Notice = "Delivery in progress. Refresh for updates."
			render(w, page)
			return
		}
		if info.Status != "completed" {
			page.Error = info.Error
			render(w, page)
			return
		}
		rv, err := workflowReview(dir)
		if err != nil {
			page.Error = err.Error()
			render(w, page)
			return
		}
		decisions, err := loadDecisions(dir)
		if err != nil {
			page.Error = err.Error()
			render(w, page)
			return
		}
		for _, f := range reviewableFiles(rv) {
			decision := decisions.Decisions[f.Path]
			switch decision {
			case decisionApproved:
				page.Approved++
			case decisionRejected:
				page.Rejected++
			default:
				page.Pending++
			}
			if decisions.Applied[f.Path] {
				page.Applied++
			}
			page.Files = append(page.Files, webFile{Path: f.Path, Status: f.Status, WorkspaceStatus: f.WorkspaceStatus, Decision: decision, Applied: decisions.Applied[f.Path], Diff: webDiff(f, mode)})
		}
		render(w, page)
	})
	mux.HandleFunc("/decision", func(w http.ResponseWriter, r *http.Request) {
		if !protected(w, r) {
			return
		}
		rel := r.PostForm.Get("job")
		dir, err := webJobDir(rel)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		rv, err := workflowReview(dir)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		d, err := loadDecisions(dir)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		action := r.PostForm.Get("action")
		path := r.PostForm.Get("path")
		value := ""
		switch action {
		case "approve", "approve-all":
			value = decisionApproved
		case "reject", "reject-all":
			value = decisionRejected
		case "reset":
			value = ""
		default:
			http.Error(w, "unknown decision", 400)
			return
		}
		valid := false
		for _, f := range reviewableFiles(rv) {
			if strings.HasSuffix(action, "-all") || f.Path == path {
				valid = true
				if d.Applied[f.Path] {
					continue
				}
				if value == "" {
					delete(d.Decisions, f.Path)
				} else {
					d.Decisions[f.Path] = value
				}
			}
		}
		if !valid {
			http.Error(w, "unknown file", 400)
			return
		}
		if err := saveDecisions(dir, d); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		http.Redirect(w, r, "/review?job="+url.QueryEscape(rel)+"&mode="+url.QueryEscape(r.PostForm.Get("mode")), http.StatusSeeOther)
	})
	mux.HandleFunc("/copy", func(w http.ResponseWriter, r *http.Request) {
		if !protected(w, r) {
			return
		}
		rel := r.PostForm.Get("job")
		dir, err := webJobDir(rel)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		rv, err := workflowReview(dir)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		files := reviewableFiles(rv)
		d, err := loadDecisions(dir)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		for _, f := range files {
			if !d.Applied[f.Path] && d.Decisions[f.Path] != decisionApproved && d.Decisions[f.Path] != decisionRejected {
				http.Error(w, "review every change before copying", 409)
				return
			}
		}
		if r.PostForm.Get("confirm") != "COPY" {
			http.Error(w, "explicit COPY confirmation required", 400)
			return
		}
		n, err := copyApprovedChanges(dir, files, &d)
		if err != nil {
			http.Error(w, "Copy blocked: "+err.Error(), 409)
			return
		}
		http.Redirect(w, r, "/review?job="+url.QueryEscape(rel)+"&notice="+url.QueryEscape(fmt.Sprintf("Copied %d approved files. Rejected files left untouched.", n)), http.StatusSeeOther)
	})
	return mux
}
func serveWeb(raw string) error {
	addr, err := parseListen(raw)
	if err != nil {
		return err
	}
	bind := addr
	if os.Getenv("PIPKIN_WEB_CONTAINER") == "1" {
		_, port, err := net.SplitHostPort(addr)
		if err != nil {
			return err
		}
		bind = net.JoinHostPort("0.0.0.0", port)
	}
	server := &http.Server{Addr: bind, Handler: webHandler(), ReadHeaderTimeout: 5 * time.Second}
	fmt.Fprintln(os.Stderr, "Pipkin web UI: http://"+addr)
	return server.ListenAndServe()
}
