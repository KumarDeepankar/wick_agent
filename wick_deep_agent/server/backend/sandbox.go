package backend

import (
	"fmt"
	"path/filepath"
	"strings"
)

// BaseSandbox provides file operations by generating shell commands
// and routing them through Execute(). Backends embed this and provide
// their own Execute() implementation.
//
// This matches the Python deepagents BaseSandbox pattern:
// all file ops (ls, read, write, edit, grep, glob) are implemented
// as shell commands run through the backend's Execute().

// SandboxFileOps generates shell commands for file operations.
// These functions return commands that should be passed to backend.Execute().

// LsCommand returns a shell command to list a directory.
func LsCommand(path string) string {
	safePath := shellQuote(path)
	return fmt.Sprintf(
		`stat -c "%%n\t%%F\t%%s" %s/* %s/.* 2>/dev/null | grep -v "/\.$" | grep -v "/\.\.$"`,
		safePath, safePath,
	)
}

// ReadFileCommand returns a shell command to read a file.
func ReadFileCommand(path string) string {
	return fmt.Sprintf("cat %s", shellQuote(path))
}

// WriteFileCommand returns a shell command to write content to a file.
func WriteFileCommand(path, content string) string {
	dir := filepath.Dir(path)
	safePath := shellQuote(path)
	// Use heredoc to handle special characters in content
	escapedContent := strings.ReplaceAll(content, "'", "'\\''")
	return fmt.Sprintf("mkdir -p %s && printf '%%s' '%s' > %s",
		shellQuote(dir), escapedContent, safePath)
}

// EditFileCommand returns a shell command to perform a search-and-replace edit.
func EditFileCommand(path, oldText, newText string) string {
	// Escape for Python string literal (not shell-quoted — this goes inside python3 -c "...")
	pyPath := strings.ReplaceAll(path, `\`, `\\`)
	pyPath = strings.ReplaceAll(pyPath, `'`, `\'`)
	// Use Python for reliable string replacement (handles multi-line, special chars)
	escapedOld := strings.ReplaceAll(oldText, "'", "'\\''")
	escapedNew := strings.ReplaceAll(newText, "'", "'\\''")
	return fmt.Sprintf(
		`python3 -c "
import sys
path = '%s'
with open(path, 'r') as f: content = f.read()
old = '''%s'''
new = '''%s'''
if old not in content:
    print('Error: old_text not found in file', file=sys.stderr)
    sys.exit(1)
content = content.replace(old, new, 1)
with open(path, 'w') as f: f.write(content)
print('OK')
" 2>&1`, pyPath, escapedOld, escapedNew)
}

// GrepCommand returns a shell command to search file contents.
func GrepCommand(pattern, path string) string {
	return fmt.Sprintf("grep -rn %s %s 2>/dev/null || true",
		shellQuote(pattern), shellQuote(path))
}

// GlobCommand returns a shell command to find files by pattern.
func GlobCommand(pattern, path string) string {
	return fmt.Sprintf("find %s -name %s -type f 2>/dev/null | head -100",
		shellQuote(path), shellQuote(pattern))
}

// ExecuteCommand returns the command string unchanged (passthrough).
func ExecuteCommand(command string) string {
	return command
}

// shellQuote safely quotes a string for shell use.
func shellQuote(s string) string {
	// Simple quoting: wrap in single quotes, escape embedded single quotes
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
