package wickfs

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

// LocalFS implements FileSystem using direct Go stdlib calls.
// Zero process spawning, no shell, no JSON serialization overhead.
type LocalFS struct{}

// NewLocalFS creates a new local filesystem.
func NewLocalFS() *LocalFS {
	return &LocalFS{}
}

// Ls lists directory entries at path.
func (fs *LocalFS) Ls(_ context.Context, path string) ([]DirEntry, error) {
	if path == "" {
		path = "."
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}

	result := make([]DirEntry, 0, len(entries))
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		typ := "file"
		if e.IsDir() {
			typ = "dir"
		} else if info.Mode()&os.ModeSymlink != 0 {
			typ = "symlink"
		}
		result = append(result, DirEntry{
			Name: e.Name(),
			Type: typ,
			Size: info.Size(),
		})
	}
	return result, nil
}

// ReadFile reads a file and returns its contents as a string.
// Binary content is base64-encoded with a "base64:" prefix.
func (fs *LocalFS) ReadFile(_ context.Context, path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if utf8.Valid(data) {
		return string(data), nil
	}
	return "base64:" + base64.StdEncoding.EncodeToString(data), nil
}

// WriteFile atomically writes content to path, creating parent directories as needed.
func (fs *LocalFS) WriteFile(_ context.Context, path, content string) (*WriteResult, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create directory: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".wickfs-tmp-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpName := tmp.Name()

	_, err = tmp.WriteString(content)
	tmp.Close()
	if err != nil {
		os.Remove(tmpName)
		return nil, fmt.Errorf("failed to write: %w", err)
	}

	if err := os.Chmod(tmpName, 0666); err != nil {
		os.Remove(tmpName)
		return nil, fmt.Errorf("failed to set permissions: %w", err)
	}

	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return nil, fmt.Errorf("failed to rename: %w", err)
	}

	return &WriteResult{Path: path, BytesWritten: len(content)}, nil
}

// EditFile replaces the first occurrence of oldText with newText in the file at path.
func (fs *LocalFS) EditFile(_ context.Context, path, oldText, newText string) (*EditResult, error) {
	if oldText == "" {
		return nil, fmt.Errorf("old_text must not be empty")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	original := string(data)
	if !strings.Contains(original, oldText) {
		return nil, fmt.Errorf("old_text not found in file")
	}

	updated := strings.Replace(original, oldText, newText, 1)

	// Atomic write
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".wickfs-tmp-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpName := tmp.Name()

	_, err = tmp.WriteString(updated)
	tmp.Close()
	if err != nil {
		os.Remove(tmpName)
		return nil, fmt.Errorf("failed to write: %w", err)
	}

	if err := os.Chmod(tmpName, 0666); err != nil {
		os.Remove(tmpName)
		return nil, fmt.Errorf("failed to set permissions: %w", err)
	}

	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return nil, fmt.Errorf("failed to rename: %w", err)
	}

	return &EditResult{Path: path, Replacements: 1}, nil
}

const maxGrepMatches = 200

// Grep searches for a regex pattern in files under the given path.
func (fs *LocalFS) Grep(_ context.Context, pattern, path string) (*GrepResult, error) {
	if path == "" {
		path = "."
	}

	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid regex: %w", err)
	}

	var matches []GrepMatch
	truncated := false

	filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
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
		if truncated {
			return filepath.SkipAll
		}

		ext := strings.ToLower(filepath.Ext(p))
		if isBinaryExt(ext) {
			return nil
		}

		f, err := os.Open(p)
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
				matches = append(matches, GrepMatch{
					File: p,
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
		matches = []GrepMatch{}
	}
	return &GrepResult{Matches: matches, Truncated: truncated}, nil
}

const maxGlobFiles = 100

// Glob finds files matching a glob pattern under the given path.
func (fs *LocalFS) Glob(_ context.Context, pattern, path string) (*GlobResult, error) {
	if path == "" {
		path = "."
	}

	var files []string
	truncated := false

	filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
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
			files = append(files, p)
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
	return &GlobResult{Files: files, Truncated: truncated}, nil
}

// Exec runs a shell command via sh -c.
func (fs *LocalFS) Exec(_ context.Context, command string) (*ExecResult, error) {
	cmd := exec.Command("sh", "-c", command)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return nil, fmt.Errorf("failed to execute: %w", err)
		}
	}

	return &ExecResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exitCode,
	}, nil
}

// isBinaryExt returns true for file extensions that should be skipped during grep.
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

// skipDirs is used by both Grep and Glob — exported for cmd/wickfs if needed.
var skipDirs = map[string]bool{
	"node_modules": true,
	"__pycache__":  true,
	"vendor":       true,
}

// editInput is used internally for JSON marshaling in RemoteFS.
type editInput struct {
	OldText string `json:"old_text"`
	NewText string `json:"new_text"`
}

// marshalEditInput creates the JSON payload for an edit command.
func marshalEditInput(oldText, newText string) string {
	data, _ := json.Marshal(editInput{OldText: oldText, NewText: newText})
	return string(data)
}
