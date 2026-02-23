package backend

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Backend is the interface for executing commands and transferring files.
type Backend interface {
	// ID returns the backend identifier.
	ID() string

	// Execute runs a shell command and returns the output.
	Execute(command string) ExecuteResponse

	// UploadFiles writes files to the backend.
	UploadFiles(files []FileUpload) []FileUploadResponse

	// DownloadFiles reads files from the backend.
	DownloadFiles(paths []string) []FileDownloadResponse

	// ContainerStatus returns the current container lifecycle status.
	// Returns "" for backends that don't have containers.
	ContainerStatus() string

	// ContainerError returns the error message when status is "error".
	ContainerError() string

	// Workdir returns the already-scoped working directory for this backend.
	Workdir() string

	// ResolvePath resolves a path relative to the workdir and validates
	// it does not escape the workspace boundary.
	ResolvePath(path string) (string, error)
}

// resolvePath is a shared helper that resolves a path relative to a workdir
// and ensures the result stays within the workdir boundary.
func resolvePath(workdir, path string) (string, error) {
	if path == "" {
		return workdir, nil
	}
	var resolved string
	if filepath.IsAbs(path) {
		resolved = filepath.Clean(path)
	} else {
		resolved = filepath.Clean(filepath.Join(workdir, path))
	}
	if resolved != workdir && !strings.HasPrefix(resolved, workdir+"/") {
		return "", fmt.Errorf("path %q is outside workspace %q", path, workdir)
	}
	return resolved, nil
}

// ExecuteResponse holds the result of a command execution.
type ExecuteResponse struct {
	Output    string `json:"output"`
	ExitCode  int    `json:"exit_code"`
	Truncated bool   `json:"truncated"`
}

// FileUpload is a file to upload (path + content).
type FileUpload struct {
	Path    string
	Content []byte
}

// FileUploadResponse is the result of a file upload.
type FileUploadResponse struct {
	Path  string `json:"path"`
	Error string `json:"error,omitempty"`
}

// FileDownloadResponse is the result of a file download.
type FileDownloadResponse struct {
	Path    string `json:"path"`
	Content []byte `json:"content,omitempty"`
	Error   string `json:"error,omitempty"`
}
