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
)

// LocalBackend executes commands directly on the host machine via sh -c.
// Requires wickfs to be installed on the host for filesystem tool operations.
// Useful for local development without Docker.
type LocalBackend struct {
	workdir        string
	timeout        time.Duration
	maxOutputBytes int
	wickfsBinDir   string // directory containing wickfs binary, prepended to PATH
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

	// Find wickfs binary directory so we can add it to PATH
	wickfsBinDir := ""
	if bin := findHostWickfs(); bin != "" {
		wickfsBinDir = filepath.Dir(bin)
	}

	return &LocalBackend{
		workdir:        workdir,
		timeout:        time.Duration(timeout) * time.Second,
		maxOutputBytes: maxOutputBytes,
		wickfsBinDir:   wickfsBinDir,
	}
}

// findHostWickfs locates the wickfs binary on the host.
// Search order: WICKFS_BIN env → next to executable → ./bin/wickfs → PATH lookup.
func findHostWickfs() string {
	if v := os.Getenv("WICKFS_BIN"); v != "" {
		if _, err := os.Stat(v); err == nil {
			return v
		}
	}

	// Next to the server executable
	if ex, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(ex), "wickfs")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}

	// ./bin/wickfs (relative to CWD)
	if _, err := os.Stat("bin/wickfs"); err == nil {
		abs, _ := filepath.Abs("bin/wickfs")
		return abs
	}

	// Try PATH
	if p, err := exec.LookPath("wickfs"); err == nil {
		return p
	}

	return ""
}

func (b *LocalBackend) ID() string      { return "local" }
func (b *LocalBackend) Workdir() string { return b.workdir }

func (b *LocalBackend) ResolvePath(path string) (string, error) {
	return resolvePath(b.workdir, path)
}

func (b *LocalBackend) TerminalCmd() []string {
	return []string{"sh"}
}

// ContainerStatus always returns "launched" — no container to manage.
func (b *LocalBackend) ContainerStatus() string { return "launched" }

// ContainerError always returns "" — no container to fail.
func (b *LocalBackend) ContainerError() string { return "" }

// setupCmd configures a command with workdir and PATH including wickfs.
func (b *LocalBackend) setupCmd(cmd *exec.Cmd) {
	cmd.Dir = b.workdir
	if b.wickfsBinDir != "" {
		cmd.Env = append(os.Environ(), "PATH="+b.wickfsBinDir+":"+os.Getenv("PATH"))
	}
}

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
	b.setupCmd(cmd)

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
	b.setupCmd(cmd)
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
