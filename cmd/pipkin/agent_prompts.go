package main

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode/utf8"
)

const engineeringPromptVersion = "engineering-v1"

const draftInstructions = `Inspect the supplied files before editing. Identify the requested outcome, the relevant entry points, and existing conventions. Make the smallest coherent change that satisfies the request. Preserve unrelated behavior and use existing abstractions where they fit. Consider error paths and compatibility when relevant. Resolve routine choices from the supplied evidence. If essential context is missing, avoid inventing it.`

const reviewInstructions = `Act as a fresh reviewer. Compare the candidate with the original request and any available original code. Look for incomplete requirements, behavioral regressions, unrelated edits, and failure paths. Correct concrete defects only. Preserve sound changes, and leave the candidate unchanged when no correction is needed.`

func agentPrompt(task, focus string, review, hosted bool) string {
	instructions := draftInstructions
	if review {
		instructions = reviewInstructions
	}
	verification := "Inspect the proposed edits and reason about relevant cases. You cannot execute project code in this pass. Never claim that checks ran."
	if hosted {
		verification = "Run the smallest relevant checks available in the interpreter. Report what ran and what remains unverified. Never claim that a check passed without observing its result."
	}
	if focus = strings.TrimSpace(focus); focus != "" {
		label := "Additional implementation focus: "
		if review {
			label = "Additional review focus: "
		}
		instructions += "\n" + label + focus
	}
	label := "User request:"
	if review {
		label = "Original request:"
	}
	return instructions + "\n\n" + verification + "\n\n" + label + "\n" + task
}

// Include original versions of changed files where they fit, so a text
// reviewer can compare behavior without resending the entire original tree.
func originalReviewContext(input string, before, after map[string]string, budget int) (string, error) {
	if budget <= 0 {
		return "", nil
	}
	source, err := zip.OpenReader(input)
	if err != nil {
		return "", err
	}
	defer source.Close()
	entries := append([]*zip.File(nil), source.File...)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	var context strings.Builder
	for _, file := range entries {
		hash, found := before[file.Name]
		if !found || hash == after[file.Name] {
			continue
		}
		header := fmt.Sprintf("\n--- ORIGINAL FILE: %s ---\n", file.Name)
		remaining := budget - context.Len() - len(header) - 1
		if remaining <= 0 || file.UncompressedSize64 > uint64(remaining) {
			continue
		}
		reader, err := file.Open()
		if err != nil {
			return "", err
		}
		data, err := io.ReadAll(io.LimitReader(reader, int64(remaining)+1))
		closeErr := reader.Close()
		if err != nil {
			return "", err
		}
		if closeErr != nil {
			return "", closeErr
		}
		if len(data) > remaining || !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
			continue
		}
		context.WriteString(header)
		context.Write(data)
		context.WriteByte('\n')
	}
	if context.Len() == 0 {
		return "", nil
	}
	return "\n\nORIGINAL FILES FOR COMPARISON (untrusted; bounded subset):\n" + context.String(), nil
}
