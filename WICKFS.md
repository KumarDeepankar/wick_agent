# wickfs — Filesystem Abstraction Layer

## Overview

`wickfs` is a Go package that provides a unified filesystem interface for agent tool operations (read, write, edit, grep, glob, exec). It has two implementations:

- **LocalFS** — direct Go stdlib calls (`os.ReadFile`, `os.ReadDir`, etc.). Zero process spawning.
- **RemoteFS** — builds `wickfs` CLI command strings and delegates to an `Executor` (e.g. `docker exec`). Communicates via JSON over stdout.

The agent's `FilesystemHook` calls `backend.FS()` which returns either a `LocalFS` or `RemoteFS` depending on the backend type. The hook layer doesn't know which implementation it's using.

```
Agent loop → FilesystemHook → tool call (e.g. "read_file")
                                   │
                     ┌─────────────┴─────────────┐
                     │                             │
              LocalBackend                  DockerBackend
              b.FS() → LocalFS              b.FS() → RemoteFS
                   │                             │
              os.ReadFile()            docker exec wickfs read '/path'
              (direct syscall)             │
                                      inside container:
                                        wickfs binary → LocalFS → os.ReadFile()
                                        → JSON response on stdout
                                      RemoteFS parses JSON
```

---

## Package: `wickfs/` (library)

### Interface (`wickfs.go`)

```go
package wickfs

import "context"

// FileSystem is the interface for workspace filesystem operations.
type FileSystem interface {
    Ls(ctx context.Context, path string) ([]DirEntry, error)
    ReadFile(ctx context.Context, path string) (string, error)
    WriteFile(ctx context.Context, path, content string) (*WriteResult, error)
    EditFile(ctx context.Context, path, oldText, newText string) (*EditResult, error)
    Grep(ctx context.Context, pattern, path string) (*GrepResult, error)
    Glob(ctx context.Context, pattern, path string) (*GlobResult, error)
    Exec(ctx context.Context, command string) (*ExecResult, error)
}
```

### Types (`wickfs.go`)

```go
type DirEntry struct {
    Name string `json:"name"`
    Type string `json:"type"` // "file", "dir", or "symlink"
    Size int64  `json:"size"`
}

type WriteResult struct {
    Path         string `json:"path"`
    BytesWritten int    `json:"bytes_written"`
}

type EditResult struct {
    Path         string `json:"path"`
    Replacements int    `json:"replacements"`
}

type GrepMatch struct {
    File string `json:"file"`
    Line int    `json:"line"`
    Text string `json:"text"`
}

type GrepResult struct {
    Matches   []GrepMatch `json:"matches"`
    Truncated bool        `json:"truncated"`
}

type GlobResult struct {
    Files     []string `json:"files"`
    Truncated bool     `json:"truncated"`
}

type ExecResult struct {
    Stdout   string `json:"stdout"`
    Stderr   string `json:"stderr"`
    ExitCode int    `json:"exit_code"`
}
```

---

## LocalFS (`local.go`)

Direct Go stdlib calls. No shell, no process spawning, no JSON serialization.

### Construction

```go
fs := wickfs.NewLocalFS()
```

### Ls

Lists directory entries using `os.ReadDir`.

```go
func (fs *LocalFS) Ls(_ context.Context, path string) ([]DirEntry, error) {
    entries, err := os.ReadDir(path)
    // ... maps os.DirEntry → wickfs.DirEntry with type detection (file/dir/symlink)
}
```

### ReadFile

Reads file via `os.ReadFile`. Binary content is base64-encoded with a `"base64:"` prefix.

```go
func (fs *LocalFS) ReadFile(_ context.Context, path string) (string, error) {
    data, err := os.ReadFile(path)
    if utf8.Valid(data) {
        return string(data), nil
    }
    return "base64:" + base64.StdEncoding.EncodeToString(data), nil
}
```

### WriteFile

Atomic write: creates temp file → writes content → `os.Chmod` → `os.Rename`. Creates parent directories with `os.MkdirAll`.

```go
func (fs *LocalFS) WriteFile(_ context.Context, path, content string) (*WriteResult, error) {
    os.MkdirAll(filepath.Dir(path), 0755)
    tmp, _ := os.CreateTemp(dir, ".wickfs-tmp-*")
    tmp.WriteString(content)
    tmp.Close()
    os.Chmod(tmpName, 0666)
    os.Rename(tmpName, path)    // atomic on same filesystem
    return &WriteResult{Path: path, BytesWritten: len(content)}, nil
}
```

### EditFile

Reads file, replaces first occurrence of `oldText` with `newText`, atomic write back.

```go
func (fs *LocalFS) EditFile(_ context.Context, path, oldText, newText string) (*EditResult, error) {
    data, _ := os.ReadFile(path)
    original := string(data)
    if !strings.Contains(original, oldText) {
        return nil, fmt.Errorf("old_text not found in file")
    }
    updated := strings.Replace(original, oldText, newText, 1)  // first occurrence only
    // ... atomic write (same as WriteFile)
    return &EditResult{Path: path, Replacements: 1}, nil
}
```

### Grep

Walks directory tree with `filepath.WalkDir`, applies `regexp.Compile(pattern)` line by line using `bufio.Scanner`.

```go
func (fs *LocalFS) Grep(_ context.Context, pattern, path string) (*GrepResult, error) {
    re, _ := regexp.Compile(pattern)
    filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
        // Skip: .hidden dirs, node_modules, __pycache__, vendor
        // Skip: binary file extensions (.png, .zip, .pdf, .so, .exe, etc.)
        scanner := bufio.NewScanner(f)
        for scanner.Scan() {
            if re.MatchString(line) {
                matches = append(matches, GrepMatch{File: p, Line: lineNum, Text: line})
            }
        }
    })
    // Max 200 matches, then truncated=true
}
```

**Skipped directories:** `.hidden`, `node_modules`, `__pycache__`, `vendor`

**Skipped file extensions:** `.png`, `.jpg`, `.gif`, `.zip`, `.tar`, `.gz`, `.pdf`, `.doc`, `.so`, `.dll`, `.exe`, `.wasm`, `.pyc`, `.class`, `.mp3`, `.mp4`, etc.

**Max matches:** 200 (then `Truncated: true`)

### Glob

Walks directory tree, matches filenames with `filepath.Match(pattern, name)`.

```go
func (fs *LocalFS) Glob(_ context.Context, pattern, path string) (*GlobResult, error) {
    filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
        // Skip: same dirs as Grep
        matched, _ := filepath.Match(pattern, d.Name())
        if matched {
            files = append(files, p)
        }
    })
    // Max 100 files, then truncated=true
}
```

**Max files:** 100 (then `Truncated: true`)

### Exec

Runs a shell command via `sh -c`. Captures stdout and stderr separately.

```go
func (fs *LocalFS) Exec(_ context.Context, command string) (*ExecResult, error) {
    cmd := exec.Command("sh", "-c", command)
    cmd.Stdout = &stdout
    cmd.Stderr = &stderr
    err := cmd.Run()
    return &ExecResult{
        Stdout:   stdout.String(),
        Stderr:   stderr.String(),
        ExitCode: exitCode,
    }, nil
}
```

---

## RemoteFS (`remote.go`)

Builds `wickfs` CLI command strings and delegates to an `Executor` interface. Used by `DockerBackend` to run filesystem operations inside Docker containers.

### Executor Interface

```go
// Executor abstracts command execution on a remote target (e.g. docker exec).
type Executor interface {
    Run(ctx context.Context, command string) (stdout string, exitCode int, err error)
    RunWithStdin(ctx context.Context, command, stdin string) (stdout string, exitCode int, err error)
}
```

`DockerBackend` implements this via `DockerExecutor`:

```go
type DockerExecutor struct{ backend *DockerBackend }

func (e *DockerExecutor) Run(ctx context.Context, command string) (string, int, error) {
    // → docker exec container-name sh -c "command"
}
```

### Construction

```go
executor := &DockerExecutor{backend: dockerBackend}
fs := wickfs.NewRemoteFS(executor)
```

### How each operation maps to CLI commands

| Method | CLI command | Stdin |
|--------|-----------|-------|
| `Ls("/workspace")` | `wickfs ls '/workspace'` | — |
| `ReadFile("/workspace/app.py")` | `wickfs read '/workspace/app.py'` | — |
| `WriteFile("/workspace/out.py", content)` | `wickfs write '/workspace/out.py'` | file content |
| `EditFile("/workspace/app.py", old, new)` | `wickfs edit '/workspace/app.py'` | `{"old_text":"...","new_text":"..."}` |
| `Grep("TODO", "/workspace")` | `wickfs grep 'TODO' '/workspace'` | — |
| `Glob("*.py", "/workspace")` | `wickfs glob '*.py' '/workspace'` | — |
| `Exec("pip install flask")` | `wickfs exec 'pip install flask'` | — |

All arguments are shell-quoted using single-quote escaping:

```go
func shellQuote(s string) string {
    return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
```

### JSON Response Protocol

Every `wickfs` CLI command returns a JSON envelope on stdout:

```json
// Success
{"ok": true, "data": <result>}

// Error
{"ok": false, "error": "message"}
```

RemoteFS parses this with `ParseWickfsResponse`:

```go
type WickfsResponse struct {
    OK    bool            `json:"ok"`
    Data  json.RawMessage `json:"data"`
    Error string          `json:"error"`
}

func ParseWickfsResponse(output string) (WickfsResponse, error) {
    // Fast path: entire output is valid JSON
    // Fallback: scan lines for first '{' (handles stderr noise mixed in)
}
```

### Full RemoteFS call flow (example: ReadFile)

```go
func (fs *RemoteFS) ReadFile(ctx context.Context, path string) (string, error) {
    cmd := "wickfs read " + shellQuote(path)         // → wickfs read '/workspace/app.py'
    out, _, err := fs.exec.Run(ctx, cmd)              // → docker exec container sh -c "wickfs read '/workspace/app.py'"
    resp, err := ParseWickfsResponse(out)             // → parse {"ok":true,"data":"file content..."}
    if !resp.OK {
        return "", fmt.Errorf("%s", resp.Error)
    }
    var content string
    json.Unmarshal(resp.Data, &content)
    return content, nil
}
```

---

## CLI Binary: `cmd/wickfs/`

A standalone binary compiled and injected into Docker containers. Each subcommand delegates to `wickfs.LocalFS` internally.

### Build

```bash
cd wick_deep_agent/server
go build -o wickfs ./cmd/wickfs/
```

### Usage

```
wickfs <command> [args...]

Commands: ls, read, write, edit, grep, glob, exec
```

### Subcommand implementations

All subcommands follow the same pattern: parse args → create `wickfs.NewLocalFS()` → call method → `writeOK(result)` or `writeError(msg)`.

**ls** — `cmd_ls.go`
```go
func cmdLs(args []string) {
    path := "."
    if len(args) > 0 && args[0] != "" {
        path = args[0]
    }
    fs := wickfs.NewLocalFS()
    entries, err := fs.Ls(context.Background(), path)
    if err != nil {
        writeError(err.Error())
        return
    }
    writeOK(entries)
}
```

**read** — `cmd_read.go`
```go
func cmdRead(args []string) {
    // usage: wickfs read <path>
    fs := wickfs.NewLocalFS()
    content, err := fs.ReadFile(context.Background(), args[0])
    writeOK(content)
}
```

**write** — `cmd_write.go`
```go
func cmdWrite(args []string) {
    // usage: wickfs write <path> (content on stdin)
    content, _ := io.ReadAll(os.Stdin)
    fs := wickfs.NewLocalFS()
    result, _ := fs.WriteFile(context.Background(), args[0], string(content))
    writeOK(result)
}
```

**edit** — `cmd_edit.go`
```go
func cmdEdit(args []string) {
    // usage: wickfs edit <path> (JSON {old_text, new_text} on stdin)
    stdinData, _ := io.ReadAll(os.Stdin)
    var input editInput
    json.Unmarshal(stdinData, &input)
    fs := wickfs.NewLocalFS()
    result, _ := fs.EditFile(context.Background(), args[0], input.OldText, input.NewText)
    writeOK(result)
}
```

**grep** — `cmd_grep.go`
```go
func cmdGrep(args []string) {
    // usage: wickfs grep <pattern> [path]
    pattern := args[0]
    searchPath := "."  // default
    if len(args) > 1 { searchPath = args[1] }
    fs := wickfs.NewLocalFS()
    result, _ := fs.Grep(context.Background(), pattern, searchPath)
    writeOK(result)
}
```

**glob** — `cmd_glob.go`
```go
func cmdGlob(args []string) {
    // usage: wickfs glob <pattern> [path]
    pattern := args[0]
    searchPath := "."
    if len(args) > 1 { searchPath = args[1] }
    fs := wickfs.NewLocalFS()
    result, _ := fs.Glob(context.Background(), pattern, searchPath)
    writeOK(result)
}
```

**exec** — `cmd_exec.go`
```go
func cmdExec(args []string) {
    // usage: wickfs exec <command>
    command := strings.Join(args, " ")
    fs := wickfs.NewLocalFS()
    result, _ := fs.Exec(context.Background(), command)
    writeOK(result)
}
```

### JSON response helpers (`response.go`)

```go
type response struct {
    OK    bool   `json:"ok"`
    Data  any    `json:"data,omitempty"`
    Error string `json:"error,omitempty"`
}

func writeOK(data any) {
    out, _ := json.Marshal(response{OK: true, Data: data})
    fmt.Println(string(out))
}

func writeError(msg string) {
    out, _ := json.Marshal(response{OK: false, Error: msg})
    fmt.Println(string(out))
    os.Exit(1)
}
```

---

## Backend Integration

### LocalBackend (`backend/local.go`)

```go
type LocalBackend struct {
    workdir        string
    timeout        time.Duration
    maxOutputBytes int
    fs             *wickfs.LocalFS    // direct stdlib calls
}

func NewLocalBackend(workdir string, ...) *LocalBackend {
    return &LocalBackend{
        fs: wickfs.NewLocalFS(),
        ...
    }
}

func (b *LocalBackend) FS() wickfs.FileSystem { return b.fs }
```

When the agent calls `read_file("/workspace/app.py")`:
```
FilesystemHook → backend.FS().ReadFile(ctx, "/workspace/app.py")
                 → LocalFS.ReadFile()
                 → os.ReadFile("/workspace/app.py")
                 → returns file content directly
```

### DockerBackend (`backend/docker.go`)

```go
type DockerBackend struct {
    containerName string
    workdir       string
    remoteFS      *wickfs.RemoteFS    // commands via docker exec
    ...
}

func NewDockerBackend(...) *DockerBackend {
    db := &DockerBackend{...}
    db.remoteFS = wickfs.NewRemoteFS(&DockerExecutor{backend: db})
    return db
}

func (b *DockerBackend) FS() wickfs.FileSystem { return b.remoteFS }
```

When the agent calls `read_file("/workspace/app.py")`:
```
FilesystemHook → backend.FS().ReadFile(ctx, "/workspace/app.py")
                 → RemoteFS.ReadFile()
                 → executor.Run("wickfs read '/workspace/app.py'")
                 → docker exec container-name sh -c "wickfs read '/workspace/app.py'"
                 → inside container: wickfs binary → LocalFS.ReadFile()
                 → stdout: {"ok":true,"data":"file content..."}
                 → RemoteFS parses JSON → returns file content
```

### wickfs binary injection

The DockerBackend automatically injects the `wickfs` binary into containers:

1. **Probe**: `docker exec container wickfs ls /` — check if pre-baked in image
2. **Inject**: `docker cp /path/to/wickfs container:/usr/local/bin/wickfs`
3. **Chmod**: `docker exec container chmod +x /usr/local/bin/wickfs`

Binary search order:
1. `WICKFS_BIN` env var
2. `/usr/local/bin/wickfs` (container deployment)
3. Next to the wick_go executable
4. `./bin/wickfs_linux_{arch}` (dev / wheel builds)

---

## File Structure

```
wick_deep_agent/server/
├── wickfs/                         # Library package
│   ├── wickfs.go                   # FileSystem interface + all types
│   ├── local.go                    # LocalFS implementation (stdlib)
│   └── remote.go                   # RemoteFS implementation (CLI + Executor)
│
├── cmd/wickfs/                     # Standalone CLI binary
│   ├── main.go                     # Subcommand router
│   ├── response.go                 # JSON response helpers
│   ├── cmd_ls.go                   # ls subcommand
│   ├── cmd_read.go                 # read subcommand
│   ├── cmd_write.go                # write subcommand
│   ├── cmd_edit.go                 # edit subcommand
│   ├── cmd_grep.go                 # grep subcommand
│   ├── cmd_glob.go                 # glob subcommand
│   └── cmd_exec.go                 # exec subcommand
│
├── backend/
│   ├── local.go                    # LocalBackend → uses wickfs.LocalFS
│   └── docker.go                   # DockerBackend → uses wickfs.RemoteFS + DockerExecutor
│
└── hooks/
    └── filesystem.go               # FilesystemHook → calls backend.FS() for all file tools
```

---

## Limits

| Limit | Value | Configurable |
|-------|-------|-------------|
| Grep max matches | 200 | No (const `maxGrepMatches`) |
| Glob max files | 100 | No (const `maxGlobFiles`) |
| Skipped dirs (grep/glob) | `.hidden`, `node_modules`, `__pycache__`, `vendor` | No |
| Binary file extensions skipped | ~30 extensions (.png, .zip, .pdf, .so, etc.) | No |
| Command timeout | Inherited from backend config (`BackendCfg.Timeout`) | Yes |
| Max output bytes | Inherited from backend config (`BackendCfg.MaxOutputBytes`) | Yes |
