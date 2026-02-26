package backend

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// DockerBackend executes commands inside a Docker container via docker exec.
// Supports local and remote Docker daemons.
type DockerBackend struct {
	containerName  string
	workdir        string
	timeout        time.Duration
	maxOutputBytes int
	dockerHost     string
	image          string
	username       string

	mu              sync.Mutex
	containerStatus string // idle | launching | launched | error
	containerError  string
	cancelLaunch    context.CancelFunc
	hasWickfs       bool
}

// NewDockerBackend creates a Docker backend.
func NewDockerBackend(containerName, workdir string, timeout float64, maxOutputBytes int, dockerHost, image, username string) *DockerBackend {
	if containerName == "" {
		containerName = "wick-skills-sandbox"
	}
	if workdir == "" {
		workdir = "/workspace"
	}
	if timeout == 0 {
		timeout = 120
	}
	if maxOutputBytes == 0 {
		maxOutputBytes = 100_000
	}
	if image == "" {
		image = "wick-sandbox"
	}

	return &DockerBackend{
		containerName:   containerName,
		workdir:         workdir,
		timeout:         time.Duration(timeout) * time.Second,
		maxOutputBytes:  maxOutputBytes,
		dockerHost:      dockerHost,
		image:           image,
		username:        username,
		containerStatus: "idle",
	}
}

func (b *DockerBackend) ID() string      { return b.containerName }
func (b *DockerBackend) Workdir() string { return b.workdir }

func (b *DockerBackend) ResolvePath(path string) (string, error) {
	return resolvePath(b.workdir, path)
}

func (b *DockerBackend) TerminalCmd() []string {
	return b.dockerCmd("exec", "-i",
		"-e", "TERM=xterm-256color",
		"-w", b.workdir,
		b.containerName,
		"sh",
	)
}

func (b *DockerBackend) ContainerStatus() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.containerStatus
}

func (b *DockerBackend) ContainerError() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.containerError
}

// SetContainerStatus sets the container status (for external launch coordination).
func (b *DockerBackend) SetContainerStatus(status, errMsg string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.containerStatus = status
	b.containerError = errMsg
}

// dockerCmd builds a docker CLI command, optionally targeting a remote host.
func (b *DockerBackend) dockerCmd(args ...string) []string {
	cmd := []string{"docker"}
	if b.dockerHost != "" {
		cmd = append(cmd, "-H", b.dockerHost)
	}
	cmd = append(cmd, args...)
	return cmd
}

// EnsureContainer checks if the container is running, launching if needed.
func (b *DockerBackend) EnsureContainer() error {
	maxRetries := 1
	if v := os.Getenv("SANDBOX_HEALTH_RETRIES"); v != "" {
		fmt.Sscanf(v, "%d", &maxRetries)
	}

	for attempt := 1; attempt <= maxRetries; attempt++ {
		cmd := b.dockerCmd("inspect", "--format", "{{.State.Running}}", b.containerName)
		result, err := exec.Command(cmd[0], cmd[1:]...).CombinedOutput()
		if err == nil && strings.Contains(strings.ToLower(string(result)), "true") {
			log.Printf("Docker sandbox container %q is running", b.containerName)
			return nil
		}

		if attempt < maxRetries {
			time.Sleep(2 * time.Second)
		}
	}

	// Container not running — launch on-demand
	target := b.dockerHost
	if target == "" {
		target = "local daemon"
	}
	log.Printf("Launching sandbox container %q on %s...", b.containerName, target)

	// Remove stale container
	rmCmd := b.dockerCmd("rm", "-f", b.containerName)
	exec.Command(rmCmd[0], rmCmd[1:]...).Run()

	// Create and start
	runCmd := b.dockerCmd("run", "-d",
		"--name", b.containerName,
		"-w", b.workdir,
		b.image,
		"sleep", "infinity",
	)
	out, err := exec.Command(runCmd[0], runCmd[1:]...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to launch container: %s: %w", string(out), err)
	}

	log.Printf("Sandbox container %q launched on %s", b.containerName, target)
	return nil
}

// LaunchContainerAsync launches the container in the background.
// Returns a channel that is closed when the launch completes.
func (b *DockerBackend) LaunchContainerAsync(onStatus func(status, username string)) {
	ctx, cancel := context.WithCancel(context.Background())
	b.mu.Lock()
	b.cancelLaunch = cancel
	b.containerStatus = "launching"
	b.containerError = ""
	b.mu.Unlock()

	if onStatus != nil {
		onStatus("container_status", b.username)
	}

	go func() {
		defer cancel()

		select {
		case <-ctx.Done():
			b.mu.Lock()
			b.containerStatus = "idle"
			b.mu.Unlock()
			return
		default:
		}

		err := b.EnsureContainer()

		// Ensure wickfs is available (pre-baked or injected)
		if err == nil {
			if wickfsErr := b.EnsureWickfs(); wickfsErr != nil {
				log.Printf("Warning: wickfs not available: %v (container will work with shell commands)", wickfsErr)
			}
		}

		b.mu.Lock()
		if err != nil {
			b.containerStatus = "error"
			b.containerError = err.Error()
		} else {
			b.containerStatus = "launched"
		}
		b.mu.Unlock()

		if onStatus != nil {
			onStatus("container_status", b.username)
		}
	}()
}

// CancelLaunch cancels any in-flight container launch.
func (b *DockerBackend) CancelLaunch() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.cancelLaunch != nil {
		b.cancelLaunch()
		b.cancelLaunch = nil
	}
}

// StopContainer stops and removes the container.
func (b *DockerBackend) StopContainer() {
	rmCmd := b.dockerCmd("rm", "-f", b.containerName)
	exec.Command(rmCmd[0], rmCmd[1:]...).Run()
	b.mu.Lock()
	b.containerStatus = "idle"
	b.containerError = ""
	b.mu.Unlock()
}

// waitForContainer blocks until the container is ready.
func (b *DockerBackend) waitForContainer() error {
	b.mu.Lock()
	status := b.containerStatus
	b.mu.Unlock()

	switch status {
	case "launched":
		return nil
	case "idle":
		if err := b.EnsureContainer(); err != nil {
			return err
		}
		b.mu.Lock()
		b.containerStatus = "launched"
		b.mu.Unlock()
		return nil
	case "launching":
		// Poll until launched or timeout (max 60s)
		for i := 0; i < 120; i++ {
			time.Sleep(500 * time.Millisecond)
			b.mu.Lock()
			s := b.containerStatus
			b.mu.Unlock()
			if s == "launched" {
				return nil
			}
			if s == "error" || s == "idle" {
				break
			}
		}
	}

	b.mu.Lock()
	errMsg := b.containerError
	b.mu.Unlock()
	return fmt.Errorf("container not available (status: %s). %s", status, errMsg)
}

// Execute runs a command inside the Docker container.
func (b *DockerBackend) Execute(command string) ExecuteResponse {
	if command == "" {
		return ExecuteResponse{
			Output:   "Error: Command must be a non-empty string.",
			ExitCode: 1,
		}
	}

	if err := b.waitForContainer(); err != nil {
		return ExecuteResponse{
			Output:   "Error: " + err.Error(),
			ExitCode: 1,
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), b.timeout)
	defer cancel()

	args := b.dockerCmd("exec", "-w", b.workdir, b.containerName, "sh", "-c", command)
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

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
				Output:   "Error executing command in container: " + err.Error(),
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

// HasWickfs returns true if wickfs has been injected into the container.
func (b *DockerBackend) HasWickfs() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.hasWickfs
}

// EnsureWickfs makes sure wickfs is available inside the container.
// First probes whether the image already has it (pre-baked), then falls back
// to docker cp injection from the host.
func (b *DockerBackend) EnsureWickfs() error {
	// Probe: is wickfs already in the container? (pre-baked image)
	probeArgs := b.dockerCmd("exec", b.containerName, "wickfs", "ls", "/")
	if out, err := exec.Command(probeArgs[0], probeArgs[1:]...).CombinedOutput(); err == nil {
		// Verify it returned valid JSON
		if strings.Contains(string(out), `"ok"`) {
			log.Printf("wickfs already present in container %q (pre-baked image)", b.containerName)
			b.mu.Lock()
			b.hasWickfs = true
			b.mu.Unlock()
			return nil
		}
	}

	// Not pre-baked — fall back to docker cp injection
	return b.injectWickfsFromHost()
}

// injectWickfsFromHost copies the wickfs binary from the host into the container.
func (b *DockerBackend) injectWickfsFromHost() error {
	bin := findWickfsBinary()
	if bin == "" {
		return fmt.Errorf("wickfs binary not found (set WICKFS_BIN or place in ./bin/)")
	}

	// docker cp <src> <container>:/usr/local/bin/wickfs
	dest := b.containerName + ":/usr/local/bin/wickfs"
	args := b.dockerCmd("cp", bin, dest)
	out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to inject wickfs: %s: %w", strings.TrimSpace(string(out)), err)
	}

	// Make it executable
	chmodArgs := b.dockerCmd("exec", b.containerName, "chmod", "+x", "/usr/local/bin/wickfs")
	out, err = exec.Command(chmodArgs[0], chmodArgs[1:]...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to chmod wickfs: %s: %w", strings.TrimSpace(string(out)), err)
	}

	log.Printf("wickfs injected into container %q from %s", b.containerName, bin)
	b.mu.Lock()
	b.hasWickfs = true
	b.mu.Unlock()
	return nil
}

// findWickfsBinary searches for the wickfs binary in known locations.
// Search order:
//  1. WICKFS_BIN env var
//  2. /usr/local/bin/wickfs (containerized deployment)
//  3. Next to the wick_go executable (e.g. /usr/local/bin/wickfs)
//  4. ./bin/wickfs_linux_{arch} (dev / wheel builds)
func findWickfsBinary() string {
	// 1. WICKFS_BIN env var
	if v := os.Getenv("WICKFS_BIN"); v != "" {
		if _, err := os.Stat(v); err == nil {
			return v
		}
	}

	arch := runtime.GOARCH
	candidates := []string{
		// 2. Well-known container path
		"/usr/local/bin/wickfs",
	}

	// 3. Next to executable
	if ex, err := os.Executable(); err == nil {
		dir := filepath.Dir(ex)
		candidates = append(candidates,
			filepath.Join(dir, "wickfs"),
			filepath.Join(dir, fmt.Sprintf("wickfs_linux_%s", arch)),
		)
	}

	// 4. Relative to CWD (dev builds / wheel bundles)
	candidates = append(candidates,
		fmt.Sprintf("bin/wickfs_linux_%s", arch),
		fmt.Sprintf("./bin/wickfs_linux_%s", arch),
	)

	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}

	return ""
}

// ExecuteWithStdin runs a command inside the Docker container with stdin piped in.
func (b *DockerBackend) ExecuteWithStdin(command string, stdin io.Reader) ExecuteResponse {
	if command == "" {
		return ExecuteResponse{
			Output:   "Error: Command must be a non-empty string.",
			ExitCode: 1,
		}
	}

	if err := b.waitForContainer(); err != nil {
		return ExecuteResponse{
			Output:   "Error: " + err.Error(),
			ExitCode: 1,
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), b.timeout)
	defer cancel()

	args := b.dockerCmd("exec", "-i", "-w", b.workdir, b.containerName, "sh", "-c", command)
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Stdin = stdin

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

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
				Output:   "Error executing command in container: " + err.Error(),
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

// UploadFiles uploads files to the container via docker exec + base64.
func (b *DockerBackend) UploadFiles(files []FileUpload) []FileUploadResponse {
	b.waitForContainer()
	responses := make([]FileUploadResponse, len(files))

	for i, f := range files {
		resolved, err := b.ResolvePath(f.Path)
		if err != nil {
			responses[i] = FileUploadResponse{Path: f.Path, Error: err.Error()}
			continue
		}

		// Ensure parent directory
		parent := filepath.Dir(resolved)
		mkdirCmd := b.dockerCmd("exec", b.containerName, "mkdir", "-p", parent)
		exec.Command(mkdirCmd[0], mkdirCmd[1:]...).Run()

		// Pipe base64-encoded content
		b64 := base64.StdEncoding.EncodeToString(f.Content)
		decodeCmd := b.dockerCmd("exec", "-i", b.containerName,
			"sh", "-c", fmt.Sprintf("base64 -d > '%s' && chmod 666 '%s'", resolved, resolved))
		cmd := exec.Command(decodeCmd[0], decodeCmd[1:]...)
		cmd.Stdin = strings.NewReader(b64)

		if err := cmd.Run(); err != nil {
			responses[i] = FileUploadResponse{Path: resolved, Error: "permission_denied"}
			continue
		}
		responses[i] = FileUploadResponse{Path: resolved}
	}

	return responses
}

// DownloadFiles downloads files from the container via docker exec + base64.
func (b *DockerBackend) DownloadFiles(paths []string) []FileDownloadResponse {
	b.waitForContainer()
	responses := make([]FileDownloadResponse, len(paths))

	for i, path := range paths {
		resolved, err := b.ResolvePath(path)
		if err != nil {
			responses[i] = FileDownloadResponse{Path: path, Error: err.Error()}
			continue
		}

		cmd := b.dockerCmd("exec", b.containerName,
			"sh", "-c", fmt.Sprintf("base64 '%s'", resolved))
		out, err := exec.Command(cmd[0], cmd[1:]...).Output()
		if err != nil {
			responses[i] = FileDownloadResponse{Path: resolved, Error: "file_not_found"}
			continue
		}

		content, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(out)))
		if err != nil {
			responses[i] = FileDownloadResponse{Path: resolved, Error: "decode_error"}
			continue
		}
		responses[i] = FileDownloadResponse{Path: resolved, Content: content}
	}

	return responses
}
