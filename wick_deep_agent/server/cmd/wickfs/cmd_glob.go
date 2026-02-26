package main

import (
	"os"
	"path/filepath"
	"strings"
)

type globResult struct {
	Files     []string `json:"files"`
	Truncated bool     `json:"truncated"`
}

const maxGlobFiles = 100

func cmdGlob(args []string) {
	if len(args) < 1 {
		writeError("usage: wickfs glob <pattern> [path]")
		return
	}

	pattern := args[0]
	searchPath := "."
	if len(args) > 1 && args[1] != "" {
		searchPath = args[1]
	}

	var files []string
	truncated := false

	filepath.WalkDir(searchPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if strings.HasPrefix(name, ".") || name == "node_modules" || name == "__pycache__" || name == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}

		name := d.Name()
		matched, _ := filepath.Match(pattern, name)
		if matched {
			files = append(files, path)
			if len(files) >= maxGlobFiles {
				truncated = true
				return filepath.SkipAll
			}
		}
		return nil
	})

	if files == nil {
		files = []string{}
	}
	writeOK(globResult{Files: files, Truncated: truncated})
}
