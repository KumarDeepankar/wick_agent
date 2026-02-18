package backend

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// LocalBackend executes commands directly on the host machine via sh -c.
// Per-user workdir isolation: /workspace/{username}
type LocalBackend struct {
	workdir        string
	timeout        time.Duration
	maxOutputBytes int
	username       string
}

// NewLocalBackend creates a local backend with per-user workdir scoping.
func NewLocalBackend(workdir string, timeout float64, maxOutputBytes int, username string) *LocalBackend {
	if timeout == 0 {
		timeout = 120
	}
	if maxOutputBytes == 0 {
		maxOutputBytes = 100_000
	}

	// Scope workdir per user
	scopedWorkdir := filepath.Join(workdir, username)
	os.MkdirAll(scopedWorkdir, 0755)

	log.Printf("Local sandbox backend ready (workdir=%s, user=%s)", scopedWorkdir, username)

	return &LocalBackend{
		workdir:        scopedWorkdir,
		timeout:        time.Duration(timeout) * time.Second,
		maxOutputBytes: maxOutputBytes,
		username:       username,
	}
}

func (b *LocalBackend) ID() string             { return "local" }
func (b *LocalBackend) ContainerStatus() string { return "launched" } // always ready
func (b *LocalBackend) ContainerError() string  { return "" }

// Execute runs a command via sh -c in the workdir.
func (b *LocalBackend) Execute(command string) ExecuteResponse {
	if command == "" {
		return ExecuteResponse{
			Output:   "Error: Command must be a non-empty string.",
			ExitCode: 1,
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), b.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = b.workdir

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	// Build output (same pattern as Python)
	var parts []string
	if stdout.Len() > 0 {
		parts = append(parts, stdout.String())
	}
	if stderr.Len() > 0 {
		for _, line := range strings.Split(strings.TrimSpace(stderr.String()), "\n") {
			parts = append(parts, "[stderr] "+line)
		}
	}

	output := "<no output>"
	if len(parts) > 0 {
		output = strings.Join(parts, "\n")
	}

	// Truncate if needed
	truncated := false
	if len(output) > b.maxOutputBytes {
		output = output[:b.maxOutputBytes]
		output += fmt.Sprintf("\n\n... Output truncated at %d bytes.", b.maxOutputBytes)
		truncated = true
	}

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else if ctx.Err() != nil {
			return ExecuteResponse{
				Output:   fmt.Sprintf("Error: Command timed out after %.1f seconds.", b.timeout.Seconds()),
				ExitCode: 124,
			}
		} else {
			return ExecuteResponse{
				Output:   "Error executing command: " + err.Error(),
				ExitCode: 1,
			}
		}
	}

	if exitCode != 0 {
		output = strings.TrimRight(output, "\n") + fmt.Sprintf("\n\nExit code: %d", exitCode)
	}

	return ExecuteResponse{
		Output:    output,
		ExitCode:  exitCode,
		Truncated: truncated,
	}
}

// UploadFiles writes files directly to the host filesystem.
func (b *LocalBackend) UploadFiles(files []FileUpload) []FileUploadResponse {
	responses := make([]FileUploadResponse, len(files))
	for i, f := range files {
		dir := filepath.Dir(f.Path)
		if err := os.MkdirAll(dir, 0755); err != nil {
			responses[i] = FileUploadResponse{Path: f.Path, Error: "permission_denied"}
			continue
		}
		if err := os.WriteFile(f.Path, f.Content, 0644); err != nil {
			responses[i] = FileUploadResponse{Path: f.Path, Error: "permission_denied"}
			continue
		}
		responses[i] = FileUploadResponse{Path: f.Path}
	}
	return responses
}

// DownloadFiles reads files directly from the host filesystem.
func (b *LocalBackend) DownloadFiles(paths []string) []FileDownloadResponse {
	responses := make([]FileDownloadResponse, len(paths))
	for i, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				responses[i] = FileDownloadResponse{Path: path, Error: "file_not_found"}
			} else {
				responses[i] = FileDownloadResponse{Path: path, Error: "permission_denied"}
			}
			continue
		}
		responses[i] = FileDownloadResponse{Path: path, Content: data}
	}
	return responses
}
