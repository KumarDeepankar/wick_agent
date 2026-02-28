// Package wickfs provides a programmatic filesystem API for workspace operations.
//
// Two implementations are provided:
//   - LocalFS: direct Go stdlib calls (os.ReadDir, os.ReadFile, etc.) with zero overhead.
//   - RemoteFS: builds wickfs CLI command strings and delegates to an Executor (e.g. docker exec).
package wickfs

import "context"

// FileSystem is the interface for workspace filesystem operations.
type FileSystem interface {
	Ls(ctx context.Context, path string) ([]DirEntry, error)
	ReadFile(ctx context.Context, path string) (string, error)
	WriteFile(ctx context.Context, path, content string) (*WriteResult, error)
	EditFile(ctx context.Context, path, oldText, newText string) (*EditResult, error)
	Grep(ctx context.Context, pattern, path string) (*GrepResult, error)
	Glob(ctx context.Context, pattern, path string) (*GlobResult, error)
	Exec(ctx context.Context, command string) (*ExecResult, error)
}

// DirEntry represents a single directory listing entry.
type DirEntry struct {
	Name string `json:"name"`
	Type string `json:"type"` // "file", "dir", or "symlink"
	Size int64  `json:"size"`
}

// WriteResult is returned by WriteFile.
type WriteResult struct {
	Path         string `json:"path"`
	BytesWritten int    `json:"bytes_written"`
}

// EditResult is returned by EditFile.
type EditResult struct {
	Path         string `json:"path"`
	Replacements int    `json:"replacements"`
}

// GrepMatch is a single grep hit.
type GrepMatch struct {
	File string `json:"file"`
	Line int    `json:"line"`
	Text string `json:"text"`
}

// GrepResult is returned by Grep.
type GrepResult struct {
	Matches   []GrepMatch `json:"matches"`
	Truncated bool        `json:"truncated"`
}

// GlobResult is returned by Glob.
type GlobResult struct {
	Files     []string `json:"files"`
	Truncated bool     `json:"truncated"`
}

// ExecResult is returned by Exec.
type ExecResult struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
}
