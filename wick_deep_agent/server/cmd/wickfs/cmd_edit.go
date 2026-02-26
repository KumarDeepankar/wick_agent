package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type editInput struct {
	OldText string `json:"old_text"`
	NewText string `json:"new_text"`
}

type editResult struct {
	Path         string `json:"path"`
	Replacements int    `json:"replacements"`
}

func cmdEdit(args []string) {
	if len(args) < 1 {
		writeError("usage: wickfs edit <path> (JSON {old_text, new_text} on stdin)")
		return
	}

	path := args[0]

	// Read edit instruction from stdin
	stdinData, err := io.ReadAll(os.Stdin)
	if err != nil {
		writeError("failed to read stdin: " + err.Error())
		return
	}

	var input editInput
	if err := json.Unmarshal(stdinData, &input); err != nil {
		writeError("invalid JSON input: " + err.Error())
		return
	}

	if input.OldText == "" {
		writeError("old_text must not be empty")
		return
	}

	// Read the file
	content, err := os.ReadFile(path)
	if err != nil {
		writeError(err.Error())
		return
	}

	original := string(content)
	count := strings.Count(original, input.OldText)
	if count == 0 {
		writeError("old_text not found in file")
		return
	}

	// Replace first occurrence only
	updated := strings.Replace(original, input.OldText, input.NewText, 1)

	// Atomic write
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".wickfs-tmp-*")
	if err != nil {
		writeError("failed to create temp file: " + err.Error())
		return
	}
	tmpName := tmp.Name()

	_, err = tmp.WriteString(updated)
	tmp.Close()
	if err != nil {
		os.Remove(tmpName)
		writeError("failed to write: " + err.Error())
		return
	}

	if err := os.Chmod(tmpName, 0666); err != nil {
		os.Remove(tmpName)
		writeError("failed to set permissions: " + err.Error())
		return
	}

	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		writeError("failed to rename: " + err.Error())
		return
	}

	writeOK(editResult{Path: path, Replacements: 1})
}
