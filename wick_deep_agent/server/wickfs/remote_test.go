package wickfs

import (
	"context"
	"encoding/json"
	"testing"
)

// mockExecutor records the command and returns pre-set output.
type mockExecutor struct {
	lastCmd   string
	lastStdin string
	stdout    string
	exitCode  int
	err       error
}

func (m *mockExecutor) Run(_ context.Context, command string) (string, int, error) {
	m.lastCmd = command
	return m.stdout, m.exitCode, m.err
}

func (m *mockExecutor) RunWithStdin(_ context.Context, command, stdin string) (string, int, error) {
	m.lastCmd = command
	m.lastStdin = stdin
	return m.stdout, m.exitCode, m.err
}

func wickfsOK(data any) string {
	d, _ := json.Marshal(data)
	out, _ := json.Marshal(WickfsResponse{OK: true, Data: d})
	return string(out)
}

func wickfsErr(msg string) string {
	out, _ := json.Marshal(WickfsResponse{OK: false, Error: msg})
	return string(out)
}

func TestRemoteFS_Ls(t *testing.T) {
	exec := &mockExecutor{
		stdout: wickfsOK([]DirEntry{{Name: "a.txt", Type: "file", Size: 10}}),
	}
	fs := NewRemoteFS(exec)

	entries, err := fs.Ls(context.Background(), "/workspace")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Name != "a.txt" {
		t.Errorf("expected a.txt, got %s", entries[0].Name)
	}
	if exec.lastCmd != "wickfs ls '/workspace'" {
		t.Errorf("unexpected command: %s", exec.lastCmd)
	}
}

func TestRemoteFS_ReadFile(t *testing.T) {
	exec := &mockExecutor{
		stdout: wickfsOK("hello world"),
	}
	fs := NewRemoteFS(exec)

	content, err := fs.ReadFile(context.Background(), "/workspace/test.txt")
	if err != nil {
		t.Fatal(err)
	}
	if content != "hello world" {
		t.Fatalf("expected 'hello world', got %q", content)
	}
	if exec.lastCmd != "wickfs read '/workspace/test.txt'" {
		t.Errorf("unexpected command: %s", exec.lastCmd)
	}
}

func TestRemoteFS_WriteFile(t *testing.T) {
	exec := &mockExecutor{
		stdout: wickfsOK(WriteResult{Path: "/workspace/out.txt", BytesWritten: 5}),
	}
	fs := NewRemoteFS(exec)

	result, err := fs.WriteFile(context.Background(), "/workspace/out.txt", "hello")
	if err != nil {
		t.Fatal(err)
	}
	if result.BytesWritten != 5 {
		t.Fatalf("expected 5, got %d", result.BytesWritten)
	}
	if exec.lastCmd != "wickfs write '/workspace/out.txt'" {
		t.Errorf("unexpected command: %s", exec.lastCmd)
	}
	if exec.lastStdin != "hello" {
		t.Errorf("unexpected stdin: %s", exec.lastStdin)
	}
}

func TestRemoteFS_EditFile(t *testing.T) {
	exec := &mockExecutor{
		stdout: wickfsOK(EditResult{Path: "/workspace/f.txt", Replacements: 1}),
	}
	fs := NewRemoteFS(exec)

	result, err := fs.EditFile(context.Background(), "/workspace/f.txt", "old", "new")
	if err != nil {
		t.Fatal(err)
	}
	if result.Replacements != 1 {
		t.Fatalf("expected 1, got %d", result.Replacements)
	}
	if exec.lastCmd != "wickfs edit '/workspace/f.txt'" {
		t.Errorf("unexpected command: %s", exec.lastCmd)
	}
}

func TestRemoteFS_Grep(t *testing.T) {
	exec := &mockExecutor{
		stdout: wickfsOK(GrepResult{Matches: []GrepMatch{{File: "a.go", Line: 1, Text: "match"}}}),
	}
	fs := NewRemoteFS(exec)

	result, err := fs.Grep(context.Background(), "match", "/workspace")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(result.Matches))
	}
	if exec.lastCmd != "wickfs grep 'match' '/workspace'" {
		t.Errorf("unexpected command: %s", exec.lastCmd)
	}
}

func TestRemoteFS_Glob(t *testing.T) {
	exec := &mockExecutor{
		stdout: wickfsOK(GlobResult{Files: []string{"a.go", "b.go"}}),
	}
	fs := NewRemoteFS(exec)

	result, err := fs.Glob(context.Background(), "*.go", "/workspace")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(result.Files))
	}
	if exec.lastCmd != "wickfs glob '*.go' '/workspace'" {
		t.Errorf("unexpected command: %s", exec.lastCmd)
	}
}

func TestRemoteFS_Exec(t *testing.T) {
	exec := &mockExecutor{
		stdout: wickfsOK(ExecResult{Stdout: "hello\n", ExitCode: 0}),
	}
	fs := NewRemoteFS(exec)

	result, err := fs.Exec(context.Background(), "echo hello")
	if err != nil {
		t.Fatal(err)
	}
	if result.Stdout != "hello\n" {
		t.Fatalf("expected 'hello\\n', got %q", result.Stdout)
	}
	if exec.lastCmd != "wickfs exec 'echo hello'" {
		t.Errorf("unexpected command: %s", exec.lastCmd)
	}
}

func TestRemoteFS_Error(t *testing.T) {
	exec := &mockExecutor{
		stdout: wickfsErr("file not found"),
	}
	fs := NewRemoteFS(exec)

	_, err := fs.ReadFile(context.Background(), "/missing")
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() != "file not found" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParseWickfsResponse_Mixed(t *testing.T) {
	// Simulate noise before JSON
	input := "Warning: something\n" + wickfsOK("data")
	resp, err := ParseWickfsResponse(input)
	if err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Fatal("expected OK")
	}
}

func TestParseWickfsResponse_Invalid(t *testing.T) {
	_, err := ParseWickfsResponse("not json at all")
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestShellQuote(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"hello", "'hello'"},
		{"it's", "'it'\\''s'"},
		{"", "''"},
	}
	for _, tt := range tests {
		got := ShellQuote(tt.input)
		if got != tt.want {
			t.Errorf("ShellQuote(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
