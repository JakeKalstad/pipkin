package main

import (
	"bufio"
	"context"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

func freePrompt(ctx context.Context, root, prompt string, cfg Config, history []chatMessage) (string, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" || len(prompt) > maxInstructionBytes || !utf8.ValidString(prompt) || strings.ContainsRune(prompt, 0) {
		return "", errors.New("prompt must contain 1 to 32768 bytes of UTF-8 text")
	}
	if cfg.configError != nil {
		return "", cfg.configError
	}
	if !cfg.supportsAgentic() {
		return "", errors.New("free prompts require a text-capable provider")
	}
	sources, err := cfg.prepareInstructions(root)
	if err != nil {
		return "", err
	}
	// Retain recent complete turns within the file-context budget used by
	// this provider profile. The on-disk transcript keeps all turns.
	budget := max(0, cfg.MaxContextBytes-len(prompt)-len(cfg.instructionText))
	start := len(history)
	for start >= 2 {
		pair := len(history[start-2].Content) + len(history[start-1].Content)
		if pair > budget {
			break
		}
		budget -= pair
		start -= 2
	}
	history = history[start:]
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	job := WorkflowJob{Root: root, Created: time.Now().UTC().Format(time.RFC3339Nano), Provider: cfg.Provider, Model: cfg.Model, Status: "completed", Operation: "free-prompt", Instructions: sources}
	trace := &llmTrace{}
	ctx = withWorkflowEvents(context.WithValue(ctx, llmTraceKey{}, trace), func(workflowEvent) {})
	reply, err := completeMessages(ctx, cfg, cfg.withInstructions("Answer the user's request directly. No project files or tools are attached."), append(history, chatMessage{"user", prompt}), &job)
	if err != nil {
		job.Status, job.Error = "failed", err.Error()
	}
	_ = reportLangfuse(cfg, job, trace.calls...)
	return reply, err
}

func (a *App) freePromptTUI(reader *bufio.Reader) {
	dir, err := conversationDirectory(a.cwd)
	if err != nil {
		a.status = safeDisplayLine(err.Error())
		return
	}
	index := 0
	var notice string
	for {
		list, err := listConversations(dir)
		if err != nil {
			a.status = safeDisplayLine(err.Error())
			return
		}
		index = min(index, len(list))
		height, width := size()
		rows := max(1, height-4)
		start := max(0, index-rows+1)
		lines := []string{" PIPKIN / FREE PROMPT HISTORY", " Enter open  n new  d delete  q back", " " + notice, ""}
		for i := start; i < min(len(list)+1, start+rows); i++ {
			label := "New conversation"
			if i > 0 {
				label = list[i-1].Title
			}
			marker := "   "
			if i == index {
				marker = " › "
			}
			lines = append(lines, trunc(marker+safeDisplayLine(label), width))
		}
		screenLinesANSI(lines)
		switch readKey(reader) {
		case "q", "esc":
			return
		case "k", "up":
			index = max(0, index-1)
		case "j", "down":
			index = min(len(list), index+1)
		case "n":
			a.composeConversation(reader, conversation{})
		case "enter":
			var thread conversation
			if index > 0 {
				thread, err = readConversation(dir, list[index-1].ID)
				if err != nil {
					notice = safeDisplayLine(err.Error())
					continue
				}
			}
			a.composeConversation(reader, thread)
		case "d":
			if index == 0 {
				continue
			}
			screenLinesANSI([]string{" Delete " + safeDisplayLine(list[index-1].Title) + "?", " Press Y to delete, any other key to cancel."})
			if readKey(reader) == "Y" {
				notice = "Conversation deleted."
				if err := deleteConversation(dir, list[index-1].ID); err != nil {
					notice = safeDisplayLine(err.Error())
				}
			}
		}
	}
}

func conversationTranscript(thread conversation, width int) []string {
	var lines []string
	for _, message := range thread.Messages {
		label := "You"
		if message.Role == "assistant" {
			label = "Pipkin"
		}
		lines = append(lines, label)
		lines = append(lines, wrapPromptText(message.Content, width)...)
		lines = append(lines, "")
	}
	return lines
}

func (a *App) composeConversation(reader *bufio.Reader, thread conversation) {
	var prompt, status string
	for {
		height, width := size()
		body := append(conversationTranscript(thread, max(1, width-4)), wrapPromptText("> "+prompt, max(1, width-4))...)
		if len(body) > max(1, height-5) {
			body = body[len(body)-max(1, height-5):]
		}
		lines := []string{" PIPKIN / FREE PROMPT", " Type your prompt  Enter newline  Ctrl+S send  Ctrl+T transcript  Esc history", " " + status, ""}
		for _, line := range body {
			lines = append(lines, " "+line)
		}
		screenLinesANSI(lines)
		switch key := readKey(reader); key {
		case "esc":
			return
		case "\x14":
			showPager(reader, " PIPKIN / TRANSCRIPT", conversationTranscript(thread, max(1, width-2)), nil, safeDisplayLine)
		case "\x13":
			if strings.TrimSpace(prompt) == "" {
				status = "Enter a prompt before sending."
				continue
			}
			screenLinesANSI([]string{" PIPKIN / FREE PROMPT", " Waiting for " + safeDisplayLine(a.cfg.Model) + "…"})
			updated, _, err := sendConversation(context.Background(), a.cwd, thread.ID, prompt, a.cfg)
			if err != nil {
				status = safeDisplayLine(err.Error())
				continue
			}
			thread, prompt, status = updated, "", ""
		case "enter":
			if len(prompt) < maxInstructionBytes {
				prompt += "\n"
			}
		case "\x7f", "\b":
			if len(prompt) > 0 {
				_, size := utf8.DecodeLastRuneInString(prompt)
				prompt = prompt[:len(prompt)-size]
			}
		default:
			r, size := utf8.DecodeRuneInString(key)
			if size == len(key) && unicode.IsPrint(r) && len(prompt)+size <= maxInstructionBytes {
				prompt += key
			}
		}
	}
}

func wrapPromptText(text string, width int) []string {
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		runes := []rune(safeDisplayLine(line))
		for len(runes) > width {
			lines = append(lines, string(runes[:width]))
			runes = runes[width:]
		}
		lines = append(lines, string(runes))
	}
	return lines
}
