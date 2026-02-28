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

	"wick_server/wickfs"
)

// DockerBackend executes commands inside a Docker container.
// When the wick-daemon is available inside the container, commands are sent
// over a direct TCP/Unix socket connection (zero docker-exec overhead).
// Falls back to docker exec if the daemon is not reachable.
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
	hasDaemon       bool

	// remoteFS is the fallback RemoteFS that uses docker exec.
	remoteFS *wickfs.RemoteFS

	// daemonClient is the persistent connection to the in-container wick-daemon.
	// nil when daemon is not available (falls back to docker exec).
	daemonClient *DaemonClient

	// daemonFS is a RemoteFS backed by the daemon (fast path).
	daemonFS *wickfs.RemoteFS
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

	db := &DockerBackend{
		containerName:   containerName,
		workdir:         workdir,
		timeout:         time.Duration(timeout) * time.Second,
		maxOutputBytes:  maxOutputBytes,
		dockerHost:      dockerHost,
		image:           image,
		username:        username,
		containerStatus: "idle",
	}
	db.remoteFS = wickfs.NewRemoteFS(&DockerExecutor{backend: db})
	return db
}

// DockerExecutor implements wickfs.Executor by running commands inside a Docker container.
type DockerExecutor struct {
	backend *DockerBackend
}

func (e *DockerExecutor) Run(ctx context.Context, command string) (string, int, error) {
	resp := e.backend.Execute(command)
	return resp.Output, resp.ExitCode, nil
}

func (e *DockerExecutor) RunWithStdin(ctx context.Context, command, stdin string) (string, int, error) {
	resp := e.backend.ExecuteWithStdin(command, strings.NewReader(stdin))
	return resp.Output, resp.ExitCode, nil
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

// FS returns the wickfs FileSystem. Prefers the daemon-backed fast path;
// falls back to docker-exec-based RemoteFS.
func (b *DockerBackend) FS() wickfs.FileSystem {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.daemonFS != nil {
		return b.daemonFS
	}
	return b.remoteFS
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

	// Create and start — try to launch with wick-daemon as the entrypoint.
	// The daemon keeps the container alive and provides a fast execution channel.
	// If the image doesn't have wick-daemon, fall back to "sleep infinity".
	runCmd := b.dockerCmd("run", "-d",
		"--name", b.containerName,
		"-w", b.workdir,
		b.image,
		"sh", "-c",
		// Try wick-daemon first; if not found, fall back to sleep.
		"if command -v wick-daemon >/dev/null 2>&1; then exec wick-daemon; else exec sleep infinity; fi",
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

		if err == nil {
			// Try to start/connect to wick-daemon (fast path)
			if daemonErr := b.EnsureDaemon(); daemonErr != nil {
				log.Printf("wick-daemon not available: %v (falling back to docker exec)", daemonErr)

				// Fallback: ensure wickfs CLI is available for docker-exec path
				if wickfsErr := b.EnsureWickfs(); wickfsErr != nil {
					log.Printf("Warning: wickfs not available: %v (container will work with shell commands)", wickfsErr)
				}
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

// StopContainer stops and removes the container, closing the daemon connection.
func (b *DockerBackend) StopContainer() {
	// Close daemon connection first
	b.mu.Lock()
	if b.daemonClient != nil {
		b.daemonClient.Close()
		b.daemonClient = nil
		b.daemonFS = nil
		b.hasDaemon = false
	}
	b.mu.Unlock()

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
// Uses the wick-daemon fast path when available, otherwise falls back to docker exec.
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

	// Fast path: use daemon
	if client := b.getDaemonClient(); client != nil {
		return b.execViaDaemon(client, command, "")
	}

	// Fallback: docker exec
	ctx, cancel := context.WithTimeout(context.Background(), b.timeout)
	defer cancel()

	args := b.dockerCmd("exec", "-w", b.workdir, b.containerName, "sh", "-c", command)
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	return b.buildExecResponse(stdout.String(), stderr.String(), err, ctx)
}

// buildExecResponse assembles an ExecuteResponse from stdout/stderr.
// Stdout is returned as the primary output. Stderr is only appended when stdout
// is empty (command produced no stdout) or when the command failed, so that
// structured JSON output from wickfs is never polluted with stderr noise.
func (b *DockerBackend) buildExecResponse(stdoutStr, stderrStr string, err error, ctx context.Context) ExecuteResponse {
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

	// Use stdout as primary output. Only mix in stderr when stdout is
	// empty (no output at all) or the command failed — this keeps wickfs
	// JSON responses clean.
	output := stdoutStr
	if output == "" && stderrStr != "" {
		output = stderrStr
	} else if exitCode != 0 && stderrStr != "" {
		output = strings.TrimRight(output, "\n") + "\n" + stderrStr
	}

	if output == "" {
		output = "<no output>"
	}

	truncated := false
	if len(output) > b.maxOutputBytes {
		output = output[:b.maxOutputBytes]
		output += fmt.Sprintf("\n\n... Output truncated at %d bytes.", b.maxOutputBytes)
		truncated = true
	}

	if exitCode != 0 {
		output = strings.TrimRight(output, "\n") + fmt.Sprintf("\n\nExit code: %d", exitCode)
	}

	// Log stderr for debugging if it was suppressed from output
	if stderrStr != "" && exitCode == 0 && stdoutStr != "" {
		log.Printf("[docker-exec] stderr (suppressed from output): %s", strings.TrimSpace(stderrStr))
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

	// Fast path: use daemon
	if client := b.getDaemonClient(); client != nil {
		stdinBytes, _ := io.ReadAll(stdin)
		return b.execViaDaemon(client, command, string(stdinBytes))
	}

	// Fallback: docker exec
	ctx, cancel := context.WithTimeout(context.Background(), b.timeout)
	defer cancel()

	args := b.dockerCmd("exec", "-i", "-w", b.workdir, b.containerName, "sh", "-c", command)
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Stdin = stdin

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	return b.buildExecResponse(stdout.String(), stderr.String(), err, ctx)
}

// ── Daemon integration ───────────────────────────────────────────────────────

// getDaemonClient returns the daemon client if connected, nil otherwise.
func (b *DockerBackend) getDaemonClient() *DaemonClient {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.daemonClient != nil && b.daemonClient.Alive() {
		return b.daemonClient
	}
	return nil
}

// execViaDaemon sends a command to the wick-daemon and builds an ExecuteResponse.
func (b *DockerBackend) execViaDaemon(client *DaemonClient, command, stdin string) ExecuteResponse {
	ctx, cancel := context.WithTimeout(context.Background(), b.timeout)
	defer cancel()

	timeoutSecs := int(b.timeout.Seconds())
	resp, err := client.Exec(ctx, command, b.workdir, stdin, timeoutSecs)
	if err != nil {
		// Daemon connection broken — clear it so we fall back to docker exec
		log.Printf("Daemon exec failed: %v (falling back to docker exec)", err)
		b.mu.Lock()
		b.daemonClient = nil
		b.daemonFS = nil
		b.hasDaemon = false
		b.mu.Unlock()
		return ExecuteResponse{
			Output:   "Error: daemon connection lost: " + err.Error(),
			ExitCode: 1,
		}
	}

	if resp.Error != "" {
		return ExecuteResponse{
			Output:   resp.Error,
			ExitCode: resp.ExitCode,
		}
	}

	// Build output following same rules as buildExecResponse
	output := resp.Stdout
	if output == "" && resp.Stderr != "" {
		output = resp.Stderr
	} else if resp.ExitCode != 0 && resp.Stderr != "" {
		output = strings.TrimRight(output, "\n") + "\n" + resp.Stderr
	}

	if output == "" {
		output = "<no output>"
	}

	truncated := false
	if len(output) > b.maxOutputBytes {
		output = output[:b.maxOutputBytes]
		output += fmt.Sprintf("\n\n... Output truncated at %d bytes.", b.maxOutputBytes)
		truncated = true
	}

	if resp.ExitCode != 0 {
		output = strings.TrimRight(output, "\n") + fmt.Sprintf("\n\nExit code: %d", resp.ExitCode)
	}

	// Log suppressed stderr
	if resp.Stderr != "" && resp.ExitCode == 0 && resp.Stdout != "" {
		log.Printf("[daemon-exec] stderr (suppressed): %s", strings.TrimSpace(resp.Stderr))
	}

	return ExecuteResponse{
		Output:    output,
		ExitCode:  resp.ExitCode,
		Truncated: truncated,
	}
}

// EnsureDaemon ensures the wick-daemon is running inside the container and
// establishes a connection to it. Steps:
//  1. Check if daemon is already reachable (pre-baked image)
//  2. If not, inject the daemon binary and start it
//  3. Connect via TCP using the container's IP
func (b *DockerBackend) EnsureDaemon() error {
	// Get container IP for TCP connection
	ip := b.getContainerIP()
	if ip == "" {
		return fmt.Errorf("could not determine container IP")
	}

	// Probe: is daemon already running? (pre-baked image with wick-daemon as entrypoint)
	client := probeDaemon(ip, "")
	if client != nil {
		b.setDaemonClient(client)
		return nil
	}

	// Daemon not running — inject and start it
	if err := b.injectAndStartDaemon(); err != nil {
		return err
	}

	// Wait for daemon to become ready (up to 5s)
	for i := 0; i < 10; i++ {
		time.Sleep(500 * time.Millisecond)
		client = probeDaemon(ip, "")
		if client != nil {
			b.setDaemonClient(client)
			return nil
		}
	}

	return fmt.Errorf("wick-daemon injected but not reachable at %s:%s", ip, DaemonPort)
}

// setDaemonClient stores the daemon client and creates a daemon-backed RemoteFS.
func (b *DockerBackend) setDaemonClient(client *DaemonClient) {
	timeoutSecs := int(b.timeout.Seconds())
	executor := NewDaemonExecutor(client, b.workdir, timeoutSecs)

	b.mu.Lock()
	b.daemonClient = client
	b.daemonFS = wickfs.NewRemoteFS(executor)
	b.hasDaemon = true
	b.mu.Unlock()

	log.Printf("wick-daemon connected for container %q (fast path enabled)", b.containerName)
}

// injectAndStartDaemon copies the wick-daemon binary into the container and starts it.
func (b *DockerBackend) injectAndStartDaemon() error {
	bin := findDaemonBinary()
	if bin == "" {
		return fmt.Errorf("wick-daemon binary not found (set WICKDAEMON_BIN or place in ./bin/)")
	}

	// docker cp <src> <container>:/usr/local/bin/wick-daemon
	dest := b.containerName + ":/usr/local/bin/wick-daemon"
	cpArgs := b.dockerCmd("cp", bin, dest)
	out, err := exec.Command(cpArgs[0], cpArgs[1:]...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to inject wick-daemon: %s: %w", strings.TrimSpace(string(out)), err)
	}

	// chmod +x
	chmodArgs := b.dockerCmd("exec", b.containerName, "chmod", "+x", "/usr/local/bin/wick-daemon")
	exec.Command(chmodArgs[0], chmodArgs[1:]...).CombinedOutput()

	// Start daemon in background inside the container
	startArgs := b.dockerCmd("exec", "-d", b.containerName, "/usr/local/bin/wick-daemon")
	out, err = exec.Command(startArgs[0], startArgs[1:]...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to start wick-daemon: %s: %w", strings.TrimSpace(string(out)), err)
	}

	log.Printf("wick-daemon injected and started in container %q", b.containerName)
	return nil
}

// getContainerIP returns the container's IP address on the Docker bridge network.
func (b *DockerBackend) getContainerIP() string {
	args := b.dockerCmd("inspect", "--format", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}", b.containerName)
	out, err := exec.Command(args[0], args[1:]...).Output()
	if err != nil {
		return ""
	}
	ip := strings.TrimSpace(string(out))
	if ip == "" || ip == "<no value>" {
		return ""
	}
	return ip
}

// HasDaemon returns true if the daemon fast path is active.
func (b *DockerBackend) HasDaemon() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.hasDaemon
}

// findDaemonBinary searches for the wick-daemon binary in known locations.
func findDaemonBinary() string {
	// 1. WICKDAEMON_BIN env var
	if v := os.Getenv("WICKDAEMON_BIN"); v != "" {
		if _, err := os.Stat(v); err == nil {
			return v
		}
	}

	arch := runtime.GOARCH
	candidates := []string{
		"/usr/local/bin/wick-daemon",
	}

	// Next to executable
	if ex, err := os.Executable(); err == nil {
		dir := filepath.Dir(ex)
		candidates = append(candidates,
			filepath.Join(dir, "wick-daemon"),
			filepath.Join(dir, fmt.Sprintf("wick-daemon_linux_%s", arch)),
		)
	}

	// Relative to CWD
	candidates = append(candidates,
		fmt.Sprintf("bin/wick-daemon_linux_%s", arch),
		fmt.Sprintf("./bin/wick-daemon_linux_%s", arch),
	)

	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}

	return ""
}

// UploadFiles uploads files to the container.
// Uses daemon when available, otherwise falls back to docker exec + base64.
func (b *DockerBackend) UploadFiles(files []FileUpload) []FileUploadResponse {
	b.waitForContainer()
	responses := make([]FileUploadResponse, len(files))

	client := b.getDaemonClient()

	for i, f := range files {
		resolved, err := b.ResolvePath(f.Path)
		if err != nil {
			responses[i] = FileUploadResponse{Path: f.Path, Error: err.Error()}
			continue
		}

		if client != nil {
			// Fast path: use daemon to write file
			mkdirCmd := fmt.Sprintf("mkdir -p '%s'", filepath.Dir(resolved))
			client.Exec(context.Background(), mkdirCmd, "/", "", 10)

			b64 := base64.StdEncoding.EncodeToString(f.Content)
			writeCmd := fmt.Sprintf("base64 -d > '%s' && chmod 666 '%s'", resolved, resolved)
			resp, err := client.Exec(context.Background(), writeCmd, "/", b64, 30)
			if err != nil || (resp != nil && resp.ExitCode != 0) {
				responses[i] = FileUploadResponse{Path: resolved, Error: "permission_denied"}
				continue
			}
			responses[i] = FileUploadResponse{Path: resolved}
		} else {
			// Fallback: docker exec + base64
			parent := filepath.Dir(resolved)
			mkdirCmd := b.dockerCmd("exec", b.containerName, "mkdir", "-p", parent)
			exec.Command(mkdirCmd[0], mkdirCmd[1:]...).Run()

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
	}

	return responses
}

// DownloadFiles downloads files from the container.
// Uses daemon when available, otherwise falls back to docker exec + base64.
func (b *DockerBackend) DownloadFiles(paths []string) []FileDownloadResponse {
	b.waitForContainer()
	responses := make([]FileDownloadResponse, len(paths))

	client := b.getDaemonClient()

	for i, path := range paths {
		resolved, err := b.ResolvePath(path)
		if err != nil {
			responses[i] = FileDownloadResponse{Path: path, Error: err.Error()}
			continue
		}

		if client != nil {
			// Fast path: use daemon
			readCmd := fmt.Sprintf("base64 '%s'", resolved)
			resp, err := client.Exec(context.Background(), readCmd, "/", "", 30)
			if err != nil || (resp != nil && resp.ExitCode != 0) {
				responses[i] = FileDownloadResponse{Path: resolved, Error: "file_not_found"}
				continue
			}
			content, err := base64.StdEncoding.DecodeString(strings.TrimSpace(resp.Stdout))
			if err != nil {
				responses[i] = FileDownloadResponse{Path: resolved, Error: "decode_error"}
				continue
			}
			responses[i] = FileDownloadResponse{Path: resolved, Content: content}
		} else {
			// Fallback: docker exec
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
	}

	return responses
}
