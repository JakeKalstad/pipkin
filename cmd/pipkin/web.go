package main

import (
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
)

type webSession struct{ ID, Project, Status, Model, Created string }
type webLine struct {
	Number int
	Text   string
}
type webFile struct {
	Path, Status, WorkspaceStatus string
	Before, After                 []webLine
}
type webPage struct {
	Title, Mode, Job, Error string
	Sessions                []webSession
	Files                   []webFile
	JobInfo                 WorkflowJob
	Root                    string
	Dirs                    []webDir
}

type webDir struct {
	Name     string
	Rel      string
	Dir      bool
	Ignored  bool
	Children []webDir
}

var reviewHTML = template.Must(template.New("review").Parse(`<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>{{.Title}} · Pipkin</title><style>
:root{color-scheme:dark;font:15px system-ui, sans-serif;background:#111b22;color:#eaf1f2}body{margin:0}header{padding:1.4rem 2rem;border-bottom:1px solid #34414b;display:flex;align-items:center;gap:1rem}header a{font-size:1.5rem;font-weight:800;color:#e9faf3;text-decoration:none}.sub{color:#91a5ad}.container{max-width:1250px;margin:2rem auto;padding:0 1rem}a{color:#89ddb7}h1{font-size:1.7rem;margin-bottom:.4rem}h2{font-size:1.1rem}.card{display:block;background:#1a2831;border:1px solid #354751;border-radius:12px;padding:1rem 1.3rem;margin:1rem 0;text-decoration:none;color:inherit}.card:hover{border-color:#87d9b6}.pill{font-size:.75rem;border:1px solid #51626a;border-radius:12px;padding:.15rem .55rem}.meta{display:flex;flex-wrap:wrap;gap:.8rem;color:#b0c0c5}nav{margin:1rem 0}.diff{display:grid;grid-template-columns:1fr 1fr;gap:1px;background:#354751;border:1px solid #354751;border-radius:9px;overflow:hidden}.pane{min-width:0;background:#16242c}.pane h3{font-size:.85rem;background:#24343d;margin:0;padding:.7rem}.pane pre{margin:0;padding:.8rem;white-space:pre-wrap;overflow-wrap:anywhere;font:12px/1.6 ui-monospace,monospace;max-height:550px;overflow:auto}.file{margin:2rem 0}.warn{color:#ffc68a}.error{color:#ffaaaa}.empty{padding:3rem 1rem;text-align:center;color:#adc0c6}@media(max-width:700px){.diff{grid-template-columns:1fr}}
</style></head><body><header><a href="/">🦦 Pipkin</a><span class="sub">Pick. Pack. Prompt. Inspect.</span></header><main class="container">
{{if .Error}}<p class="error">{{.Error}}</p>{{end}}
{{if not .Job}}<h1>Saved deliveries</h1><p class="sub">Your parcel-carrying code otter keeps every returned ZIP safely outside the repository.</p>{{range .Sessions}}<a class="card" href="/review?job={{.Project}}/{{.ID}}"><strong>{{.Project}} / {{.ID}}</strong> <span class="pill">{{.Status}}</span><p class="meta">{{.Model}} · {{.Created}}</p></a>{{else}}<div class="empty">No sessions yet. Send Pipkin a ZIP and he'll bring your changes back.</div>{{end}}
{{else}}<p><a href="/">← All deliveries</a></p><h1>{{.JobInfo.ID}}</h1><p class="meta">{{.JobInfo.Provider}} · {{.JobInfo.Model}} · {{.JobInfo.Status}} · {{.JobInfo.InputTokens}} input tokens · {{.JobInfo.OutputTokens}} output tokens</p><nav><a href="/review?job={{.Job}}&amp;mode=submitted">Submitted vs result</a> &nbsp; | &nbsp; <a href="/review?job={{.Job}}&amp;mode=current">Current workspace vs result</a></nav>

<h2>Project files and directories</h2>
<ul>
{{- range .Dirs}}
  <li>{{template "dir" .}}</li>
{{- end}}
</ul>

{{define "dir"}}
  {{- if .Dir}}
    <details open>
      <summary>{{.Name}}{{if .Ignored}} (ignored){{end}}</summary>
      {{- if .Children}}
        <ul>
        {{- range .Children}}
          <li>{{template "dir" .}}</li>
        {{- end}}
        </ul>
      {{- end}}
    </details>
  {{- else}}
    {{.Name}}{{if .Ignored}} (ignored){{end}}
  {{- end}}
{{end}}

{{range .Files}}<section class="file"><h2>{{.Path}} <span class="pill">{{.Status}}</span></h2><p class="sub">Workspace: {{.WorkspaceStatus}}</p><div class="diff"><div class="pane"><h3>{{if eq $.Mode "current"}}Current workspace{{else}}Submitted ZIP{{end}}</h3><pre>{{range .Before}}{{.Number}}  {{.Text}}
{{end}}</pre></div><div class="pane"><h3>Returned ZIP</h3><pre>{{range .After}}{{.Number}}  {{.Text}}
{{end}}</pre></div></div></section>{{else}}<div class="empty">No differences for this comparison.</div>{{end}}<p class="warn">Read-only review. Approve files through VSCodium or CLI; no changes are applied here.</p>{{end}}</main></body></html>`))

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
	if u.Scheme != "http" || u.Host == "" || u.User != nil || u.Path != "" && u.Path != "/" || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("LISTEN_URL must be an http://loopback-host:port URL")
	}
	host, port, err := net.SplitHostPort(u.Host)
	if err != nil {
		return "", err
	}
	ip := net.ParseIP(host)
	if !(strings.EqualFold(host, "localhost") || ip != nil && ip.IsLoopback()) {
		return "", errors.New("web UI has no authentication; bind only to 127.0.0.1, ::1, or localhost")
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
			p := filepath.Join(base, proj.Name(), job.Name(), "job.json")
			b, err := os.ReadFile(p)
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

func webLines(b []byte) []webLine {
	if len(b) == 0 {
		return nil
	}
	if len(b) > 256*1024 {
		return []webLine{{Text: "[File too large for browser preview; use VSCodium diff]"}}
	}
	if strings.ContainsRune(string(b), '\x00') {
		return []webLine{{Text: "[Binary file]"}}
	}
	lines := strings.Split(string(b), "\n")
	out := make([]webLine, 0, len(lines))
	for i, s := range lines {
		out = append(out, webLine{Number: i + 1, Text: s})
	}
	return out
}

func buildWebDirTree(root string, base string) ([]webDir, error) {
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
	var dirs []webDir
	for _, e := range entries {
		name := e.Name()
		rel := filepath.Join(root, name)
		ig := ignored(name, e.IsDir())
		wd := webDir{Name: name, Rel: rel, Dir: e.IsDir(), Ignored: ig}
		if e.IsDir() && !ig {
			children, err := buildWebDirTree(rel, filepath.Join(base, name))
			if err == nil {
				wd.Children = children
			}
		}
		dirs = append(dirs, wd)
	}
	return dirs, nil
}

func webHandler() http.Handler {
	mux := http.NewServeMux()
	render := func(w http.ResponseWriter, p webPage) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'")
		_ = reviewHTML.Execute(w, p)
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		render(w, webPage{Title: "Deliveries", Sessions: listWebSessions()})
	})
	mux.HandleFunc("/review", func(w http.ResponseWriter, r *http.Request) {
		rel := r.URL.Query().Get("job")
		parts := strings.Split(rel, "/")
		if len(parts) != 2 || parts[0] == "" || !strings.HasPrefix(parts[1], "job-") || (filepath.Base(parts[0]) != parts[0] || parts[0] == "..") || (filepath.Base(parts[1]) != parts[1] || parts[1] == "..") {
			http.Error(w, "invalid job", 400)
			return
		}
		dir := filepath.Join(outputDirectory(), "jobs", parts[0], parts[1])
		if _, err := mcpJob(dir); err != nil {
			http.Error(w, "unknown job", 404)
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
		rv, err := workflowReview(dir)
		if err != nil {
			render(w, webPage{Title: "Review", Job: rel, JobInfo: info, Mode: mode, Error: err.Error()})
			return
		}
		page := webPage{Title: "Review", Job: rel, JobInfo: info, Mode: mode, Root: info.Root}

		dirs, err := buildWebDirTree("", info.Root)
		if err == nil {
			page.Dirs = dirs
		}

		for _, f := range rv.Files {
			left := f.Before
			if mode == "current" {
				left = f.Current
			}
			var before, after []byte
			if left != "" {
				before, _ = os.ReadFile(left)
			}
			if f.After != "" {
				after, _ = os.ReadFile(f.After)
			}
			if mode == "current" && string(before) == string(after) {
				continue
			}
			page.Files = append(page.Files, webFile{Path: f.Path, Status: f.Status, WorkspaceStatus: f.WorkspaceStatus, Before: webLines(before), After: webLines(after)})
		}
		render(w, page)
	})
	return mux
}
func serveWeb(raw string) error {
	addr, err := parseListen(raw)
	if err != nil {
		return err
	}
	server := &http.Server{Addr: addr, Handler: webHandler(), ReadHeaderTimeout: 5 * time.Second}
	fmt.Fprintln(os.Stderr, "Pipkin web review: http://"+addr)
	return server.ListenAndServe()
}
