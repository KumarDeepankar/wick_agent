package backend

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"wick_server/wickfs"
)

// Default daemon port inside the container.
const DaemonPort = "9090"

// DaemonRequest is the JSON command sent to the wick-daemon.
type DaemonRequest struct {
	ID      string `json:"id"`
	Cmd     string `json:"cmd"`
	Workdir string `json:"workdir,omitempty"`
	Stdin   string `json:"stdin,omitempty"`
	Timeout int    `json:"timeout,omitempty"`
}

// DaemonResponse is the JSON result from the wick-daemon.
type DaemonResponse struct {
	ID       string `json:"id"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
	Error    string `json:"error,omitempty"`
}

// DaemonClient manages a persistent connection to a wick-daemon instance.
// It is safe for concurrent use — requests are serialized with a mutex.
type DaemonClient struct {
	mu      sync.Mutex
	conn    net.Conn
	enc     *json.Encoder
	scanner *bufio.Scanner
	network string // "tcp" or "unix"
	addr    string
	nextID  atomic.Int64
}

// DialDaemon connects to a wick-daemon at the given address.
// network is "tcp" or "unix". addr is e.g. "172.17.0.2:9090" or "/tmp/wick-daemon.sock".
func DialDaemon(network, addr string) (*DaemonClient, error) {
	conn, err := net.DialTimeout(network, addr, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("daemon dial %s://%s: %w", network, addr, err)
	}

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 64*1024), 10*1024*1024) // 10MB max response

	return &DaemonClient{
		conn:    conn,
		enc:     json.NewEncoder(conn),
		scanner: scanner,
		network: network,
		addr:    addr,
	}, nil
}

// Exec sends a command to the daemon and waits for the response.
func (c *DaemonClient) Exec(ctx context.Context, cmd, workdir, stdin string, timeout int) (*DaemonResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn == nil {
		return nil, fmt.Errorf("daemon connection closed")
	}

	id := fmt.Sprintf("r%d", c.nextID.Add(1))

	req := DaemonRequest{
		ID:      id,
		Cmd:     cmd,
		Workdir: workdir,
		Stdin:   stdin,
		Timeout: timeout,
	}

	// Set a write deadline based on context
	if deadline, ok := ctx.Deadline(); ok {
		c.conn.SetWriteDeadline(deadline)
	}

	if err := c.enc.Encode(req); err != nil {
		c.conn = nil // mark as broken
		return nil, fmt.Errorf("daemon send: %w", err)
	}

	// Set read deadline
	if deadline, ok := ctx.Deadline(); ok {
		c.conn.SetReadDeadline(deadline)
	} else {
		// Default: timeout from request + 5s buffer
		d := time.Duration(timeout)*time.Second + 5*time.Second
		if timeout <= 0 {
			d = 125 * time.Second // 120s default + 5s buffer
		}
		c.conn.SetReadDeadline(time.Now().Add(d))
	}

	if !c.scanner.Scan() {
		err := c.scanner.Err()
		c.conn = nil // mark as broken
		if err != nil {
			return nil, fmt.Errorf("daemon read: %w", err)
		}
		return nil, fmt.Errorf("daemon connection closed")
	}

	var resp DaemonResponse
	if err := json.Unmarshal(c.scanner.Bytes(), &resp); err != nil {
		return nil, fmt.Errorf("daemon response parse: %w", err)
	}

	return &resp, nil
}

// Ping sends a no-op command to verify the daemon is responsive.
func (c *DaemonClient) Ping(ctx context.Context) error {
	resp, err := c.Exec(ctx, "echo ok", "/", "", 5)
	if err != nil {
		return err
	}
	if resp.ExitCode != 0 {
		return fmt.Errorf("ping failed: exit %d", resp.ExitCode)
	}
	return nil
}

// Close closes the daemon connection.
func (c *DaemonClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		err := c.conn.Close()
		c.conn = nil
		return err
	}
	return nil
}

// Alive reports whether the connection is still open.
func (c *DaemonClient) Alive() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn != nil
}

// ── DaemonExecutor — implements wickfs.Executor via DaemonClient ─────────────

// DaemonExecutor implements wickfs.Executor by sending commands to a wick-daemon.
type DaemonExecutor struct {
	client  *DaemonClient
	workdir string
	timeout int // seconds
}

// Compile-time check that DaemonExecutor satisfies wickfs.Executor.
var _ wickfs.Executor = (*DaemonExecutor)(nil)

// NewDaemonExecutor creates an executor backed by a daemon connection.
func NewDaemonExecutor(client *DaemonClient, workdir string, timeout int) *DaemonExecutor {
	return &DaemonExecutor{client: client, workdir: workdir, timeout: timeout}
}

func (e *DaemonExecutor) Run(ctx context.Context, command string) (string, int, error) {
	resp, err := e.client.Exec(ctx, command, e.workdir, "", e.timeout)
	if err != nil {
		return "", 1, err
	}
	if resp.Error != "" {
		return resp.Error, resp.ExitCode, nil
	}
	return combineOutput(resp), resp.ExitCode, nil
}

func (e *DaemonExecutor) RunWithStdin(ctx context.Context, command, stdin string) (string, int, error) {
	resp, err := e.client.Exec(ctx, command, e.workdir, stdin, e.timeout)
	if err != nil {
		return "", 1, err
	}
	if resp.Error != "" {
		return resp.Error, resp.ExitCode, nil
	}
	return combineOutput(resp), resp.ExitCode, nil
}

// combineOutput merges stdout/stderr following the same rules as DockerBackend:
// stdout is primary. Stderr is only included when stdout is empty or command failed.
func combineOutput(resp *DaemonResponse) string {
	output := resp.Stdout
	if output == "" && resp.Stderr != "" {
		output = resp.Stderr
	} else if resp.ExitCode != 0 && resp.Stderr != "" {
		output = strings.TrimRight(output, "\n") + "\n" + resp.Stderr
	}
	return output
}

// ── Daemon connection helpers ────────────────────────────────────────────────

// probeDaemon attempts to connect to a wick-daemon. Returns a connected client
// or nil if the daemon is not reachable. Tries TCP first, then Unix socket.
func probeDaemon(containerIP string, socketPath string) *DaemonClient {
	// Try TCP (works for both local and remote Docker)
	if containerIP != "" {
		addr := containerIP + ":" + DaemonPort
		client, err := DialDaemon("tcp", addr)
		if err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if client.Ping(ctx) == nil {
				log.Printf("Connected to wick-daemon at tcp://%s", addr)
				return client
			}
			client.Close()
		}
	}

	// Try Unix socket (local Docker only, if socket is mounted)
	if socketPath != "" {
		client, err := DialDaemon("unix", socketPath)
		if err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if client.Ping(ctx) == nil {
				log.Printf("Connected to wick-daemon at unix://%s", socketPath)
				return client
			}
			client.Close()
		}
	}

	return nil
}
