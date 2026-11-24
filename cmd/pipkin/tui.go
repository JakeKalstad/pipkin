package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Restrict work in the renderer to lightweight scans of local metadata.
type recentArtifact struct {
	name      string
	status    string
	info      string
	timestamp time.Time
}

func terminalPad(s string, width int) string {
	if width <= 0 {
		return ""
	}
	n := utf8.RuneCountInString(s)
	if n >= width {
		return trunc(s, width)
	}
	return s + strings.Repeat(" ", width-n)
}
func recentArtifacts(root string) (archives, jobs []recentArtifact) {
	base := outputDirectory()
	if entries, err := os.ReadDir(base); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".zip") {
				continue
			}
			if info, err := e.Info(); err == nil {
				archives = append(archives, recentArtifact{name: e.Name(), info: humanBytes(info.Size()), timestamp: info.ModTime()})
			}
		}
	}
	jobDir := filepath.Join(base, "jobs", filepath.Base(root))
	if entries, err := os.ReadDir(jobDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			body, err := os.ReadFile(filepath.Join(jobDir, e.Name(), "job.json"))
			if err != nil {
				continue
			}
			var job WorkflowJob
			if json.Unmarshal(body, &job) != nil {
				continue
			}
			when, _ := time.Parse(time.RFC3339, job.Created)
			if when.IsZero() {
				if info, err := e.Info(); err == nil {
					when = info.ModTime()
				}
			}
			jobs = append(jobs, recentArtifact{name: job.ID, status: job.Status, info: job.Model, timestamp: when})
		}
	}
	sort.Slice(archives, func(i, j int) bool { return archives[i].timestamp.After(archives[j].timestamp) })
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].timestamp.After(jobs[j].timestamp) })
	if len(archives) > 6 {
		archives = archives[:6]
	}
	if len(jobs) > 6 {
		jobs = jobs[:6]
	}
	return
}
func humanBytes(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	if n < 1024*1024 {
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	}
	return fmt.Sprintf("%.1f MB", float64(n)/1048576)
}
func loadingSymbol(frame int) string {
	return []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}[frame%10]
}
func parcelAnimation(frame int) string {
	track := []rune("───────────────────")
	track[frame%len(track)] = '●'
	return " [" + string(track) + "]"
}
func (a *App) cachedRecent() (archives, jobs []recentArtifact) {
	if a.recentAt.IsZero() || time.Since(a.recentAt) > time.Second {
		a.cachedArchives, a.cachedJobs = recentArtifacts(a.cwd)
		a.recentAt = time.Now()
	}
	return a.cachedArchives, a.cachedJobs
}
func (a *App) sidePanel(width int) []string {
	spinner := loadingSymbol(a.spinnerFrame)
	lines := []string{
		`  PIPKIN / CODE COURIER`,
		``,
		"       _         _",
		"      ( `-.___.-' )",
		"       /  o   o  \\",
		"      |  = .Y. =  |",
		"       \\_.-\\_/-._/",
		"       /  .----.  \\",
		"      /  (| ZIP |) \\",
		"     (    '----'    ) _..-.",
		"      '._/    \\_.'--'_.-'",
		"         `----'  `---'",
		"",
		"  Parcel the otter",
		"",
		`  DELIVERY STATUS`,
	}
	if a.busy {
		lines = append(lines, "  "+spinner+" "+a.spinnerMessage, "  "+parcelAnimation(a.spinnerFrame))
	} else {
		lines = append(lines, "  "+a.status)
	}
	lines = append(lines, "  Provider: "+a.cfg.Provider, "  Model: "+a.cfg.Model, "", "  RECENT ARCHIVES")
	archives, jobs := a.cachedRecent()
	if len(archives) == 0 {
		lines = append(lines, "  No parcels packed yet")
	}
	for _, item := range archives {
		lines = append(lines, "  "+item.name, fmt.Sprintf("  %s  |  %s", item.timestamp.Format("Jan 02 15:04"), item.info))
	}
	lines = append(lines, "", "  RECENT ATTEMPTS (r to review)")
	if len(jobs) == 0 {
		lines = append(lines, "  No jobs yet")
	}
	for _, item := range jobs {
		lines = append(lines, "  "+item.name+"  ["+item.status+"]", "  "+item.info)
	}
	for i, s := range lines {
		lines[i] = trunc(s, width)
	}
	return lines
}
func (a *App) render() {
	h, w := size()
	rightWidth := 0
	leftWidth := w
	if w >= 104 {
		rightWidth = min(43, w/3)
		leftWidth = w - rightWidth - 3
	}
	links := configuredLinks(a.cfg)
	screenHeight := h - len(links)
	if a.traceStatus != "" {
		screenHeight--
	}
	rows := max(1, screenHeight-7)
	if a.cursor < a.offset {
		a.offset = a.cursor
	}
	if a.cursor >= a.offset+rows {
		a.offset = a.cursor - rows + 1
	}
	shortcuts := "  ↑↓ move  ←→ expand  Space select  z ZIP  p LLM"
	if a.cfg.supportsAgentic() {
		shortcuts += "  P Agentic"
	}
	shortcuts += "  r Review  i Import  q Quit"
	if w < utf8.RuneCountInString(shortcuts) {
		shortcuts = " Space select  z ZIP  p LLM"
		if a.cfg.supportsAgentic() {
			shortcuts = " p LLM  P Agentic  Space select  z ZIP"
		}
		shortcuts += "  r Review  i Import  q Quit"
	}
	// The shortcut bar spans the terminal so the sidebar cannot clip it.
	header := []string{
		" PIPKIN  " + trunc(a.cwd, max(0, leftWidth-10)),
		shortcuts,
		strings.Repeat("─", max(0, leftWidth)),
	}
	var lines []string
	lines = append(lines, header...)
	for i := a.offset; i < len(a.visible) && i < a.offset+rows; i++ {
		n := a.visible[i]
		check := "[ ]"
		if n.selected {
			check = "[x]"
		}
		arrow := "  "
		if n.dir {
			if n.expanded {
				arrow = "▾ "
			} else {
				arrow = "▸ "
			}
		}
		cursor := "  "
		if i == a.cursor {
			cursor = "> "
		}
		s := cursor + strings.Repeat("  ", min(depth(n), 25)) + arrow + check + " " + n.name
		if n.ignored {
			s += " (ignored)"
		}
		lines = append(lines, trunc(s, max(0, leftWidth)))
	}
	for len(lines) < rows+3 {
		lines = append(lines, "")
	}
	selected, _ := a.selectedFiles()
	status := a.status
	if a.busy {
		status = loadingSymbol(a.spinnerFrame) + " " + a.spinnerMessage
	}
	lines = append(lines, strings.Repeat("─", max(0, leftWidth)), fmt.Sprintf(" %d files selected  |  %s", len(selected), status))
	if a.editing {
		label := " Prompt (Enter send, Esc cancel):"
		if a.agentic {
			label = " Agentic prompt (Enter send, Esc cancel):"
			if name, _, err := a.cfg.activeAgentProfile(); err == nil && name != "builtin" {
				label += " " + name
			}
		}
		lines = append(lines, label, " > "+a.prompt)
	} else {
		lines = append(lines, " ZIP: "+a.outputPath(""))
	}
	var side []string
	if rightWidth > 0 {
		side = a.sidePanel(rightWidth)
	}
	out := strings.Builder{}
	out.WriteString("\x1b[2J\x1b[H")
	count := len(lines)
	if rightWidth > 0 && len(side) > count {
		count = len(side)
	}
	// Clip output to terminal height; never print beyond the viewport.
	if count > screenHeight {
		count = screenHeight
	}
	for i := 0; i < count; i++ {
		l := ""
		if i < len(lines) {
			l = lines[i]
		}
		if rightWidth == 0 || i == 1 {
			out.WriteString(trunc(l, w))
		} else {
			r := ""
			if i < len(side) {
				r = side[i]
			}
			out.WriteString(terminalPad(trunc(l, leftWidth), leftWidth) + " │ " + trunc(r, rightWidth))
		}
		if i < count-1 {
			out.WriteString("\r\n")
		}
	}
	if a.traceStatus != "" {
		out.WriteString("\r\n" + trunc(" "+a.traceStatus, w))
	}
	for _, link := range links {
		label := trunc(" "+link.Label+": "+link.URL, w)
		fmt.Fprintf(&out, "\r\n\x1b]8;;%s\x1b\\%s\x1b]8;;\x1b\\", link.URL, label)
	}
	os.Stdout.WriteString(out.String())
}
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (a *App) submitWithSpinner(prompt string) error {
	a.busy = true
	a.spinnerFrame = 0
	a.spinnerMessage = "Packaging request..."
	events := make(chan workflowEvent, 32)
	done := make(chan error, 1)
	var completion, trace string
	a.traceStatus = ""
	ctx := withWorkflowEvents(context.Background(), func(e workflowEvent) {
		switch e.Type {
		case "complete":
			completion = e.Message
		case "trace":
			trace = e.Message
		}
		select {
		case events <- e:
		default:
		}
	})
	defer func() { a.busy = false; a.spinnerMessage = "" }()
	go func() { done <- a.submit(ctx, prompt) }()
	ticker := time.NewTicker(120 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-done:
			a.status = completion
			a.traceStatus = trace
			return err
		case e := <-events:
			if e.Type == "status" || e.Type == "job" || e.Type == "complete" {
				a.spinnerMessage = e.Message
			}
			if e.Type == "usage" {
				a.spinnerMessage = fmt.Sprintf("Usage: %d in / %d out tokens", e.InputTokens, e.OutputTokens)
			}
			if e.Type == "error" {
				a.spinnerMessage = e.Message
			}
		case <-ticker.C:
			a.spinnerFrame++
			a.render()
		}
	}
}
