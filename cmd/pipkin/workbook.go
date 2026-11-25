package main

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

//go:embed workbook.py
var workbookHelper string

func workbookPath(root, rel string) (string, error) {
	if !safeRel(rel) || filepath.ToSlash(filepath.Clean(rel)) != rel ||
		!hiddenPathAllowed(rel, false) || !strings.EqualFold(filepath.Ext(rel), ".xlsx") {
		return "", errors.New("use a workspace-relative .xlsx path")
	}
	return filepath.Join(root, filepath.FromSlash(rel)), nil
}

func workbookOperation(ctx context.Context, root, input, output string, edits []byte) (json.RawMessage, error) {
	source, err := workbookPath(root, input)
	if err != nil {
		return nil, err
	}
	info, err := statSelectedPath(root, input)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 32<<20 {
		return nil, errors.New("workbook must be a regular file no larger than 32 MiB")
	}
	python := strings.TrimSpace(os.Getenv("PIPKIN_XLSX_PYTHON"))
	if python == "" {
		python = "python3"
	}
	args := []string{"-I", "-c", workbookHelper, source}
	var destination, tmp string
	if output != "" {
		if len(edits) == 0 || len(edits) > 256<<10 {
			return nil, errors.New("provide a cell edit manifest up to 256 KiB")
		}
		destination, err = workbookPath(root, output)
		if err != nil {
			return nil, err
		}
		if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
			if err == nil {
				return nil, errors.New("workbook output already exists")
			}
			return nil, err
		}
		parent, err := statSelectedPath(root, filepath.Dir(output))
		if err != nil {
			return nil, err
		}
		if !parent.IsDir() {
			return nil, errors.New("output parent must be a directory")
		}
		f, err := os.CreateTemp(filepath.Dir(destination), ".pipkin-workbook-*.xlsx")
		if err != nil {
			return nil, err
		}
		tmp = f.Name()
		defer os.Remove(tmp)
		if err := f.Close(); err != nil {
			return nil, err
		}
		args = append(args, tmp)
	} else if len(edits) > 0 {
		return nil, errors.New("cell edits require a separate output workbook path")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, args...)
	cmd.Dir = root
	cmd.Stdin = bytes.NewReader(edits)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	result, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("workbook operation: %w %s", err, strings.TrimSpace(stderr.String()))
	}
	if !json.Valid(result) {
		return nil, errors.New("invalid workbook response")
	}
	if tmp != "" {
		if err := os.Link(tmp, destination); err != nil {
			return nil, err
		}
	}
	return json.RawMessage(result), nil
}

func (a *App) workbookTUI(reader *bufio.Reader, input string, restore func()) {
	if _, err := workbookPath(a.cwd, input); err != nil {
		a.status = "Highlight an .xlsx workbook first."
		return
	}
	for {
		screenLinesANSI([]string{" PIPKIN / WORKBOOK", " " + safeDisplayLine(input),
			" Enter preview  e edit cells in a new copy  q back", " " + safeDisplayLine(a.status)})
		var output string
		var edits []byte
		switch readKey(reader) {
		case "q", "esc":
			return
		case "enter":
		case "e":
			suggested := strings.TrimSuffix(input, filepath.Ext(input)) + "-edited.xlsx"
			request := struct {
				Output string          `json:"output"`
				Edits  json.RawMessage `json:"edits"`
			}{suggested, json.RawMessage("[]")}
			draft, _ := json.MarshalIndent(request, "", "  ")
			text, err := editTerminalDocument(a.cwd, "*.json", string(draft)+"\n", 256<<10, restore)
			if err == nil {
				err = json.Unmarshal(text, &request)
			}
			if err != nil {
				a.status = err.Error()
				continue
			}
			if len(request.Edits) == 0 || bytes.Equal(request.Edits, []byte("[]")) {
				continue
			}
			if request.Output == "" {
				a.status = "Supply a new output workbook path."
				continue
			}
			output, edits = request.Output, request.Edits
		default:
			continue
		}
		result, err := workbookOperation(context.Background(), a.cwd, input, output, edits)
		if err != nil {
			a.status = err.Error()
			continue
		}
		title := " PIPKIN / WORKBOOK PREVIEW"
		if output != "" {
			a.lastOutput = filepath.Join(a.cwd, output)
			title = " PIPKIN / SAVED " + safeDisplayLine(output)
		}
		formatted, _ := json.MarshalIndent(result, "", "  ")
		_, width := size()
		showPager(reader, title, wrapPromptText(string(formatted), max(1, width-2)), nil, safeDisplayLine)
	}
}
