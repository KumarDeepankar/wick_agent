package main

import (
	"io"
	"os"
	"path/filepath"
)

type writeResult struct {
	Path         string `json:"path"`
	BytesWritten int    `json:"bytes_written"`
}

func cmdWrite(args []string) {
	if len(args) < 1 {
		writeError("usage: wickfs write <path> (content on stdin)")
		return
	}

	path := args[0]

	// Read all content from stdin
	content, err := io.ReadAll(os.Stdin)
	if err != nil {
		writeError("failed to read stdin: " + err.Error())
		return
	}

	// Ensure parent directories exist
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		writeError("failed to create directory: " + err.Error())
		return
	}

	// Atomic write: write to temp file, then rename
	tmp, err := os.CreateTemp(dir, ".wickfs-tmp-*")
	if err != nil {
		writeError("failed to create temp file: " + err.Error())
		return
	}
	tmpName := tmp.Name()

	_, err = tmp.Write(content)
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

	writeOK(writeResult{Path: path, BytesWritten: len(content)})
}
