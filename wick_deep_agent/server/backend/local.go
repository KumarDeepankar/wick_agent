package backend

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"wick_server/wickfs"
)

// LocalBackend executes commands directly on the host machine via sh -c.
// Filesystem tool operations use wickfs.LocalFS (direct Go stdlib calls).
type LocalBackend struct {
	workdir        string
	timeout        time.Duration
	maxOutputBytes int
	fs             *wickfs.LocalFS
}

// NewLocalBackend creates a local backend that operates on the host filesystem.
func NewLocalBackend(workdir string, timeout float64, maxOutputBytes int) *LocalBackend {
	if workdir == "" {
		workdir, _ = os.Getwd()
		if workdir == "" {
			workdir = "/tmp/wick-workspace"
		}
	}
	// Ensure workdir is absolute
	if !filepath.IsAbs(workdir) {
		abs, err := filepath.Abs(workdir)
		if err == nil {
			workdir = abs
		}
	}
	if timeout == 0 {
		timeout = 120
	}
	if maxOutputBytes == 0 {
		maxOutputBytes = 100_000
	}

	// Ensure workdir exists
	os.MkdirAll(workdir, 0755)

	return &LocalBackend{
		workdir:        workdir,
		timeout:        time.Duration(timeout) * time.Second,
		maxOutputBytes: maxOutputBytes,
		fs:             wickfs.NewLocalFS(),
	}
}

func (b *LocalBackend) ID() string      { return "local" }
func (b *LocalBackend) Workdir() string { return b.workdir }

func (b *LocalBackend) ResolvePath(path string) (string, error) {
	return resolvePath(b.workdir, path)
}

func (b *LocalBackend) TerminalCmd() []string {
	return []string{"sh"}
}

// ContainerStatus returns "" — local backend has no container.
func (b *LocalBackend) ContainerStatus() string { return "" }

// ContainerError always returns "" — no container to fail.
func (b *LocalBackend) ContainerError() string { return "" }

// FS returns the local wickfs FileSystem (direct stdlib calls, zero overhead).
func (b *LocalBackend) FS() wickfs.FileSystem { return b.fs }

// Execute runs a command on the host via sh -c.
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

	return b.buildResponse(stdout.String(), stderr.String(), err, ctx)
}

// ExecuteWithStdin runs a command on the host with stdin piped in.
func (b *LocalBackend) ExecuteWithStdin(command string, stdin io.Reader) ExecuteResponse {
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
	cmd.Stdin = stdin

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	return b.buildResponse(stdout.String(), stderr.String(), err, ctx)
}

// buildResponse constructs an ExecuteResponse from command output.
func (b *LocalBackend) buildResponse(stdoutStr, stderrStr string, err error, ctx context.Context) ExecuteResponse {
	var parts []string
	if stdoutStr != "" {
		parts = append(parts, stdoutStr)
	}
	if stderrStr != "" {
		for _, line := range strings.Split(strings.TrimSpace(stderrStr), "\n") {
			parts = append(parts, "[stderr] "+line)
		}
	}

	output := "<no output>"
	if len(parts) > 0 {
		output = strings.Join(parts, "\n")
	}

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
		resolved, err := b.ResolvePath(f.Path)
		if err != nil {
			responses[i] = FileUploadResponse{Path: f.Path, Error: err.Error()}
			continue
		}

		// Ensure parent directory
		if err := os.MkdirAll(filepath.Dir(resolved), 0755); err != nil {
			responses[i] = FileUploadResponse{Path: resolved, Error: err.Error()}
			continue
		}

		if err := os.WriteFile(resolved, f.Content, 0666); err != nil {
			responses[i] = FileUploadResponse{Path: resolved, Error: "permission_denied"}
			continue
		}
		responses[i] = FileUploadResponse{Path: resolved}
	}

	return responses
}

// DownloadFiles reads files directly from the host filesystem.
func (b *LocalBackend) DownloadFiles(paths []string) []FileDownloadResponse {
	responses := make([]FileDownloadResponse, len(paths))

	for i, path := range paths {
		resolved, err := b.ResolvePath(path)
		if err != nil {
			responses[i] = FileDownloadResponse{Path: path, Error: err.Error()}
			continue
		}

		content, err := os.ReadFile(resolved)
		if err != nil {
			responses[i] = FileDownloadResponse{Path: resolved, Error: "file_not_found"}
			continue
		}
		responses[i] = FileDownloadResponse{Path: resolved, Content: content}
	}

	return responses
}
