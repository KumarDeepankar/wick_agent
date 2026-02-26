package main

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type grepMatch struct {
	File string `json:"file"`
	Line int    `json:"line"`
	Text string `json:"text"`
}

type grepResult struct {
	Matches   []grepMatch `json:"matches"`
	Truncated bool        `json:"truncated"`
}

const maxGrepMatches = 200

func cmdGrep(args []string) {
	if len(args) < 1 {
		writeError("usage: wickfs grep <pattern> [path]")
		return
	}

	pattern := args[0]
	searchPath := "."
	if len(args) > 1 && args[1] != "" {
		searchPath = args[1]
	}

	re, err := regexp.Compile(pattern)
	if err != nil {
		writeError("invalid regex: " + err.Error())
		return
	}

	var matches []grepMatch
	truncated := false

	filepath.WalkDir(searchPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip inaccessible
		}
		if d.IsDir() {
			name := d.Name()
			// Skip hidden and common non-code dirs
			if strings.HasPrefix(name, ".") || name == "node_modules" || name == "__pycache__" || name == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if truncated {
			return filepath.SkipAll
		}

		// Skip binary/large files by extension
		ext := strings.ToLower(filepath.Ext(path))
		if isBinaryExt(ext) {
			return nil
		}

		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()

		scanner := bufio.NewScanner(f)
		lineNum := 0
		for scanner.Scan() {
			lineNum++
			line := scanner.Text()
			if re.MatchString(line) {
				matches = append(matches, grepMatch{
					File: path,
					Line: lineNum,
					Text: line,
				})
				if len(matches) >= maxGrepMatches {
					truncated = true
					return filepath.SkipAll
				}
			}
		}
		return nil
	})

	if matches == nil {
		matches = []grepMatch{}
	}
	writeOK(grepResult{Matches: matches, Truncated: truncated})
}

func isBinaryExt(ext string) bool {
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".bmp", ".ico", ".webp",
		".zip", ".tar", ".gz", ".bz2", ".xz", ".7z",
		".pdf", ".doc", ".docx", ".xls", ".xlsx",
		".so", ".dylib", ".dll", ".exe", ".o", ".a",
		".wasm", ".pyc", ".class",
		".mp3", ".mp4", ".avi", ".mov", ".wav", ".flac":
		return true
	}
	return false
}
