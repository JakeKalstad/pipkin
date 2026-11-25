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
	"slices"
	"strings"
	"time"
)

//go:embed workbook.py
var workbookHelper string

func workbookPath(root, rel string) (string, error) {
	if !safeRel(rel) || filepath.ToSlash(filepath.Clean(rel)) != rel ||
		!hiddenPathAllowed(rel, false) || !spreadsheetPath(rel) {
		return "", errors.New("use a workspace-relative .csv or .xlsx path")
	}
	return filepath.Join(root, filepath.FromSlash(rel)), nil
}

func spreadsheetPath(rel string) bool {
	ext := filepath.Ext(rel)
	return strings.EqualFold(ext, ".csv") || strings.EqualFold(ext, ".xlsx")
}

func checkedWorkbookSource(root, input string) (string, error) {
	source, err := workbookPath(root, input)
	if err != nil {
		return "", err
	}
	info, err := statSelectedPath(root, input)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() > 32<<20 {
		return "", errors.New("workbook must be a regular file no larger than 32 MiB")
	}
	return source, nil
}

func workbookOperation(ctx context.Context, root, input, output string, edits []byte) (json.RawMessage, error) {
	source, err := checkedWorkbookSource(root, input)
	if err != nil {
		return nil, err
	}
	ext := strings.ToLower(filepath.Ext(input))
	args := []string{"-I", "-c", workbookHelper, source}
	var destination, tmp string
	if output != "" {
		if len(edits) == 0 || len(edits) > 256<<10 {
			return nil, errors.New("provide a cell edit manifest up to 256 KiB")
		}
		if !strings.EqualFold(filepath.Ext(output), ext) {
			return nil, errors.New("output must use the same spreadsheet format as the source")
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
		f, err := os.CreateTemp(filepath.Dir(destination), ".pipkin-workbook-*"+ext)
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
	var result json.RawMessage
	if ext == ".csv" {
		result, err = csvOperation(source, tmp, edits)
	} else {
		result, err = runXLSXOperation(ctx, root, args, edits)
	}
	if err != nil {
		return nil, err
	}
	if tmp != "" {
		if err := os.Link(tmp, destination); err != nil {
			return nil, err
		}
	}
	return json.RawMessage(result), nil
}

func runXLSXOperation(ctx context.Context, root string, args []string, edits []byte) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	python := strings.TrimSpace(os.Getenv("PIPKIN_XLSX_PYTHON"))
	if python == "" {
		python = filepath.Join(filepath.Dir(configPath()), "xlsx-venv", "bin", "python")
		if _, err := os.Stat(python); err != nil {
			python = "python3"
		}
	}
	cmd := exec.CommandContext(ctx, python, args...)
	cmd.Dir = root
	cmd.Stdin = bytes.NewReader(edits)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	result, err := cmd.Output()
	if err != nil {
		if stderr.Len() > 0 {
			return nil, errors.New(strings.TrimSpace(stderr.String()))
		}
		return nil, fmt.Errorf("XLSX helper: %w. Set PIPKIN_XLSX_PYTHON or install the XLSX environment with make install-xlsx", err)
	}
	if !json.Valid(result) {
		return nil, errors.New("invalid workbook response")
	}
	return json.RawMessage(result), nil
}

func (a *App) workbookTUI(reader *bufio.Reader, input string) {
	if _, err := workbookPath(a.cwd, input); err != nil {
		a.status = "Highlight a CSV or XLSX file first."
		return
	}
	if strings.EqualFold(filepath.Ext(input), ".csv") {
		a.csvTUI(reader, input)
		return
	}
	source, err := checkedWorkbookSource(a.cwd, input)
	if err != nil {
		a.status = safeDisplayLine(err.Error())
		return
	}
	args := []string{"-I", "-c", workbookHelper, source}
	ctx := context.Background()
	summary, err := runXLSXOperation(ctx, a.cwd, append(slices.Clone(args), "--sheets"), nil)
	if err != nil {
		a.status = safeDisplayLine(err.Error())
		return
	}
	var sheets []string
	if err := json.Unmarshal(summary, &sheets); err != nil || len(sheets) == 0 {
		a.status = "No worksheets available."
		return
	}
	index := 0
	if len(sheets) > 1 {
		for {
			height, width := size()
			rows := max(1, height-4)
			start := max(0, index-rows+1)
			lines := []string{" PIPKIN / XLSX WORKSHEETS", " Arrows choose  Enter open sheet  q back"}
			for i := start; i < min(len(sheets), start+rows); i++ {
				marker := "   "
				if i == index {
					marker = " › "
				}
				lines = append(lines, trunc(marker+safeDisplayLine(sheets[i]), width))
			}
			screenLinesANSI(lines)
			switch readKey(reader) {
			case "q", "esc":
				return
			case "up", "k":
				index = max(0, index-1)
			case "down", "j":
				index = min(len(sheets)-1, index+1)
			case "enter":
				goto openSheet
			}
		}
	}
openSheet:
	sheet := sheets[index]
	grid, err := runXLSXOperation(ctx, a.cwd, append(slices.Clone(args), "--grid", sheet), nil)
	if err != nil {
		a.status = safeDisplayLine(err.Error())
		return
	}
	var rows [][]string
	if err := json.Unmarshal(grid, &rows); err != nil {
		a.status = "Could not read the worksheet grid."
		return
	}
	a.spreadsheetGrid(reader, input, "XLSX: "+sheet, sheet, rows)
}
