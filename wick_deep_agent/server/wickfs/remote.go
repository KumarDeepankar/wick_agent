package wickfs

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Executor abstracts command execution on a remote target (e.g. docker exec).
type Executor interface {
	Run(ctx context.Context, command string) (stdout string, exitCode int, err error)
	RunWithStdin(ctx context.Context, command, stdin string) (stdout string, exitCode int, err error)
}

// RemoteFS implements FileSystem by building wickfs CLI command strings
// and delegating execution to an Executor.
type RemoteFS struct {
	exec Executor
}

// NewRemoteFS creates a remote filesystem backed by the given executor.
func NewRemoteFS(exec Executor) *RemoteFS {
	return &RemoteFS{exec: exec}
}

func (fs *RemoteFS) Ls(ctx context.Context, path string) ([]DirEntry, error) {
	cmd := "wickfs ls " + shellQuote(path)
	out, _, err := fs.exec.Run(ctx, cmd)
	if err != nil {
		return nil, err
	}
	resp, err := ParseWickfsResponse(out)
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, fmt.Errorf("%s", resp.Error)
	}
	var entries []DirEntry
	if err := json.Unmarshal(resp.Data, &entries); err != nil {
		return nil, fmt.Errorf("failed to parse ls data: %w", err)
	}
	return entries, nil
}

func (fs *RemoteFS) ReadFile(ctx context.Context, path string) (string, error) {
	cmd := "wickfs read " + shellQuote(path)
	out, _, err := fs.exec.Run(ctx, cmd)
	if err != nil {
		return "", err
	}
	resp, err := ParseWickfsResponse(out)
	if err != nil {
		return "", err
	}
	if !resp.OK {
		return "", fmt.Errorf("%s", resp.Error)
	}
	var content string
	if err := json.Unmarshal(resp.Data, &content); err != nil {
		return "", fmt.Errorf("failed to parse read data: %w", err)
	}
	return content, nil
}

func (fs *RemoteFS) WriteFile(ctx context.Context, path, content string) (*WriteResult, error) {
	cmd := "wickfs write " + shellQuote(path)
	out, _, err := fs.exec.RunWithStdin(ctx, cmd, content)
	if err != nil {
		return nil, err
	}
	resp, err := ParseWickfsResponse(out)
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, fmt.Errorf("%s", resp.Error)
	}
	var result WriteResult
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return nil, fmt.Errorf("failed to parse write data: %w", err)
	}
	return &result, nil
}

func (fs *RemoteFS) EditFile(ctx context.Context, path, oldText, newText string) (*EditResult, error) {
	cmd := "wickfs edit " + shellQuote(path)
	stdin := marshalEditInput(oldText, newText)
	out, _, err := fs.exec.RunWithStdin(ctx, cmd, stdin)
	if err != nil {
		return nil, err
	}
	resp, err := ParseWickfsResponse(out)
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, fmt.Errorf("%s", resp.Error)
	}
	var result EditResult
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return nil, fmt.Errorf("failed to parse edit data: %w", err)
	}
	return &result, nil
}

func (fs *RemoteFS) Grep(ctx context.Context, pattern, path string) (*GrepResult, error) {
	cmd := "wickfs grep " + shellQuote(pattern) + " " + shellQuote(path)
	out, _, err := fs.exec.Run(ctx, cmd)
	if err != nil {
		return nil, err
	}
	resp, err := ParseWickfsResponse(out)
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, fmt.Errorf("%s", resp.Error)
	}
	var result GrepResult
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return nil, fmt.Errorf("failed to parse grep data: %w", err)
	}
	return &result, nil
}

func (fs *RemoteFS) Glob(ctx context.Context, pattern, path string) (*GlobResult, error) {
	cmd := "wickfs glob " + shellQuote(pattern) + " " + shellQuote(path)
	out, _, err := fs.exec.Run(ctx, cmd)
	if err != nil {
		return nil, err
	}
	resp, err := ParseWickfsResponse(out)
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, fmt.Errorf("%s", resp.Error)
	}
	var result GlobResult
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return nil, fmt.Errorf("failed to parse glob data: %w", err)
	}
	return &result, nil
}

func (fs *RemoteFS) Exec(ctx context.Context, command string) (*ExecResult, error) {
	cmd := "wickfs exec " + shellQuote(command)
	out, _, err := fs.exec.Run(ctx, cmd)
	if err != nil {
		return nil, err
	}
	resp, err := ParseWickfsResponse(out)
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, fmt.Errorf("%s", resp.Error)
	}
	var result ExecResult
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return nil, fmt.Errorf("failed to parse exec data: %w", err)
	}
	return &result, nil
}

// ── Shell quoting + response parsing (moved from backend/sandbox.go) ─────────

// ShellQuote safely quotes a string for shell use.
func ShellQuote(s string) string {
	return shellQuote(s)
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// WickfsResponse is the JSON envelope returned by wickfs commands.
type WickfsResponse struct {
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data"`
	Error string          `json:"error"`
}

// ParseWickfsResponse parses the JSON output of a wickfs command.
// It first tries the full output, then falls back to extracting the first
// line that looks like JSON (starts with '{'), to handle cases where
// stderr or other noise gets mixed into the output.
func ParseWickfsResponse(output string) (WickfsResponse, error) {
	output = strings.TrimSpace(output)
	var resp WickfsResponse

	// Fast path: entire output is valid JSON
	if err := json.Unmarshal([]byte(output), &resp); err == nil {
		return resp, nil
	}

	// Fallback: scan lines for the JSON envelope
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		if err := json.Unmarshal([]byte(line), &resp); err == nil {
			return resp, nil
		}
	}

	return resp, fmt.Errorf("failed to parse wickfs response (raw: %s)", truncate(output, 200))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
