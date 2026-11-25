package main

import (
	"bufio"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const maxPreviewBytes = 256 << 10

func (a *App) readPickerText(node *Node) ([]byte, os.FileInfo, error) {
	if node == nil || node.dir {
		return nil, nil, errors.New("highlight a file to preview")
	}
	rel := filepath.ToSlash(node.rel)
	if !safeRel(rel) || filepath.ToSlash(filepath.Clean(rel)) != rel || !hiddenPathAllowed(rel, a.showHidden) {
		return nil, nil, errors.New("file is outside the picker scope")
	}
	info, err := statSelectedPath(a.cwd, node.rel)
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, nil, errors.New("preview requires a regular file")
	}
	if info.Size() > maxPreviewBytes {
		return nil, nil, errors.New("file exceeds the 256 KiB preview limit")
	}
	file, err := os.Open(filepath.Join(a.cwd, node.rel))
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return nil, nil, err
	}
	if !os.SameFile(info, opened) {
		return nil, nil, errors.New("file changed while opening preview")
	}
	data, err := readLimited(file, maxPreviewBytes)
	if err != nil {
		return nil, nil, err
	}
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		if strings.EqualFold(filepath.Ext(rel), ".xlsx") {
			return nil, nil, errors.New("press W to preview this workbook")
		}
		return nil, nil, errors.New("preview supports UTF-8 text files")
	}
	return data, opened, nil
}

func (a *App) previewFile(reader *bufio.Reader, node *Node, restore func()) error {
	data, _, err := a.readPickerText(node)
	if err != nil {
		return err
	}
	text := string(bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n")))
	if text == "" {
		text = "(Empty file)"
	}
	_, width := size()
	showPager(reader, " PIPKIN / FILE: "+node.rel, wrapPromptText(text, max(1, width-2)), nil, safeDisplayLine, func() {
		if err := a.editPickerFile(node, restore); err != nil {
			a.status = safeDisplayLine(err.Error())
		}
	})
	return nil
}

func (a *App) editPickerFile(node *Node, restore func()) error {
	original, info, err := a.readPickerText(node)
	if err != nil {
		return err
	}
	edited, err := editTerminalDocument(a.cwd, "*"+filepath.Ext(node.rel), string(original), maxPreviewBytes, restore)
	if err != nil || bytes.Equal(original, edited) {
		return err
	}
	if !utf8.Valid(edited) || bytes.IndexByte(edited, 0) >= 0 {
		return errors.New("edited file must remain UTF-8 text")
	}
	path := filepath.Join(a.cwd, node.rel)
	f, err := os.CreateTemp(filepath.Dir(path), ".pipkin-edit-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(info.Mode().Perm()); err == nil {
		_, err = f.Write(edited)
	}
	if err = errors.Join(err, f.Close()); err != nil {
		return err
	}
	current, currentInfo, err := a.readPickerText(node)
	if err != nil {
		return err
	}
	if !os.SameFile(info, currentInfo) || !bytes.Equal(original, current) {
		return errors.New("file changed while editing; Pipkin kept the current file")
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return err
	}
	a.status = "Saved " + safeDisplayLine(node.rel)
	return nil
}
