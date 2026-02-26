package backend

import (
	"encoding/json"
	"fmt"
	"strings"
)

// wickfs command generators — build command strings for the wickfs binary
// running inside Docker sandbox containers.

// shellQuote safely quotes a string for shell use.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// LsCommand returns a wickfs ls command string.
func LsCommand(path string) string {
	return "wickfs ls " + shellQuote(path)
}

// ReadFileCommand returns a wickfs read command string.
func ReadFileCommand(path string) string {
	return "wickfs read " + shellQuote(path)
}

// WriteFileCommand returns a wickfs write command string.
// Content must be passed via stdin using ExecuteWithStdin.
func WriteFileCommand(path string) string {
	return "wickfs write " + shellQuote(path)
}

// EditFileCommand returns a wickfs edit command string.
// A JSON {old_text, new_text} object must be passed via stdin using ExecuteWithStdin.
func EditFileCommand(path string) string {
	return "wickfs edit " + shellQuote(path)
}

// GrepCommand returns a wickfs grep command string.
func GrepCommand(pattern, path string) string {
	return "wickfs grep " + shellQuote(pattern) + " " + shellQuote(path)
}

// GlobCommand returns a wickfs glob command string.
func GlobCommand(pattern, path string) string {
	return "wickfs glob " + shellQuote(pattern) + " " + shellQuote(path)
}

// ExecCommand returns a wickfs exec command string.
func ExecCommand(command string) string {
	return "wickfs exec " + shellQuote(command)
}

// ── wickfs response parsing ──────────────────────────────────────────────────

// WickfsResponse is the JSON envelope returned by wickfs commands.
type WickfsResponse struct {
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data"`
	Error string          `json:"error"`
}

// ParseWickfsResponse parses the JSON output of a wickfs command.
func ParseWickfsResponse(output string) (WickfsResponse, error) {
	output = strings.TrimSpace(output)
	var resp WickfsResponse
	if err := json.Unmarshal([]byte(output), &resp); err != nil {
		return resp, fmt.Errorf("failed to parse wickfs response: %w (raw: %s)", err, truncate(output, 200))
	}
	return resp, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
