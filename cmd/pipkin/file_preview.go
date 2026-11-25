package main

import (
	"bufio"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"unicode/utf8"
)

const maxPreviewBytes = 256 << 10

func (a *App) previewFile(reader *bufio.Reader, node *Node) error {
	if node == nil || node.dir {
		return errors.New("highlight a file to preview")
	}
	rel := filepath.ToSlash(node.rel)
	if !safeRel(rel) || filepath.ToSlash(filepath.Clean(rel)) != rel || !hiddenPathAllowed(rel, a.showHidden) {
		return errors.New("file is outside the picker scope")
	}
	info, err := statSelectedPath(a.cwd, node.rel)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("preview requires a regular file")
	}
	if info.Size() > maxPreviewBytes {
		return errors.New("file exceeds the 256 KiB preview limit")
	}
	file, err := os.Open(filepath.Join(a.cwd, node.rel))
	if err != nil {
		return err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(info, opened) {
		return errors.New("file changed while opening preview")
	}
	data, err := readLimited(file, maxPreviewBytes)
	if err != nil {
		return err
	}
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		if filepath.Ext(rel) == ".xlsx" {
			return errors.New("press W to preview this workbook")
		}
		return errors.New("preview supports UTF-8 text files")
	}
	text := string(bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n")))
	if text == "" {
		text = "(Empty file)"
	}
	_, width := size()
	showPager(reader, " PIPKIN / FILE: "+rel, wrapPromptText(text, max(1, width-2)), nil, safeDisplayLine)
	return nil
}
