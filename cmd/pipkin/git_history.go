package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type gitHistory struct {
	root, scope, upstream string
}

type gitCommit struct {
	Hash, Short, Date, Author, Subject string
}

var gitObjectID = regexp.MustCompile("^([0-9a-f]{40}|[0-9a-f]{64})$")

const gitDiffLimit = 512 * 1024

// Bound the displayed output even when the repository has very large changes.
type gitOutput struct {
	buffer    bytes.Buffer
	truncated bool
}

func (o *gitOutput) Write(p []byte) (int, error) {
	length := len(p)
	if remaining := gitDiffLimit - o.buffer.Len(); remaining < length {
		o.truncated = true
		p = p[:remaining]
	}
	o.buffer.Write(p)
	return length, nil
}

func readGit(ctx context.Context, root string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	prefix := []string{"--no-pager", "--literal-pathspecs", "-c", "color.ui=false",
		"-c", "core.fsmonitor=false", "-c", "core.quotePath=true"}
	cmd := exec.CommandContext(ctx, "git", append(prefix, args...)...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_NO_LAZY_FETCH=1")
	var stdout, stderr gitOutput
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if message := strings.TrimSpace(stderr.buffer.String()); message != "" {
			return "", errors.New(message)
		}
		return "", err
	}
	if stdout.truncated {
		return stdout.buffer.String() + "\n[Output truncated at 512 KiB]", nil
	}
	return stdout.buffer.String(), nil
}

func openGitHistory(ctx context.Context, workspace, scope string) (gitHistory, error) {
	// Resolve symlinks in the workspace itself to match Git's top-level path.
	workspace, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		return gitHistory{}, err
	}
	if scope != "" && (!safeRel(scope) || filepath.ToSlash(filepath.Clean(scope)) != scope) {
		return gitHistory{}, errors.New("invalid Git path")
	}
	top, err := readGit(ctx, workspace, "rev-parse", "--show-toplevel")
	if err != nil {
		return gitHistory{}, err
	}
	history := gitHistory{root: strings.TrimSuffix(top, "\n")}
	if scope != "" {
		history.scope, err = filepath.Rel(history.root, filepath.Join(workspace, scope))
		if err != nil {
			return gitHistory{}, err
		}
	}
	upstream, err := readGit(ctx, history.root, "rev-parse", "--symbolic-full-name", "@{upstream}")
	if err == nil {
		history.upstream = strings.TrimSpace(upstream)
	} else if branch, err := readGit(ctx, history.root, "symbolic-ref", "--quiet", "--short", "HEAD"); err == nil {
		ref := "refs/remotes/origin/" + strings.TrimSuffix(branch, "\n")
		if _, err = readGit(ctx, history.root, "show-ref", "--verify", "--", ref); err == nil {
			history.upstream = ref
		}
	}
	return history, nil
}

func (g gitHistory) scopeLabel() string {
	if g.scope == "" {
		return filepath.Base(g.root) + " (repository)"
	}
	return filepath.ToSlash(g.scope)
}

func (g gitHistory) upstreamLabel() string {
	return strings.TrimPrefix(strings.TrimPrefix(g.upstream, "refs/remotes/"), "refs/heads/")
}

func (g gitHistory) run(ctx context.Context, args ...string) (string, error) {
	args = append(args, "--")
	if g.scope != "" {
		args = append(args, g.scope)
	}
	return readGit(ctx, g.root, args...)
}

func (g gitHistory) log(ctx context.Context, local bool) ([]gitCommit, error) {
	revision := "HEAD"
	if local {
		if g.upstream == "" {
			return nil, errors.New("no tracked upstream or matching origin branch")
		}
		revision = g.upstream + "..HEAD"
	}
	if _, err := readGit(ctx, g.root, "rev-parse", "--verify", "HEAD"); err != nil {
		return nil, errors.New("the current branch has no commits")
	}
	output, err := g.run(ctx, "log", "-100", "--no-show-signature", "--format=%H%x00%h%x00%as%x00%an%x00%s", revision)
	if err != nil {
		return nil, err
	}
	var commits []gitCommit
	for _, line := range strings.Split(output, "\n") {
		fields := strings.SplitN(line, "\x00", 5)
		if len(fields) == 5 && gitObjectID.MatchString(fields[0]) {
			commits = append(commits, gitCommit{fields[0], fields[1], fields[2], fields[3], fields[4]})
		}
	}
	return commits, nil
}

func (g gitHistory) diff(ctx context.Context, mode, hash string) (string, error) {
	options := []string{"--no-ext-diff", "--no-textconv", "--ignore-submodules=dirty",
		"--src-prefix=a/", "--dst-prefix=b/"}
	switch mode {
	case "commit":
		if !gitObjectID.MatchString(hash) {
			return "", errors.New("invalid commit ID")
		}
		args := append([]string{"show", "--format=", "--no-show-signature", "--diff-merges=first-parent"}, options...)
		return g.run(ctx, append(args, hash+"^{commit}")...)
	case "upstream":
		if g.upstream == "" {
			return "", errors.New("no tracked upstream or matching origin branch")
		}
		return g.run(ctx, append(append([]string{"diff"}, options...), g.upstream, "HEAD")...)
	case "working":
		args := append([]string{"diff"}, options...)
		if _, err := readGit(ctx, g.root, "rev-parse", "--verify", "HEAD"); err == nil {
			args = append(args, "HEAD")
			return g.run(ctx, args...)
		}
		staged, err := g.run(ctx, append(args, "--cached")...)
		if err != nil {
			return "", err
		}
		unstaged, err := g.run(ctx, args...)
		return staged + unstaged, err
	default:
		return "", errors.New("unknown Git comparison")
	}
}

func (a *App) gitTUI(reader *bufio.Reader, scope string) error {
	ctx := context.Background()
	history, err := openGitHistory(ctx, a.cwd, scope)
	if err != nil {
		return err
	}
	local, cursor, offset := false, 0, 0
	commits, err := history.log(ctx, local)
	for {
		height, width := size()
		rows := max(1, height-5)
		offset = max(min(cursor, offset), cursor-rows+1)
		title := "History"
		if local {
			title = "Local commits"
		}
		upstream := history.upstreamLabel()
		if upstream == "" {
			upstream = "No upstream"
		}
		lines := []string{" PIPKIN / GIT · " + history.scopeLabel(),
			" ↑↓ move  Enter patch  l local/history  u upstream  w working  q back",
			fmt.Sprintf(" %s · %s · Up to 100 commits", title, upstream)}
		if err != nil {
			lines = append(lines, " "+err.Error())
		} else if len(commits) == 0 {
			lines = append(lines, " No commits for this path.")
		}
		for i := offset; i < min(len(commits), offset+rows); i++ {
			c := commits[i]
			marker := "   "
			if i == cursor {
				marker = " › "
			}
			lines = append(lines, marker+c.Short+"  "+c.Date+"  "+c.Subject+"  ·  "+c.Author)
		}
		for i, line := range lines {
			lines[i] = trunc(safeDisplayLine(line), width)
		}
		screenLinesANSI(lines)
		mode, hash := "", ""
		switch readKey(reader) {
		case "q", "b", "esc":
			return nil
		case "up", "k":
			cursor = max(0, cursor-1)
		case "down", "j":
			cursor = min(max(0, len(commits)-1), cursor+1)
		case "l", "right":
			local, cursor, offset = !local, 0, 0
			commits, err = history.log(ctx, local)
		case "enter":
			if len(commits) > 0 {
				mode, hash = "commit", commits[cursor].Hash
			}
		case "u":
			mode = "upstream"
		case "w":
			mode = "working"
		}
		if mode != "" {
			diff, diffErr := history.diff(ctx, mode, hash)
			if diffErr != nil {
				diff = diffErr.Error()
			}
			showDiffPager(reader, history.scopeLabel()+" · "+gitDiffTitle(mode, hash, history), gitPatchLines(diff), nil)
		}
	}
}

func gitDiffTitle(mode, hash string, g gitHistory) string {
	switch mode {
	case "commit":
		return "Commit " + hash
	case "upstream":
		return "Branch tip compared with " + g.upstreamLabel()
	default:
		return "Staged and unstaged changes to tracked files"
	}
}

func gitPatchLines(patch string) []string {
	if patch == "" {
		return []string{"No changes for this path."}
	}
	return strings.Split(strings.TrimSuffix(patch, "\n"), "\n")
}
