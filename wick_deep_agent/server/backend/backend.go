package backend

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
