package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Restrict work in the renderer to lightweight scans of local metadata.
type recentArtifact struct {
	name      string
	path      string
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
				archives = append(archives, recentArtifact{name: e.Name(), path: filepath.Join(base, e.Name()), info: humanBytes(info.Size()), timestamp: info.ModTime()})
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
			jobs = append(jobs, recentArtifact{name: job.ID, path: filepath.Join(jobDir, e.Name()), status: job.Status, info: job.Model, timestamp: when})
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
func (a *App) sidePanel(width int) ([]string, map[int]string) {
	links := make(map[int]string)
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
		links[len(lines)] = outputFolderURL(item.path)
		lines = append(lines, "  "+safeDisplayLine(item.name), fmt.Sprintf("  %s  |  %s", item.timestamp.Format("Jan 02 15:04"), item.info))
	}
	lines = append(lines, "", "  RECENT ATTEMPTS (r to review)")
	if len(jobs) == 0 {
		lines = append(lines, "  No jobs yet")
	}
	for _, item := range jobs {
		links[len(lines)] = outputFolderURL(item.path)
		lines = append(lines, "  "+safeDisplayLine(item.name)+"  ["+item.status+"]", "  "+item.info)
	}
	for i, s := range lines {
		lines[i] = trunc(s, width)
	}
	return lines, links
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
	screenHeight-- // Output folder link
	rows := max(1, screenHeight-7)
	if a.cursor < a.offset {
		a.offset = a.cursor
	}
	if a.cursor >= a.offset+rows {
		a.offset = a.cursor - rows + 1
	}
	shortcuts := "  ↑↓ move  ←→ expand  Space toggle  s Select one  z ZIP  p LLM"
	if a.cfg.supportsAgentic() {
		shortcuts += "  P Agentic"
	}
	if len(a.cfg.ProviderProfiles) > 0 {
		shortcuts += "  m Model"
	}
	shortcuts += "  g/G Git  H Hidden  ? Help  J Project  I Instructions  f Free prompt  D MkDocs  r Review  i Import  q Quit"
	if w < utf8.RuneCountInString(shortcuts) {
		shortcuts = " ? Help  D MkDocs  J Project  I Instructions  f Free prompt  s Select one  Space toggle  g/G Git  H Hidden  z ZIP  p LLM"
		if a.cfg.supportsAgentic() {
			shortcuts = " ? Help  D MkDocs  J Project  I Instructions  f Free prompt  s Select one  p LLM  P Agentic  Space toggle  g/G Git  H Hidden  z ZIP"
		}
		if len(a.cfg.ProviderProfiles) > 0 {
			shortcuts = " m Model " + shortcuts
		}
		shortcuts += "  r Review  q Quit"
	}
	// The shortcut bar spans the terminal so the sidebar cannot clip it.
	model := a.cfg.Provider + " / " + a.cfg.Model
	if a.cfg.ProviderProfile != "" {
		model = a.cfg.ProviderProfile + " · " + model
	}
	header := []string{
		" PIPKIN  " + model + "  ·  " + a.cwd,
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
	outputLinks := make(map[int]string, 2)
	if a.lastOutput != "" && !a.busy && (strings.Contains(a.status, a.lastOutput) || strings.HasPrefix(a.status, "Delivery saved")) {
		outputLinks[len(lines)-1] = outputFolderURL(a.lastOutput)
	}
	if a.editing {
		label := " Prompt (Enter send, Esc cancel):"
		if a.mkdocsExport {
			label = " MkDocs brief (Enter export, Esc cancel):"
		} else if a.agentic {
			label = " Agentic prompt (Enter send, Esc cancel):"
			if name, _, err := a.cfg.activeAgentProfile(); err == nil && name != "builtin" {
				label += " " + name
			}
		}
		lines = append(lines, label, " > "+a.prompt)
	} else {
		outputLinks[len(lines)] = outputFolderURL(a.outputPath(""))
		lines = append(lines, " ZIP: "+safeDisplayLine(a.outputPath("")))
	}
	var side []string
	var sideLinks map[int]string
	if rightWidth > 0 {
		side, sideLinks = a.sidePanel(rightWidth)
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
			out.WriteString(terminalLink(trunc(l, w), outputLinks[i]))
		} else {
			r := ""
			if i < len(side) {
				r = side[i]
			}
			out.WriteString(terminalLink(terminalPad(trunc(l, leftWidth), leftWidth), outputLinks[i]) + " │ " + terminalLink(trunc(r, rightWidth), sideLinks[i]))
		}
		if i < count-1 {
			out.WriteString("\r\n")
		}
	}
	if a.traceStatus != "" {
		out.WriteString("\r\n" + trunc(" "+a.traceStatus, w))
	}
	target := a.lastOutput
	if target == "" {
		target = filepath.Join(outputDirectory(), "output.zip")
	}
	label := trunc(" O Open output folder: "+safeDisplayLine(outputFolder(target)), w)
	out.WriteString("\r\n" + terminalLink(label, outputFolderURL(target)))
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

func (a *App) chooseProvider(reader *bufio.Reader) {
	names := make([]string, 0, len(a.cfg.ProviderProfiles))
	for name := range a.cfg.ProviderProfiles {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		a.status = "Add provider profiles to config.json to switch models"
		return
	}
	index, matched := slices.BinarySearch(names, a.cfg.ProviderProfile)
	if !matched {
		index = 0
	}
	for {
		height, _ := size()
		lines := []string{" PIPKIN / MODEL", " ↑↓ choose  Enter select  Esc back"}
		start := max(0, index-max(1, height-3)+1)
		for i := start; i < len(names) && len(lines) < height; i++ {
			prefix := "  "
			if i == index {
				prefix = "> "
			}
			profile := a.cfg.ProviderProfiles[names[i]]
			lines = append(lines, safeDisplayLine(prefix+names[i]+"  ·  "+profile.Provider+" / "+profile.Model))
		}
		screenLines(lines)
		switch readKey(reader) {
		case "q", "b", "esc":
			return
		case "up", "k":
			index = max(0, index-1)
		case "down", "j":
			index = min(len(names)-1, index+1)
		case "enter":
			cfg := loadProviderConfig(names[index])
			if cfg.configError != nil {
				a.status = cfg.configError.Error()
				return
			}
			cfg.instructionRequest = a.cfg.instructionRequest
			a.cfg = cfg
			a.status = "Selected " + cfg.ProviderProfile + " for this session"
			return
		}
	}
}

func (a *App) submitWithSpinner(prompt string) error {
	a.busy = true
	a.spinnerFrame = 0
	a.spinnerMessage = "Packaging request..."
	events := make(chan workflowEvent, 32)
	done := make(chan error, 1)
	var completion, trace, output string
	a.traceStatus = ""
	ctx := withWorkflowEvents(context.Background(), func(e workflowEvent) {
		switch e.Type {
		case "complete":
			completion, output = e.Message, e.Path
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
			if output != "" {
				a.lastOutput = output
				a.recentAt = time.Time{}
			}
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

func (a *App) toggleHidden() error {
	show := !a.showHidden
	if err := a.rescanTree(show); err != nil {
		return err
	}
	a.status = "Hidden files excluded"
	if show {
		a.status = "Hidden files shown. Select the hidden files or folders to include."
	}
	return nil
}

func (a *App) rescanTree(show bool) error {
	root := &Node{name: a.root.name, dir: true, expanded: true, includeHidden: show}
	if err := scan(root, a.cwd); err != nil {
		return err
	}
	var restore func(*Node, *Node)
	restore = func(old, next *Node) {
		next.expanded, next.selected = old.expanded, old.selected
		previous := make(map[string]*Node, len(old.children))
		for _, child := range old.children {
			previous[child.name] = child
		}
		for _, child := range next.children {
			if prior := previous[child.name]; prior != nil {
				restore(prior, child)
			}
		}
	}
	restore(a.root, root)
	a.root, a.showHidden = root, show
	a.refresh()
	return nil
}
