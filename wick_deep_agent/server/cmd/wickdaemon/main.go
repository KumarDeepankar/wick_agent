// wick-daemon runs inside a Docker container and provides a fast command
// execution channel over TCP (and optionally Unix socket). This eliminates
// the overhead of "docker exec" for every command — the host connects once
// and sends commands as newline-delimited JSON.
//
// Protocol (NDJSON — one JSON object per line):
//
//	Request:  {"id":"r1","cmd":"ls /workspace","workdir":"/workspace","stdin":"","timeout":120}
//	Response: {"id":"r1","stdout":"...","stderr":"","exit_code":0}
//
// The daemon accepts multiple concurrent connections. Each connection can
// send multiple sequential requests (request-response pattern).
//
// Environment variables:
//
//	DAEMON_LISTEN  — TCP listen address (default "0.0.0.0:9090")
//	DAEMON_SOCKET  — Unix socket path  (default "/tmp/wick-daemon.sock")
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Request is the JSON command sent by the host.
type Request struct {
	ID      string `json:"id"`
	Cmd     string `json:"cmd"`
	Workdir string `json:"workdir,omitempty"`
	Stdin   string `json:"stdin,omitempty"`
	Timeout int    `json:"timeout,omitempty"` // seconds, default 120
}

// Response is the JSON result sent back to the host.
type Response struct {
	ID       string `json:"id"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
	Error    string `json:"error,omitempty"`
}

func main() {
	listenAddr := envOr("DAEMON_LISTEN", "0.0.0.0:9090")
	socketPath := envOr("DAEMON_SOCKET", "/tmp/wick-daemon.sock")

	var wg sync.WaitGroup
	var listeners []net.Listener

	// TCP listener
	tcpLn, err := net.Listen("tcp", listenAddr)
	if err != nil {
		log.Fatalf("TCP listen on %s failed: %v", listenAddr, err)
	}
	listeners = append(listeners, tcpLn)
	log.Printf("wick-daemon listening on tcp://%s", listenAddr)

	wg.Add(1)
	go func() {
		defer wg.Done()
		acceptLoop(tcpLn)
	}()

	// Unix socket listener
	os.Remove(socketPath) // clean up stale socket
	unixLn, err := net.Listen("unix", socketPath)
	if err != nil {
		log.Printf("Warning: Unix socket %s failed: %v (TCP-only mode)", socketPath, err)
	} else {
		listeners = append(listeners, unixLn)
		log.Printf("wick-daemon listening on unix://%s", socketPath)

		wg.Add(1)
		go func() {
			defer wg.Done()
			acceptLoop(unixLn)
		}()
	}

	// Block until SIGTERM/SIGINT
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	<-sig

	log.Println("wick-daemon shutting down...")
	for _, ln := range listeners {
		ln.Close()
	}
	wg.Wait()
}

// acceptLoop accepts connections on a listener and handles each concurrently.
func acceptLoop(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return // listener closed
		}
		go handleConn(conn)
	}
}

// handleConn processes sequential requests on a single connection.
// The connection stays open for multiple request-response cycles.
func handleConn(conn net.Conn) {
	defer conn.Close()

	scanner := bufio.NewScanner(conn)
	// Allow up to 10MB per line (large stdin payloads)
	scanner.Buffer(make([]byte, 64*1024), 10*1024*1024)
	enc := json.NewEncoder(conn)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			enc.Encode(Response{Error: "invalid JSON: " + err.Error()})
			continue
		}

		resp := executeCmd(req)
		if err := enc.Encode(resp); err != nil {
			return // write failed, connection broken
		}
	}
}

// executeCmd runs a shell command and returns the result.
func executeCmd(req Request) Response {
	if req.Cmd == "" {
		return Response{ID: req.ID, Error: "empty command", ExitCode: 1}
	}

	timeout := time.Duration(req.Timeout) * time.Second
	if timeout <= 0 {
		timeout = 120 * time.Second
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", req.Cmd)
	if req.Workdir != "" {
		cmd.Dir = req.Workdir
	}
	if req.Stdin != "" {
		cmd.Stdin = strings.NewReader(req.Stdin)
	}

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else if ctx.Err() != nil {
			return Response{
				ID:       req.ID,
				Error:    fmt.Sprintf("command timed out after %v", timeout),
				ExitCode: 124,
			}
		} else {
			return Response{
				ID:       req.ID,
				Error:    "exec error: " + err.Error(),
				ExitCode: 1,
			}
		}
	}

	return Response{
		ID:       req.ID,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exitCode,
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
