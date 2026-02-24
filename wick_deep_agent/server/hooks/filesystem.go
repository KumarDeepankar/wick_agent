package hooks

import (
	"context"
	"fmt"

	"wick_go/agent"
	"wick_go/backend"
	"wick_go/llm"
)

// FilesystemHook registers file-operation tools (ls, read_file, write_file, edit_file,
// glob, grep, execute) that delegate to a backend.Execute() with generated shell commands.
//
// Also implements large result eviction: if a tool result exceeds 80,000 chars (~20k tokens),
// the output is truncated with a head+tail reference.
type FilesystemHook struct {
	agent.BaseHook
	backend backend.Backend
}

// NewFilesystemHook creates a filesystem hook backed by the given backend.
func NewFilesystemHook(b backend.Backend) *FilesystemHook {
	return &FilesystemHook{backend: b}
}

func (h *FilesystemHook) Name() string { return "filesystem" }

func (h *FilesystemHook) Phases() []string {
	return []string{"before_agent", "wrap_tool_call"}
}

// BeforeAgent registers the 7 file-operation tools on the agent state.
func (h *FilesystemHook) BeforeAgent(ctx context.Context, state *agent.AgentState) error {
	b := h.backend

	// ls
	agent.RegisterToolOnState(state, &agent.FuncTool{
		ToolName: "ls",
		ToolDesc: "List files and directories at a given path. Returns names, types, and sizes.",
		ToolParams: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{"type": "string", "description": fmt.Sprintf("Directory path to list (default: %s)", b.Workdir())},
			},
		},
		Fn: func(ctx context.Context, args map[string]any) (string, error) {
			path, _ := args["path"].(string)
			resolved, err := b.ResolvePath(path)
			if err != nil {
				return "Error: " + err.Error(), nil
			}
			cmd := backend.LsCommand(resolved)
			result := b.Execute(cmd)
			return result.Output, nil
		},
	})

	// read_file
	agent.RegisterToolOnState(state, &agent.FuncTool{
		ToolName: "read_file",
		ToolDesc: "Read the contents of a file at the given path.",
		ToolParams: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"file_path": map[string]any{"type": "string", "description": fmt.Sprintf("Path to the file to read (relative to %s, or absolute within it)", b.Workdir())},
			},
			"required": []string{"file_path"},
		},
		Fn: func(ctx context.Context, args map[string]any) (string, error) {
			path, _ := args["file_path"].(string)
			if path == "" {
				return "Error: file_path is required", nil
			}
			resolved, err := b.ResolvePath(path)
			if err != nil {
				return "Error: " + err.Error(), nil
			}
			cmd := backend.ReadFileCommand(resolved)
			result := b.Execute(cmd)
			return result.Output, nil
		},
	})

	// write_file
	agent.RegisterToolOnState(state, &agent.FuncTool{
		ToolName: "write_file",
		ToolDesc: "Write content to a file at the given path. Creates the file and parent directories if they don't exist.",
		ToolParams: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"file_path": map[string]any{"type": "string", "description": fmt.Sprintf("Path to write the file (relative to %s, or absolute within it)", b.Workdir())},
				"content":   map[string]any{"type": "string", "description": "Content to write"},
			},
			"required": []string{"file_path", "content"},
		},
		Fn: func(ctx context.Context, args map[string]any) (string, error) {
			path, _ := args["file_path"].(string)
			content, _ := args["content"].(string)
			if path == "" {
				return "Error: file_path is required", nil
			}
			resolved, err := b.ResolvePath(path)
			if err != nil {
				return "Error: " + err.Error(), nil
			}
			cmd := backend.WriteFileCommand(resolved, content)
			result := b.Execute(cmd)
			if result.ExitCode != 0 {
				return "Error: " + result.Output, nil
			}
			// Track written files
			if state.Files == nil {
				state.Files = make(map[string]string)
			}
			state.Files[resolved] = content
			return fmt.Sprintf("File written: %s (%d bytes)", resolved, len(content)), nil
		},
	})

	// edit_file
	agent.RegisterToolOnState(state, &agent.FuncTool{
		ToolName: "edit_file",
		ToolDesc: "Edit a file by replacing old_text with new_text. The old_text must be an exact match.",
		ToolParams: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"file_path": map[string]any{"type": "string", "description": fmt.Sprintf("Path to the file to edit (relative to %s, or absolute within it)", b.Workdir())},
				"old_text":  map[string]any{"type": "string", "description": "Exact text to find and replace"},
				"new_text":  map[string]any{"type": "string", "description": "Text to replace old_text with"},
			},
			"required": []string{"file_path", "old_text", "new_text"},
		},
		Fn: func(ctx context.Context, args map[string]any) (string, error) {
			path, _ := args["file_path"].(string)
			oldText, _ := args["old_text"].(string)
			newText, _ := args["new_text"].(string)
			if path == "" {
				return "Error: file_path is required", nil
			}
			resolved, err := b.ResolvePath(path)
			if err != nil {
				return "Error: " + err.Error(), nil
			}
			cmd := backend.EditFileCommand(resolved, oldText, newText)
			result := b.Execute(cmd)
			if result.ExitCode != 0 {
				return result.Output, nil
			}
			// Read back edited file and update state tracker
			readResult := b.Execute(backend.ReadFileCommand(resolved))
			if readResult.ExitCode == 0 {
				if state.Files == nil {
					state.Files = make(map[string]string)
				}
				state.Files[resolved] = readResult.Output
			}
			return result.Output, nil
		},
	})

	// glob
	agent.RegisterToolOnState(state, &agent.FuncTool{
		ToolName: "glob",
		ToolDesc: "Find files matching a glob pattern. Returns matching file paths.",
		ToolParams: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{"type": "string", "description": "Glob pattern (e.g., '*.py', '**/*.js')"},
				"path":    map[string]any{"type": "string", "description": fmt.Sprintf("Directory to search in (default: %s)", b.Workdir())},
			},
			"required": []string{"pattern"},
		},
		Fn: func(ctx context.Context, args map[string]any) (string, error) {
			pattern, _ := args["pattern"].(string)
			path, _ := args["path"].(string)
			resolved, err := b.ResolvePath(path)
			if err != nil {
				return "Error: " + err.Error(), nil
			}
			cmd := backend.GlobCommand(pattern, resolved)
			result := b.Execute(cmd)
			return result.Output, nil
		},
	})

	// grep
	agent.RegisterToolOnState(state, &agent.FuncTool{
		ToolName: "grep",
		ToolDesc: "Search file contents for a pattern. Returns matching lines with file paths and line numbers.",
		ToolParams: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{"type": "string", "description": "Search pattern (regex supported)"},
				"path":    map[string]any{"type": "string", "description": fmt.Sprintf("File or directory to search in (default: %s)", b.Workdir())},
			},
			"required": []string{"pattern"},
		},
		Fn: func(ctx context.Context, args map[string]any) (string, error) {
			pattern, _ := args["pattern"].(string)
			path, _ := args["path"].(string)
			resolved, err := b.ResolvePath(path)
			if err != nil {
				return "Error: " + err.Error(), nil
			}
			cmd := backend.GrepCommand(pattern, resolved)
			result := b.Execute(cmd)
			return result.Output, nil
		},
	})

	// execute
	agent.RegisterToolOnState(state, &agent.FuncTool{
		ToolName: "execute",
		ToolDesc: "Execute an arbitrary shell command in the workspace.",
		ToolParams: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{"type": "string", "description": "Shell command to execute"},
			},
			"required": []string{"command"},
		},
		Fn: func(ctx context.Context, args map[string]any) (string, error) {
			command, _ := args["command"].(string)
			if command == "" {
				return "Error: command is required", nil
			}
			result := b.Execute(command)
			return result.Output, nil
		},
	})

	return nil
}

// WrapToolCall implements large result eviction.
// If a tool result exceeds 80,000 chars, truncate with head+tail.
func (h *FilesystemHook) WrapToolCall(ctx context.Context, call agent.ToolCall, next agent.ToolCallFunc) (*agent.ToolResult, error) {
	result, err := next(ctx, call)
	if err != nil || result == nil {
		return result, err
	}

	const maxChars = 80_000
	// Excluded tools (small results that shouldn't be evicted)
	excluded := map[string]bool{
		"ls": true, "glob": true, "grep": true,
		"read_file": true, "edit_file": true, "write_file": true,
	}

	if len(result.Output) > maxChars && !excluded[call.Name] {
		// Truncate: show first 2000 chars + last 2000 chars
		head := result.Output[:2000]
		tail := result.Output[len(result.Output)-2000:]
		result.Output = fmt.Sprintf(
			"%s\n\n... [Output truncated: %d chars total. Showing first and last 2000 chars] ...\n\n%s",
			head, len(result.Output), tail,
		)
	}

	return result, nil
}

// ModifyRequest is a no-op for FilesystemHook.
func (h *FilesystemHook) ModifyRequest(ctx context.Context, msgs []agent.Message) ([]agent.Message, error) {
	return msgs, nil
}

// WrapModelCall passes through.
func (h *FilesystemHook) WrapModelCall(ctx context.Context, msgs []agent.Message, next agent.ModelCallWrapFunc) (*llm.Response, error) {
	return next(ctx, msgs)
}

