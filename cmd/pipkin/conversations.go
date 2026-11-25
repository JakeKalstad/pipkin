package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"
)

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type conversationSummary struct {
	ID      string    `json:"id"`
	Title   string    `json:"title"`
	Updated time.Time `json:"updated"`
}

type conversation struct {
	conversationSummary
	Messages        []chatMessage `json:"messages"`
	ProviderProfile string        `json:"provider_profile,omitempty"`
}

const conversationSize = 4 << 20

func conversationDirectory(root string) (string, error) {
	path, err := instructionSettingsPath(root)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(filepath.Dir(path)), "conversations", strings.TrimSuffix(filepath.Base(path), ".json")), nil
}

func conversationPath(dir, id string) (string, error) {
	if len(id) != 32 || strings.ToLower(id) != id {
		return "", errors.New("invalid conversation ID")
	}
	if _, err := hex.DecodeString(id); err != nil {
		return "", errors.New("invalid conversation ID")
	}
	return filepath.Join(dir, id+".json"), nil
}

func readConversation(dir, id string) (conversation, error) {
	var thread conversation
	path, err := conversationPath(dir, id)
	if err != nil {
		return thread, err
	}
	f, err := os.Open(path)
	if err != nil {
		return thread, err
	}
	defer f.Close()
	data, err := readLimited(f, conversationSize)
	if err != nil {
		return thread, err
	}
	err = json.Unmarshal(data, &thread)
	return thread, err
}

func listConversations(dir string) ([]conversationSummary, error) {
	list := make([]conversationSummary, 0)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return list, nil
	}
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !entry.Type().IsRegular() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		thread, err := readConversation(dir, strings.TrimSuffix(entry.Name(), ".json"))
		if err == nil {
			list = append(list, thread.conversationSummary)
		}
	}
	slices.SortFunc(list, func(a, b conversationSummary) int {
		if n := b.Updated.Compare(a.Updated); n != 0 {
			return n
		}
		return strings.Compare(a.ID, b.ID)
	})
	return list, nil
}

// Keep the read/check/write sequence atomic across web, terminal and MCP
// processes. The lock is held only for local mutations, never a model call.
func lockConversations(dir string, change func() error) error {
	return withFileLock(filepath.Join(dir, "lock"), change)
}

func withFileLock(path string, change func() error) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	return change()
}

func deleteConversation(dir, id string) error {
	path, err := conversationPath(dir, id)
	if err != nil {
		return err
	}
	return lockConversations(dir, func() error { return os.Remove(path) })
}

func saveConversation(dir string, thread conversation, previous int) error {
	data, err := json.Marshal(thread)
	if err != nil {
		return err
	}
	if len(data) > conversationSize {
		return errors.New("conversation exceeds 4 MiB; start a new conversation")
	}
	path, err := conversationPath(dir, thread.ID)
	if err != nil {
		return err
	}
	return lockConversations(dir, func() error {
		if previous != 0 {
			current, err := readConversation(dir, thread.ID)
			if err != nil {
				return err
			}
			if len(current.Messages) != previous {
				return errors.New("conversation changed while the model was responding; reopen it and retry")
			}
		}
		f, err := os.CreateTemp(dir, "conversation-*")
		if err != nil {
			return err
		}
		defer os.Remove(f.Name())
		_, err = f.Write(data)
		if err = errors.Join(err, f.Close()); err != nil {
			return err
		}
		return os.Rename(f.Name(), path)
	})
}

func sendConversation(ctx context.Context, root, id, prompt string, cfg Config, files ...*Node) (conversation, string, error) {
	dir, err := conversationDirectory(root)
	if err != nil {
		return conversation{}, "", err
	}
	var thread conversation
	prompt = strings.TrimSpace(prompt)
	if id == "" {
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			return thread, "", err
		}
		title := []rune(strings.Join(strings.Fields(prompt), " "))
		if len(title) > 60 {
			title = append(title[:59], '…')
		}
		thread.ID, thread.Title = fmt.Sprintf("%x", random), string(title)
	} else {
		thread, err = readConversation(dir, id)
		if err != nil {
			return thread, "", err
		}
	}
	previous := len(thread.Messages)
	reply, err := freePrompt(ctx, root, prompt, cfg, thread.Messages, files...)
	if err != nil {
		return thread, "", err
	}
	thread.Messages = append(thread.Messages, chatMessage{"user", prompt}, chatMessage{"assistant", reply})
	thread.Updated = time.Now().UTC()
	thread.ProviderProfile = cfg.ProviderProfile
	if err := saveConversation(dir, thread, previous); err != nil {
		return thread, "", err
	}
	return thread, reply, nil
}
