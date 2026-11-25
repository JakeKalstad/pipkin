package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	decisionPending  = "pending"
	decisionApproved = "approved"
	decisionRejected = "rejected"
)

type reviewDecisions struct {
	Decisions map[string]string `json:"decisions"`
	Applied   map[string]bool   `json:"applied,omitempty"`
}

func loadDecisions(dir string) (reviewDecisions, error) {
	d := reviewDecisions{Decisions: map[string]string{}, Applied: map[string]bool{}}
	b, err := os.ReadFile(filepath.Join(dir, "decisions.json"))
	if errors.Is(err, os.ErrNotExist) {
		return d, nil
	}
	if err != nil {
		return d, err
	}
	if err = json.Unmarshal(b, &d); err != nil {
		return d, err
	}
	if d.Decisions == nil {
		d.Decisions = map[string]string{}
	}
	if d.Applied == nil {
		d.Applied = map[string]bool{}
	}
	return d, nil
}
func saveDecisions(dir string, d reviewDecisions) error {
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".pipkin-decisions-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err = tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if _, err = tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, "decisions.json"))
}
func reviewableFiles(review ReviewResult) []ReviewFile {
	out := make([]ReviewFile, 0, len(review.Files))
	for _, f := range review.Files {
		if f.Status != "workspace_only" {
			out = append(out, f)
		}
	}
	return out
}
func diffLines(f ReviewFile, mode string) []string {
	before := f.Before
	if mode == "current" {
		before = f.Current
	}
	after := f.After
	// No external file creation is necessary: the absent side is /dev/null.
	if before == "" {
		before = os.DevNull
	}
	if after == "" {
		after = os.DevNull
	}
	cmd := exec.Command("git", "diff", "--no-index", "--no-ext-diff", "--no-color", "--", before, after)
	b, err := cmd.CombinedOutput()
	if err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) || ee.ExitCode() != 1 {
			return []string{"Unable to render diff: " + err.Error()}
		}
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) > 0 && strings.HasPrefix(lines[0], "diff --git") {
		lines = lines[1:]
	}
	if len(lines) == 1 && lines[0] == "" {
		return []string{"No textual differences."}
	}
	if len(lines) > 25000 {
		lines = append(lines[:25000], "...diff truncated at 25,000 lines")
	}
	return lines
}
func safeDisplayLine(line string) string {
	return strings.Map(func(r rune) rune {
		if r == '\t' {
			return ' '
		}
		if r < 32 || r == 127 || r == 0x9b {
			return '�'
		}
		return r
	}, line)
}
func coloredDiff(line string) string {
	line = safeDisplayLine(line)
	switch {
	case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "new file"):
		return "\x1b[32m" + line + "\x1b[0m"
	case strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---"), strings.HasPrefix(line, "deleted file"):
		return "\x1b[31m" + line + "\x1b[0m"
	case strings.HasPrefix(line, "@@"):
		return "\x1b[36m" + line + "\x1b[0m"
	default:
		return line
	}
}
func reviewSessionPaths(root string) ([]string, error) {
	base := filepath.Join(outputDirectory(), "jobs", filepath.Base(root))
	entries, err := os.ReadDir(base)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	type candidate struct {
		path string
		when time.Time
	}
	var result []candidate
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(base, e.Name())
		b, err := os.ReadFile(filepath.Join(path, "job.json"))
		if err != nil {
			continue
		}
		var job WorkflowJob
		if json.Unmarshal(b, &job) != nil || job.Root != root {
			continue
		}
		when, _ := time.Parse(time.RFC3339, job.Created)
		result = append(result, candidate{path, when})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].when.After(result[j].when) })
	out := make([]string, 0, len(result))
	for _, r := range result {
		out = append(out, r.path)
	}
	return out, nil
}
func screenLines(lines []string) {
	h, w := size()
	var b strings.Builder
	b.WriteString("\x1b[2J\x1b[H")
	for i, line := range lines {
		if i >= h {
			break
		}
		b.WriteString(trunc(line, w))
		if i < h-1 {
			b.WriteString("\r\n")
		}
	}
	fmt.Fprint(os.Stdout, b.String())
}
func (a *App) reviewTUI(r *bufio.Reader) error {
	sessions, err := reviewSessionPaths(a.cwd)
	if err != nil {
		return err
	}
	if len(sessions) == 0 {
		return errors.New("no deliveries yet; press p to submit code")
	}
	index := 0
	jobs := make([]WorkflowJob, len(sessions))
	completed := 0
	for i, session := range sessions {
		data, err := os.ReadFile(filepath.Join(session, "job.json"))
		if err == nil && json.Unmarshal(data, &jobs[i]) == nil && jobs[i].Status == "completed" {
			completed++
		}
	}
	for {
		h, _ := size()
		lines := []string{" PIPKIN / PARCEL HISTORY", " ↑↓ choose  Enter inspect  q return", strings.Repeat("─", 65)}
		start := max(0, index-max(1, h-8)+1)
		for i := start; i < len(jobs) && len(lines) < h-3; i++ {
			job := jobs[i]
			marker := "  "
			if i == index {
				marker = "> "
			}
			lines = append(lines, safeDisplayLine(fmt.Sprintf("%s%s  [%s]  %s  %s", marker, job.ID, job.Status, job.Created, job.Model)))
		}
		lines = append(lines, "", fmt.Sprintf(" %d completed deliveries · %d total", completed, len(sessions)))
		screenLines(lines)
		switch readKey(r) {
		case "q", "esc", "b":
			return nil
		case "up", "k":
			if index > 0 {
				index--
			}
		case "down", "j":
			if index < len(sessions)-1 {
				index++
			}
		case "enter":
			dir := sessions[index]
			data, err := os.ReadFile(filepath.Join(dir, "job.json"))
			if err != nil {
				return err
			}
			var job WorkflowJob
			if err := json.Unmarshal(data, &job); err != nil {
				return err
			}
			if job.Status != "completed" {
				lines := []string{" PIPKIN / " + strings.ToUpper(job.Status), " Delivery: " + job.ID, " Model: " + job.Model}
				if job.Error != "" {
					_, width := size()
					words := strings.Fields(safeDisplayLine(job.Error))
					line := " "
					for _, word := range words {
						if utf8.RuneCountInString(line+" "+word) > width {
							lines = append(lines, line)
							line = " "
						}
						line += word + " "
					}
					lines = append(lines, line)
				}
				if prompt := savedPrompt(dir); prompt != "" {
					lines = append(lines, " Prompt: "+safeDisplayLine(strings.Join(strings.Fields(prompt), " ")))
				}
				screenLines(append(lines, "", " Press any key to return"))
				readKey(r)
				continue
			}
			if err := runReviewScreen(r, dir); err != nil {
				return err
			}
		}
	}
}

// Older imported deliveries may have no saved prompt.
func savedPrompt(dir string) string {
	f, err := os.Open(filepath.Join(dir, "prompt.txt"))
	if err != nil {
		return ""
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil {
		return ""
	}
	if len(data) > 4096 {
		return strings.TrimSpace(string(data[:4096])) + "…"
	}
	return strings.TrimSpace(string(data))
}

func runReviewScreen(r *bufio.Reader, dir string) error {
	review, err := workflowReview(dir)
	if err != nil {
		return err
	}
	prompt := safeDisplayLine(strings.Join(strings.Fields(savedPrompt(dir)), " "))
	promptLabel := ""
	if prompt != "" {
		promptLabel = " Prompt: " + prompt
	}
	files := reviewableFiles(review)
	decisions, err := loadDecisions(dir)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		screenLines([]string{"PIPKIN / REVIEW", promptLabel, "No changes in the returned ZIP.", "Press any key to return."})
		readKey(r)
		return nil
	}
	idx, scroll := 0, 0
	mode := "submitted"
	focusDiff := false
	confirm := false
	message := "Review each change before copying."
	for {
		h, w := size()
		if idx >= len(files) {
			idx = len(files) - 1
		}
		f := files[idx]
		lines := diffLines(f, mode)
		leftW := max(26, min(48, w/3))
		split := w >= 88
		if !split {
			leftW = w
		}
		rightW := max(0, w-leftW-3)
		height := max(2, h-7)
		if promptLabel != "" {
			height--
		}
		if scroll > max(0, len(lines)-height) {
			scroll = max(0, len(lines)-height)
		}
		header := []string{
			" PIPKIN / PARCEL INSPECTION  ·  " + filepath.Base(dir),
			" ↑↓ files/diff  Tab switch  o OK  x reject  u reset  A all OK  X all reject",
			" Enter diff  v submitted/current  c COPY  b back  q exit",
		}
		if promptLabel != "" {
			header = append(header, trunc(promptLabel, w))
		}
		header = append(header, strings.Repeat("─", w))
		var buffer []string
		buffer = append(buffer, header...)
		approved, pending, rejected := 0, 0, 0
		for _, item := range files {
			switch decisions.Decisions[item.Path] {
			case decisionApproved:
				approved++
			case decisionRejected:
				rejected++
			default:
				pending++
			}
		}
		fileOffset := max(0, idx-height+1)
		for row := 0; row < height; row++ {
			fileLine := ""
			fileIdx := row + fileOffset
			if fileIdx < len(files) {
				item := files[fileIdx]
				mark := "?"
				switch decisions.Decisions[item.Path] {
				case decisionApproved:
					mark = "+"
				case decisionRejected:
					mark = "-"
				}
				if decisions.Applied[item.Path] {
					mark = "✓"
				}
				selectMark := " "
				if fileIdx == idx {
					selectMark = ">"
				}
				fileLine = fmt.Sprintf("%s[%s] %-8s %s", selectMark, mark, item.Status, item.Path)
				if item.WorkspaceStatus == "changed" || item.WorkspaceStatus == "existing_file_conflict" || item.WorkspaceStatus == "unavailable" {
					fileLine += " !"
				}
			}
			if split {
				diffLine := ""
				if row+scroll < len(lines) {
					diffLine = lines[row+scroll]
				}
				// ANSI color is applied only after printable text has been clipped.
				buffer = append(buffer, terminalPad(trunc(fileLine, leftW), leftW)+" │ "+coloredDiff(trunc(safeDisplayLine(diffLine), rightW)))
			} else {
				buffer = append(buffer, trunc(fileLine, leftW))
			}
		}
		if !split {
			buffer = append(buffer, "", fmt.Sprintf(" DIFF %s [%d/%d] — press Enter to view", f.Path, idx+1, len(files)))
		}
		buffer = append(buffer, strings.Repeat("─", w), fmt.Sprintf(" %d OK  ·  %d rejected  ·  %d pending  |  Diff: %s  |  Focus: %s", approved, rejected, pending, mode, map[bool]string{true: "diff", false: "files"}[focusDiff]))
		if confirm {
			message = fmt.Sprintf("COPY %d approved changes to working tree? Press Y to confirm or N to cancel.", approved)
		}
		buffer = append(buffer, " "+message)
		screenLinesANSI(buffer)
		key := readKey(r)
		if confirm {
			confirm = false
			if key == "Y" || key == "y" {
				n, e := copyApprovedChanges(dir, files, &decisions)
				if e != nil {
					message = "COPY FAILED: " + e.Error()
				} else {
					message = fmt.Sprintf("Copied %d files. Run git diff to review your working tree.", n)
				}
			} else {
				message = "Copy cancelled."
			}
			continue
		}
		switch key {
		case "q", "b", "esc":
			return nil
		case "tab":
			focusDiff = !focusDiff
		case "up", "k":
			if focusDiff && split {
				if scroll > 0 {
					scroll--
				}
			} else if idx > 0 {
				idx--
				scroll = 0
			}
		case "down", "j":
			if focusDiff && split {
				if scroll < len(lines)-height {
					scroll++
				}
			} else if idx < len(files)-1 {
				idx++
				scroll = 0
			}
		case "left", "h":
			if idx > 0 {
				idx--
				scroll = 0
			}
		case "right", "l":
			if idx < len(files)-1 {
				idx++
				scroll = 0
			}
		case "enter":
			if !split {
				showFullDiff(r, f, mode)
			} else {
				focusDiff = !focusDiff
			}
		case "v":
			if mode == "submitted" {
				mode = "current"
			} else {
				mode = "submitted"
			}
			scroll = 0
		case "o":
			decisions.Decisions[f.Path] = decisionApproved
			err = saveDecisions(dir, decisions)
		case "x":
			decisions.Decisions[f.Path] = decisionRejected
			err = saveDecisions(dir, decisions)
		case "u":
			delete(decisions.Decisions, f.Path)
			err = saveDecisions(dir, decisions)
		case "A":
			for _, item := range files {
				decisions.Decisions[item.Path] = decisionApproved
			}
			err = saveDecisions(dir, decisions)
		case "X":
			for _, item := range files {
				decisions.Decisions[item.Path] = decisionRejected
			}
			err = saveDecisions(dir, decisions)
		case "c":
			if pending > 0 {
				message = fmt.Sprintf("Review the remaining %d pending files (OK or reject) before copying.", pending)
			} else if approved == 0 {
				message = "Nothing approved to copy."
			} else {
				confirm = true
			}
		}
		if err != nil {
			message = "Could not save decisions: " + err.Error()
			err = nil
		}
	}
}
func screenLinesANSI(lines []string) {
	h, _ := size()
	var b strings.Builder
	b.WriteString("\x1b[2J\x1b[H")
	for i, line := range lines {
		if i >= h {
			break
		}
		b.WriteString(line)
		if i < h-1 {
			b.WriteString("\r\n")
		}
	}
	fmt.Fprint(os.Stdout, b.String())
}
func showFullDiff(r *bufio.Reader, f ReviewFile, mode string) {
	showDiffPager(r, f.Path, diffLines(f, mode), func() []string {
		if mode == "submitted" {
			mode = "current"
		} else {
			mode = "submitted"
		}
		return diffLines(f, mode)
	})
}

func showDiffPager(r *bufio.Reader, title string, lines []string, toggle func() []string) {
	showPager(r, " PIPKIN / DIFF: "+title, lines, toggle, coloredDiff)
}

func showPager(r *bufio.Reader, title string, lines []string, toggle func() []string, format func(string) string, edit ...func()) {
	scroll := 0
	hint := " ↑↓ scroll  q back"
	if toggle != nil {
		hint = " ↑↓ scroll  v toggle mode  q back"
	}
	if len(edit) != 0 {
		hint = " ↑↓ scroll  e edit  q back"
	}
	for {
		h, w := size()
		height := max(1, h-4)
		out := []string{trunc(safeDisplayLine(title), w), hint, strings.Repeat("─", w)}
		for i := scroll; i < min(len(lines), scroll+height); i++ {
			out = append(out, format(trunc(safeDisplayLine(lines[i]), w)))
		}
		screenLinesANSI(out)
		switch readKey(r) {
		case "q", "b", "esc", "enter":
			return
		case "up", "k":
			scroll = max(0, scroll-1)
		case "down", "j":
			scroll = min(max(0, len(lines)-height), scroll+1)
		case "e":
			if len(edit) != 0 {
				edit[0]()
				return
			}
		case "v":
			if toggle != nil {
				lines, scroll = toggle(), 0
			}
		}
	}
}

// Validate every destination before writing anything. Backups stay in the job
// directory, and a failed application attempts to restore earlier writes.
func copyApprovedChanges(dir string, files []ReviewFile, d *reviewDecisions) (int, error) {
	b, err := os.ReadFile(filepath.Join(dir, "job.json"))
	if err != nil {
		return 0, err
	}
	var job WorkflowJob
	if err = json.Unmarshal(b, &job); err != nil {
		return 0, err
	}
	root, err := filepath.Abs(job.Root)
	if err != nil {
		return 0, err
	}
	type plan struct {
		f        ReviewFile
		target   string
		contents []byte
		existed  bool
		original []byte
		mode     os.FileMode
		stage    string
	}
	var plans []plan
	for _, f := range files {
		if d.Decisions[f.Path] != decisionApproved || d.Applied[f.Path] {
			continue
		}
		if !safeRel(f.Path) || filepath.ToSlash(filepath.Clean(f.Path)) != f.Path {
			return 0, fmt.Errorf("unsafe proposed path: %q", f.Path)
		}
		if f.Status == "added" {
			parts := strings.Split(f.Path, "/")
			for i, part := range parts {
				if ignored(part, i < len(parts)-1) {
					return 0, fmt.Errorf("refusing to create ignored or sensitive path: %s", f.Path)
				}
			}
		}
		target := filepath.Join(root, filepath.FromSlash(f.Path))
		if !strings.HasPrefix(target, root+string(os.PathSeparator)) {
			return 0, errors.New("file outside workspace")
		}
		cur := root
		parts := strings.Split(filepath.FromSlash(f.Path), string(os.PathSeparator))
		for i, part := range parts {
			cur = filepath.Join(cur, part)
			info, e := os.Lstat(cur)
			if e != nil && !errors.Is(e, os.ErrNotExist) {
				return 0, e
			}
			if e == nil && (info.Mode()&os.ModeSymlink != 0 || (i < len(parts)-1 && !info.IsDir()) || (i == len(parts)-1 && !info.Mode().IsRegular())) {
				return 0, fmt.Errorf("unsafe destination: %s", f.Path)
			}
		}
		p := plan{f: f, target: target, mode: 0644}
		old, e := os.ReadFile(target)
		if e == nil {
			p.existed = true
			p.original = old
			fi, err := os.Stat(target)
			if err != nil {
				return 0, err
			}
			p.mode = fi.Mode().Perm()
		}
		if e != nil && !errors.Is(e, os.ErrNotExist) {
			return 0, e
		}
		switch f.Status {
		case "added":
			if p.existed {
				return 0, fmt.Errorf("new file already exists: %s", f.Path)
			}
		case "modified", "deleted":
			if !p.existed {
				return 0, fmt.Errorf("original file missing: %s", f.Path)
			}
			hash := sha256.Sum256(old)
			if hex.EncodeToString(hash[:]) != f.ExpectedSHA {
				return 0, fmt.Errorf("local changes detected: %s (refusing overwrite)", f.Path)
			}
		default:
			return 0, fmt.Errorf("not a proposed edit: %s", f.Path)
		}
		if f.Status != "deleted" {
			p.contents, e = os.ReadFile(f.After)
			if e != nil {
				return 0, e
			}
		}
		plans = append(plans, p)
	}
	if len(plans) == 0 {
		return 0, nil
	}
	backupDir := filepath.Join(dir, "backups", time.Now().UTC().Format("20060102T150405.000000000"))
	if err = os.MkdirAll(backupDir, 0700); err != nil {
		return 0, err
	}
	defer func() {
		for _, p := range plans {
			if p.stage != "" {
				_ = os.Remove(p.stage)
			}
		}
	}()
	// Prepare backups and staging before modifying any target.
	for i := range plans {
		p := &plans[i]
		if p.existed {
			dst := filepath.Join(backupDir, filepath.FromSlash(p.f.Path))
			if err = os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
				return 0, err
			}
			if err = os.WriteFile(dst, p.original, 0600); err != nil {
				return 0, err
			}
		}
		if p.f.Status != "deleted" {
			if err = os.MkdirAll(filepath.Dir(p.target), 0755); err != nil {
				return 0, err
			}
			tmp, e := os.CreateTemp(filepath.Dir(p.target), ".pipkin-stage-*")
			if e != nil {
				return 0, e
			}
			p.stage = tmp.Name()
			if _, e = tmp.Write(p.contents); e != nil {
				tmp.Close()
				return 0, e
			}
			if e = tmp.Chmod(p.mode); e != nil {
				tmp.Close()
				return 0, e
			}
			if e = tmp.Close(); e != nil {
				return 0, e
			}
		}
	}

	applied := 0
	for _, p := range plans {
		if p.f.Status == "deleted" {
			err = os.Remove(p.target)
		} else {
			err = os.Rename(p.stage, p.target)
		}
		if err != nil {
			break
		}
		applied++
	}
	if err != nil {
		for i := applied - 1; i >= 0; i-- {
			p := plans[i]
			if p.existed {
				_ = os.WriteFile(p.target, p.original, p.mode)
			} else {
				_ = os.Remove(p.target)
			}
		}
		return 0, fmt.Errorf("copy failed (rollback attempted; backups at %s): %w", backupDir, err)
	}
	for _, p := range plans {
		d.Applied[p.f.Path] = true
	}
	if err = saveDecisions(dir, *d); err != nil {
		return applied, fmt.Errorf("files copied but decisions could not be saved: %w", err)
	}
	return applied, nil
}

// importOutputPair lets older/manual ZIP workflows be reviewed in the TUI.
// The comparison always uses the ZIPs, never an implicit working-tree snapshot.
func importOutputPair(root, input, result string) (string, error) {
	if err := validateZipStrict(input); err != nil {
		return "", fmt.Errorf("input ZIP: %w", err)
	}
	if err := validateZipStrict(result); err != nil {
		return "", fmt.Errorf("result ZIP: %w", err)
	}
	base := filepath.Join(outputDirectory(), "jobs", filepath.Base(root))
	if err := os.MkdirAll(base, 0700); err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp(base, "job-import-")
	if err != nil {
		return "", err
	}
	fail := func(e error) (string, error) { _ = os.RemoveAll(dir); return "", e }
	if err = copyFile(input, filepath.Join(dir, "input.zip")); err != nil {
		return fail(err)
	}
	if err = copyFile(result, filepath.Join(dir, "result.zip")); err != nil {
		return fail(err)
	}
	inSHA, err := shaFile(filepath.Join(dir, "input.zip"))
	if err != nil {
		return fail(err)
	}
	outSHA, err := shaFile(filepath.Join(dir, "result.zip"))
	if err != nil {
		return fail(err)
	}
	files, err := zipChecksums(filepath.Join(dir, "input.zip"))
	if err != nil {
		return fail(err)
	}
	j := WorkflowJob{ID: filepath.Base(dir), Created: time.Now().UTC().Format(time.RFC3339), Root: root, Provider: "imported-zip", Model: "external", Status: "completed", Files: files, InputSHA256: inSHA, OutputSHA256: outSHA}
	if err = saveJob(dir, j); err != nil {
		return fail(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "prompt.txt"), []byte("Imported local ZIP pair; original prompt not available.\n"), 0600); err != nil {
		return fail(err)
	}
	return dir, nil
}
