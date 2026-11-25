package main

import (
	"bufio"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

var spreadsheetNumber = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)

var csvAddress = regexp.MustCompile(`^([A-Z]{1,3})([1-9][0-9]{0,6})$`)

func readCSV(path string) ([][]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buffer := bufio.NewReader(f)
	if prefix, _ := buffer.Peek(3); string(prefix) == "\xef\xbb\xbf" {
		_, _ = buffer.Discard(3)
	}
	r := csv.NewReader(buffer)
	r.FieldsPerRecord = -1
	rows, err := r.ReadAll()
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		for _, value := range row {
			if !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
				return nil, errors.New("CSV must contain UTF-8 text")
			}
		}
	}
	return rows, nil
}

func csvColumnName(col int) string {
	var name string
	for col++; col > 0; col = (col - 1) / 26 {
		name = string(rune('A'+(col-1)%26)) + name
	}
	return name
}

type csvCellEdit struct {
	Sheet string          `json:"sheet,omitempty"`
	Cell  string          `json:"cell"`
	Value json.RawMessage `json:"value"`
	Kind  string          `json:"kind,omitempty"`
}

func csvOperation(source, output string, edits []byte) (json.RawMessage, error) {
	rows, err := readCSV(source)
	if err != nil {
		return nil, err
	}
	if output == "" {
		columns := 0
		for _, row := range rows {
			columns = max(columns, len(row))
		}
		preview := make([][]string, 0, min(20, len(rows)))
		for _, row := range rows[:min(20, len(rows))] {
			preview = append(preview, row[:min(12, len(row))])
		}
		return json.Marshal(map[string]any{"sheet_count": 1, "sheets": []any{
			map[string]any{"name": "CSV", "rows": len(rows), "columns": columns, "preview": preview},
		}})
	}
	var changes []csvCellEdit
	if err := json.Unmarshal(edits, &changes); err != nil {
		return nil, err
	}
	if len(changes) == 0 || len(changes) > 1000 {
		return nil, errors.New("provide 1 to 1000 CSV cell edits")
	}
	seen := make(map[string]bool)
	report := make([]map[string]string, 0, len(changes))
	for _, edit := range changes {
		address := strings.ToUpper(edit.Cell)
		parts := csvAddress.FindStringSubmatch(address)
		if len(parts) != 3 || seen[address] || (edit.Sheet != "" && edit.Sheet != "CSV") {
			return nil, fmt.Errorf("invalid or duplicate CSV cell %q", edit.Cell)
		}
		if edit.Kind != "" && edit.Kind != "value" {
			return nil, errors.New("CSV edits accept values only; use XLSX for formulas")
		}
		seen[address] = true
		row, _ := strconv.Atoi(parts[2])
		col := 0
		for _, letter := range parts[1] {
			col = col*26 + int(letter-'A'+1)
		}
		if row > len(rows) || col > len(rows[row-1]) {
			return nil, fmt.Errorf("%s is outside the existing CSV cells", address)
		}
		decoder := json.NewDecoder(strings.NewReader(string(edit.Value)))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			return nil, fmt.Errorf("%s requires a JSON scalar value", address)
		}
		var formatted string
		switch v := value.(type) {
		case nil:
		case string:
			formatted = v
		case bool, json.Number:
			formatted = fmt.Sprint(v)
		default:
			return nil, fmt.Errorf("%s requires a JSON scalar value", address)
		}
		if strings.ContainsRune(formatted, 0) {
			return nil, errors.New("CSV cell values cannot contain NUL")
		}
		previous := rows[row-1][col-1]
		rows[row-1][col-1] = formatted
		report = append(report, map[string]string{"sheet": "CSV", "cell": address, "before": previous, "after": formatted})
	}
	f, err := os.Create(output)
	if err != nil {
		return nil, err
	}
	buffer := bufio.NewWriter(f)
	w := csv.NewWriter(buffer)
	for _, row := range rows {
		// A quoted empty field is distinct from a blank line, which CSV readers skip.
		if len(row) == 1 && row[0] == "" {
			w.Flush()
			_, err = buffer.WriteString("\"\"\n")
		} else {
			err = w.Write(row)
		}
		if err != nil {
			break
		}
	}
	w.Flush()
	err = errors.Join(err, w.Error(), buffer.Flush())
	if err = errors.Join(err, f.Close()); err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"changes": report})
}

func (a *App) csvTUI(reader *bufio.Reader, input string) {
	source, err := checkedWorkbookSource(a.cwd, input)
	if err != nil {
		a.status = safeDisplayLine(err.Error())
		return
	}
	rows, err := readCSV(source)
	if err != nil || len(rows) == 0 {
		a.status = "CSV has no rows to edit."
		if err != nil {
			a.status = safeDisplayLine(err.Error())
		}
		return
	}
	a.spreadsheetGrid(reader, input, "CSV", "", rows)
}

func (a *App) spreadsheetGrid(reader *bufio.Reader, input, label, sheet string, rows [][]string) {
	if len(rows) == 0 || len(rows[0]) == 0 {
		a.status = "The spreadsheet has no cells to edit."
		return
	}
	row, col := 0, 0
	pending := make(map[string]csvCellEdit)
	var notice string
	for {
		height, width := size()
		visibleRows := max(1, height-6)
		visibleColumns := max(1, (width-7)/16)
		firstRow := max(0, row-visibleRows+1)
		firstCol := max(0, col-visibleColumns+1)
		header := "       "
		for c := firstCol; c < firstCol+visibleColumns; c++ {
			header += terminalPad(csvColumnName(c), 16)
		}
		lines := []string{" PIPKIN / " + safeDisplayLine(label) + ": " + safeDisplayLine(input), " Arrows move  Enter edit cell  s save copy  q back", " " + safeDisplayLine(notice), header}
		for r := firstRow; r < min(len(rows), firstRow+visibleRows); r++ {
			line := fmt.Sprintf("%5d  ", r+1)
			for c := firstCol; c < min(len(rows[r]), firstCol+visibleColumns); c++ {
				label := " " + safeDisplayLine(rows[r][c])
				if r == row && c == col {
					label = "›" + safeDisplayLine(rows[r][c])
				}
				line += terminalPad(label, 16)
			}
			lines = append(lines, line)
		}
		screenLinesANSI(lines)
		switch key := readKey(reader); key {
		case "q", "esc":
			if len(pending) != 0 {
				screenLinesANSI([]string{" Discard unsaved cell edits? Press Y to discard."})
				if readKey(reader) != "Y" {
					continue
				}
			}
			return
		case "up", "down", "j", "k":
			if key == "up" || key == "k" {
				row = max(0, row-1)
			} else {
				row = min(len(rows)-1, row+1)
			}
			col = min(col, max(0, len(rows[row])-1))
		case "left", "h":
			col = max(0, col-1)
		case "right", "l":
			col = min(len(rows[row])-1, col+1)
		case "enter", "e":
			if len(rows[row]) == 0 {
				continue
			}
			address := csvColumnName(col) + strconv.Itoa(row+1)
			if value, ok := csvEditText(reader, "Edit "+address, rows[row][col]); ok && value != rows[row][col] {
				data, _ := json.Marshal(value)
				edit := csvCellEdit{Sheet: sheet, Cell: address, Value: data}
				if sheet != "" && strings.HasPrefix(value, "=") {
					edit.Kind = "formula"
				} else if sheet != "" && value == "" {
					edit.Value = json.RawMessage("null")
				} else if sheet != "" && spreadsheetNumber.MatchString(value) {
					edit.Value = json.RawMessage(value)
				}
				pending[address] = edit
				rows[row][col] = value
				notice = fmt.Sprintf("%d unsaved cell edit(s)", len(pending))
			}
		case "s":
			if len(pending) == 0 {
				notice = "No pending edits."
				continue
			}
			ext := filepath.Ext(input)
			suggested := strings.TrimSuffix(input, ext) + "-edited" + ext
			title := "Save a new " + strings.ToUpper(strings.TrimPrefix(ext, ".")) + " copy"
			output, ok := csvEditText(reader, title, suggested)
			if !ok {
				continue
			}
			if strings.TrimSpace(output) == "" {
				notice = "Enter an output path."
				continue
			}
			edits := make([]csvCellEdit, 0, len(pending))
			for _, edit := range pending {
				edits = append(edits, edit)
			}
			slices.SortFunc(edits, func(a, b csvCellEdit) int { return strings.Compare(a.Cell, b.Cell) })
			data, _ := json.Marshal(edits)
			if _, err := workbookOperation(context.Background(), a.cwd, input, output, data); err != nil {
				notice = err.Error()
				continue
			}
			a.lastOutput = filepath.Join(a.cwd, output)
			a.status = "Saved: " + a.lastOutput
			return
		}
	}
}

func csvEditText(reader *bufio.Reader, title, value string) (string, bool) {
	replace := true
	for {
		_, width := size()
		screenLinesANSI([]string{" " + title, " Type to replace  Backspace edits  Enter confirms  Esc cancels", " > " + trunc(safeDisplayLine(value), max(1, width-4))})
		switch key := readKey(reader); key {
		case "esc":
			return "", false
		case "enter":
			return value, true
		case "\x7f", "\b":
			if len(value) > 0 {
				_, size := utf8.DecodeLastRuneInString(value)
				value = value[:len(value)-size]
			}
			replace = false
		default:
			r, size := utf8.DecodeRuneInString(key)
			if size == len(key) && unicode.IsPrint(r) {
				if replace {
					value = ""
					replace = false
				}
				if len(value)+size <= 32<<10 {
					value += key
				}
			}
		}
	}
}
