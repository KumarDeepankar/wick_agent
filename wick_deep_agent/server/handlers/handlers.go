package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"wick_go/agent"
	"wick_go/backend"
	"wick_go/hooks"
	"wick_go/llm"
	"wick_go/sse"
	"wick_go/tracing"
)

// Config holds handler-level configuration.
type Config struct {
	WickGatewayURL string
}

// Deps holds shared dependencies injected into handlers.
type Deps struct {
	Registry  *agent.Registry
	AppConfig *Config

	// EventBus broadcasts per-user config change events (container_status, etc.)
	EventBus *EventBus

	// BackendStore maps "agentID:username" → backend.Backend
	Backends *BackendStore

	// ResolveUser extracts the username from the request context.
	// Set by main to bridge the auth middleware's context key into handlers.
	ResolveUser func(r *http.Request) string

	// ResolveRole extracts the user's role from the request context.
	ResolveRole func(r *http.Request) string

	// TraceStore holds recent request traces.
	TraceStore *tracing.Store

	// ExternalTools stores tools registered at runtime via HTTP callback.
	ExternalTools *ToolStore
}

// RegisterRoutes registers all /agents/ routes on the given mux.
func RegisterRoutes(mux *http.ServeMux, deps *Deps) {
	if deps.EventBus == nil {
		deps.EventBus = NewEventBus()
	}
	if deps.Backends == nil {
		deps.Backends = NewBackendStore()
	}

	h := &agentHandler{deps: deps}

	mux.HandleFunc("/agents/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/agents")
		path = strings.TrimPrefix(path, "/")

		// /agents/ (list or create)
		if path == "" || path == "/" {
			switch r.Method {
			case http.MethodGet:
				h.listAgents(w, r)
			case http.MethodPost:
				h.createAgent(w, r)
			default:
				http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			}
			return
		}

		if path == "events" {
			h.agentEvents(w, r)
			return
		}
		if path == "tools/available" {
			h.availableTools(w, r)
			return
		}
		if path == "tools/register" && r.Method == http.MethodPost {
			h.registerTool(w, r)
			return
		}
		if strings.HasPrefix(path, "tools/deregister/") {
			if r.Method == http.MethodDelete {
				toolName := strings.TrimPrefix(path, "tools/deregister/")
				h.deregisterTool(w, r, toolName)
			} else {
				http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			}
			return
		}
		if path == "skills/available" {
			h.availableSkills(w, r)
			return
		}
		if path == "hooks/available" {
			h.availableHooks(w, r)
			return
		}
		if path == "messages/test" {
			h.messagesTest(w, r)
			return
		}
		if path == "invoke" {
			h.invoke(w, r, nil)
			return
		}
		if path == "stream" {
			h.stream(w, r, nil)
			return
		}
		if path == "resume" {
			h.resume(w, r, nil)
			return
		}
		if path == "files/download" {
			h.downloadFile(w, r)
			return
		}
		if path == "files/upload" {
			h.uploadFile(w, r)
			return
		}
		if strings.HasPrefix(path, "traces") {
			h.traces(w, r, strings.TrimPrefix(path, "traces"))
			return
		}

		// /agents/{id}/...
		parts := strings.SplitN(path, "/", 2)
		agentID := parts[0]
		sub := ""
		if len(parts) > 1 {
			sub = parts[1]
		}

		switch sub {
		case "":
			switch r.Method {
			case http.MethodGet:
				h.getAgent(w, r, agentID)
			case http.MethodDelete:
				h.deleteAgent(w, r, agentID)
			default:
				http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			}
		case "invoke":
			h.invoke(w, r, &agentID)
		case "stream":
			h.stream(w, r, &agentID)
		case "resume":
			h.resume(w, r, &agentID)
		case "flow":
			h.getFlow(w, r, agentID)
		case "hooks":
			h.patchHooks(w, r, agentID)
		case "backend":
			h.patchBackend(w, r, agentID)
		case "terminal":
			h.terminal(w, r, agentID)
		case "files/list":
			h.listFiles(w, r, agentID)
		case "files/read":
			h.readFile(w, r, agentID)
		default:
			http.NotFound(w, r)
		}
	})
}

type agentHandler struct {
	deps *Deps
}

// resolveUsername extracts username from the request via the injected ResolveUser func.
func (h *agentHandler) resolveUsername(r *http.Request) string {
	if h.deps.ResolveUser != nil {
		return h.deps.ResolveUser(r)
	}
	return "local"
}

// resolveRole extracts the user's role from the request via the injected ResolveRole func.
func (h *agentHandler) resolveRole(r *http.Request) string {
	if h.deps.ResolveRole != nil {
		return h.deps.ResolveRole(r)
	}
	return "admin"
}

// requireAdmin checks that the request comes from an admin user.
// Returns true if authorized, false if it wrote a 403 response.
func (h *agentHandler) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	if h.deps.AppConfig != nil && h.deps.AppConfig.WickGatewayURL != "" {
		if h.resolveRole(r) != "admin" {
			writeJSONError(w, http.StatusForbidden, "admin role required")
			return false
		}
	}
	return true
}

// --- CRUD ---

func (h *agentHandler) listAgents(w http.ResponseWriter, r *http.Request) {
	username := h.resolveUsername(r)
	agents := h.deps.Registry.ListAgents(username)
	if agents == nil {
		agents = []agent.AgentInfo{}
	}
	// Enrich with container status from backend store
	for i := range agents {
		b := h.deps.Backends.Get(agents[i].AgentID, username)
		if b != nil {
			if s := b.ContainerStatus(); s != "" {
				agents[i].ContainerStatus = &s
			}
			if e := b.ContainerError(); e != "" {
				agents[i].ContainerError = &e
			}
		}
	}
	writeJSON(w, http.StatusOK, agents)
}

func (h *agentHandler) getAgent(w http.ResponseWriter, r *http.Request, agentID string) {
	username := h.resolveUsername(r)
	inst, err := h.deps.Registry.GetOrClone(agentID, username)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, err.Error())
		return
	}
	// Ensure agent is built so hooks are populated in the response
	if _, err := h.buildAgent(inst, username); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	info := buildAgentInfo(inst, h.deps.Backends)
	writeJSON(w, http.StatusOK, info)
}

func (h *agentHandler) createAgent(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}

	var req struct {
		AgentID       string              `json:"agent_id"`
		Name          string              `json:"name"`
		Model         any                 `json:"model"`
		SystemPrompt  string              `json:"system_prompt"`
		Tools         []string            `json:"tools"`
		Middleware    []string             `json:"middleware"`
		Subagents    []agent.SubAgentCfg   `json:"subagents"`
		Backend      *agent.BackendCfg     `json:"backend"`
		Skills       *agent.SkillsCfg      `json:"skills"`
		Memory       *agent.MemoryCfg      `json:"memory"`
		Debug         bool                 `json:"debug"`
		ContextWindow int                  `json:"context_window"`
		BuiltinConfig map[string]string    `json:"builtin_config"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	cfg := &agent.AgentConfig{
		Name:          req.Name,
		Model:         req.Model,
		SystemPrompt:  req.SystemPrompt,
		Tools:         req.Tools,
		Middleware:    req.Middleware,
		Subagents:    req.Subagents,
		Backend:      req.Backend,
		Skills:       req.Skills,
		Memory:       req.Memory,
		Debug:         req.Debug,
		ContextWindow: req.ContextWindow,
		BuiltinConfig: req.BuiltinConfig,
	}

	username := h.resolveUsername(r)
	h.deps.Registry.RegisterTemplate(req.AgentID, cfg)
	inst, _ := h.deps.Registry.GetOrClone(req.AgentID, username)

	// Eagerly initialize backend so container_status is available immediately
	if cfg.Backend != nil && h.deps.Backends.Get(req.AgentID, username) == nil {
		var b backend.Backend
		switch cfg.Backend.Type {
		case "local":
			workdir := cfg.Backend.Workdir
			if workdir == "" {
				workdir = "/workspace"
			}
			b = backend.NewLocalBackend(workdir, cfg.Backend.Timeout, cfg.Backend.MaxOutputBytes, username)
		case "docker":
			containerName := cfg.Backend.ContainerName
			if containerName == "" {
				containerName = fmt.Sprintf("wick-sandbox-%s", username)
			}
			db := backend.NewDockerBackend(
				containerName, cfg.Backend.Workdir,
				cfg.Backend.Timeout, cfg.Backend.MaxOutputBytes,
				cfg.Backend.DockerHost, cfg.Backend.Image, username,
			)
			db.LaunchContainerAsync(func(event, user string) {
				h.deps.EventBus.Broadcast(event + ":" + user)
			})
			b = db
		}
		if b != nil {
			h.deps.Backends.Set(req.AgentID, username, b)
		}
	}

	writeJSON(w, http.StatusCreated, buildAgentInfo(inst, h.deps.Backends))
}

func (h *agentHandler) deleteAgent(w http.ResponseWriter, r *http.Request, agentID string) {
	if !h.requireAdmin(w, r) {
		return
	}

	username := h.resolveUsername(r)
	// Clean up backend if present
	h.deps.Backends.Remove(agentID, username)
	if err := h.deps.Registry.DeleteInstance(agentID, username); err != nil {
		writeJSONError(w, http.StatusNotFound, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- Invoke ---

type invokeRequest struct {
	Messages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
	ThreadID *string `json:"thread_id"`
	Trace    bool    `json:"trace"`
}

// validateAndConvertMessages checks that all user-submitted messages have an
// allowed role and returns them as agent.Message. Only "user" and "system" are
// accepted — "assistant" and "tool" are internal roles created by the agent loop.
func validateAndConvertMessages(msgs []struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}) ([]agent.Message, error) {
	chain := make(agent.Messages, len(msgs))
	for i, m := range msgs {
		chain[i] = agent.Message{Role: m.Role, Content: m.Content}
	}
	if err := chain.ValidateUserInput(); err != nil {
		return nil, err
	}
	return chain.Slice(), nil
}

func (h *agentHandler) invoke(w http.ResponseWriter, r *http.Request, agentID *string) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req invokeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	username := h.resolveUsername(r)
	resolvedID := "default"
	if agentID != nil {
		resolvedID = *agentID
	}

	inst, err := h.deps.Registry.GetOrClone(resolvedID, username)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, err.Error())
		return
	}

	// Validate and convert messages
	msgs, err := validateAndConvertMessages(req.Messages)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Build agent
	a, buildErr := h.buildAgent(inst, username)
	if buildErr != nil {
		writeJSONError(w, http.StatusInternalServerError, buildErr.Error())
		return
	}

	threadID := fmt.Sprintf("%d", time.Now().UnixNano())
	if req.ThreadID != nil {
		threadID = *req.ThreadID
	}

	// Create trace
	trace := tracing.NewTrace(resolvedID, threadID, inst.Config.ModelStr(), "invoke", len(msgs))
	ctx := tracing.WithTrace(r.Context(), trace)

	state, err := a.Run(ctx, msgs, threadID)

	// Finalize trace
	trace.Finish(err)
	if state != nil {
		trace.Output = map[string]any{"message_count": len(state.Messages)}
	}
	h.deps.TraceStore.Put(trace)

	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Extract response
	response := ""
	if len(state.Messages) > 0 {
		last := state.Messages[len(state.Messages)-1]
		if last.Role == "assistant" {
			response = last.Content
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"trace_id":            trace.TraceID,
		"thread_id":           state.ThreadID,
		"response":            response,
		"tool_calls":          []any{},
		"todos":               state.Todos,
		"files":               state.Files,
		"structured_response": nil,
		"interrupted":         false,
		"interrupted_tool":    nil,
		"interrupted_args":    nil,
	})
}

// --- Stream (SSE) ---

func (h *agentHandler) stream(w http.ResponseWriter, r *http.Request, agentID *string) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req invokeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	username := h.resolveUsername(r)
	resolvedID := "default"
	if agentID != nil {
		resolvedID = *agentID
	}

	// Validate before SSE headers are sent (NewWriter commits 200)
	msgs, err := validateAndConvertMessages(req.Messages)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	inst, err := h.deps.Registry.GetOrClone(resolvedID, username)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, err.Error())
		return
	}

	// Create SSE writer
	sseWriter := sse.NewWriter(w)
	if sseWriter == nil {
		writeJSONError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	// Build agent
	a, buildErr := h.buildAgent(inst, username)
	if buildErr != nil {
		writeJSONError(w, http.StatusInternalServerError, buildErr.Error())
		return
	}

	threadID := fmt.Sprintf("%d", time.Now().UnixNano())
	if req.ThreadID != nil {
		threadID = *req.ThreadID
	}

	// Create trace
	trace := tracing.NewTrace(resolvedID, threadID, inst.Config.ModelStr(), "stream", len(msgs))
	ctx := tracing.WithTrace(r.Context(), trace)

	startTime := time.Now()

	// Run agent in goroutine, stream events to SSE
	eventCh := make(chan agent.StreamEvent, 64)
	go a.RunStream(ctx, msgs, threadID, eventCh)

	for evt := range eventCh {
		// The frontend parses JSON from the data field.
		// Events must match the exact format from useAgentStream.ts analysis.
		switch evt.Event {
		case "on_chat_model_stream":
			sseWriter.SendEvent("on_chat_model_stream", map[string]any{
				"event": "on_chat_model_stream",
				"name":  evt.Name,
				"data":  evt.Data,
			})

		case "on_chat_model_start":
			sseWriter.SendEvent("on_chat_model_start", map[string]any{
				"event": "on_chat_model_start",
				"name":  evt.Name,
			})

		case "on_chat_model_end":
			sseWriter.SendEvent("on_chat_model_end", map[string]any{
				"event": "on_chat_model_end",
				"name":  evt.Name,
			})

		case "on_tool_start":
			sseWriter.SendEvent("on_tool_start", map[string]any{
				"event":  "on_tool_start",
				"name":   evt.Name,
				"run_id": evt.RunID,
				"data":   evt.Data,
			})

		case "on_tool_end":
			sseWriter.SendEvent("on_tool_end", map[string]any{
				"event":  "on_tool_end",
				"name":   evt.Name,
				"run_id": evt.RunID,
				"data":   evt.Data,
			})

		case "done":
			trace.Finish(nil)
			h.deps.TraceStore.Put(trace)
			elapsed := time.Since(startTime).Milliseconds()
			sseWriter.SendEvent("done", map[string]any{
				"trace_id":          trace.TraceID,
				"thread_id":         threadID,
				"total_duration_ms": elapsed,
			})

		case "error":
			trace.Finish(fmt.Errorf("%v", evt.Data))
			h.deps.TraceStore.Put(trace)
			sseWriter.SendEvent("error", evt.Data)
		}
	}
}

// --- Resume ---

func (h *agentHandler) resume(w http.ResponseWriter, r *http.Request, agentID *string) {
	// Placeholder — human-in-the-loop not yet implemented
	writeJSONError(w, http.StatusNotImplemented, "resume not yet implemented")
}

// --- Tools / Backend ---

func (h *agentHandler) availableSkills(w http.ResponseWriter, r *http.Request) {
	type skillEntry struct {
		Name          string   `json:"name"`
		Description   string   `json:"description"`
		SamplePrompts []string `json:"sample_prompts"`
		Icon          string   `json:"icon"`
	}

	// Collect all skill paths from agent configs via registry.
	seen := map[string]bool{}
	var skillDirs []string
	for _, cfg := range h.deps.Registry.AllConfigs() {
		if cfg.Skills == nil {
			continue
		}
		for _, s := range cfg.Skills.Paths {
			if seen[s] {
				continue
			}
			seen[s] = true
			skillDirs = append(skillDirs, s)
		}
	}

	var skills []skillEntry
	for _, dir := range skillDirs {
		absDir := dir
		if !filepath.IsAbs(dir) {
			// Relative paths are not supported without a config file — skip
			log.Printf("skills: skipping relative path %q (requires absolute path from Python caller)", dir)
			continue
		}
		// dir is a parent folder containing subdirectories, one per skill
		entries, err := os.ReadDir(absDir)
		if err != nil {
			log.Printf("skills: cannot read %s: %v", absDir, err)
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			skillFile := filepath.Join(absDir, entry.Name(), "SKILL.md")
			data, err := os.ReadFile(skillFile)
			if err != nil {
				continue
			}
			skill := parseSkillFrontmatter(string(data))
			if skill.Name == "" {
				skill.Name = entry.Name()
			}
			skills = append(skills, skill)
		}
	}

	if skills == nil {
		skills = []skillEntry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"skills": skills})
}

// parseSkillFrontmatter extracts name, description, icon, sample-prompts
// from a SKILL.md YAML frontmatter block (between --- delimiters).
func parseSkillFrontmatter(content string) struct {
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	SamplePrompts []string `json:"sample_prompts"`
	Icon          string   `json:"icon"`
} {
	type entry = struct {
		Name          string   `json:"name"`
		Description   string   `json:"description"`
		SamplePrompts []string `json:"sample_prompts"`
		Icon          string   `json:"icon"`
	}

	result := entry{SamplePrompts: []string{}}

	// Extract frontmatter between --- delimiters
	parts := strings.SplitN(content, "---", 3)
	if len(parts) < 3 {
		return result
	}
	fm := strings.TrimSpace(parts[1])

	// Simple line-by-line parse (avoids full YAML dependency for this)
	lines := strings.Split(fm, "\n")
	var currentKey string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		// List item under a key
		if strings.HasPrefix(trimmed, "- ") && currentKey == "sample-prompts" {
			result.SamplePrompts = append(result.SamplePrompts, strings.TrimPrefix(trimmed, "- "))
			continue
		}
		// Key: value
		if idx := strings.Index(line, ":"); idx > 0 && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			key := strings.TrimSpace(line[:idx])
			val := strings.TrimSpace(line[idx+1:])
			// Handle YAML multiline > indicator
			if val == ">" || val == "|" {
				currentKey = key
				continue
			}
			currentKey = key
			switch key {
			case "name":
				result.Name = val
			case "description":
				result.Description = val
			case "icon":
				result.Icon = val
			}
		} else if currentKey == "description" && (strings.HasPrefix(line, "  ") || strings.HasPrefix(line, "\t")) {
			// Continuation of multiline description
			if result.Description != "" {
				result.Description += " "
			}
			result.Description += trimmed
		}
	}

	return result
}

func (h *agentHandler) availableHooks(w http.ResponseWriter, r *http.Request) {
	type hookEntry struct {
		Name         string   `json:"name"`
		Description  string   `json:"description"`
		Phases       []string `json:"phases"`
		Configurable bool     `json:"configurable"`
		Tools        []string `json:"tools"`
	}

	hooksInfo := []hookEntry{
		{Name: "tracing", Description: "Records timed spans for LLM and tool calls", Phases: []string{"wrap_model_call", "wrap_tool_call"}, Configurable: false, Tools: []string{}},
		{Name: "todolist", Description: "Tracks task progress via a write_todos tool", Phases: []string{"before_agent"}, Configurable: false, Tools: []string{"write_todos"}},
		{Name: "filesystem", Description: "Registers file-operation tools (ls, read, write, edit, glob, grep, execute)", Phases: []string{"before_agent", "wrap_tool_call"}, Configurable: false, Tools: []string{"ls", "read_file", "write_file", "edit_file", "glob", "grep", "execute"}},
		{Name: "skills", Description: "Discovers SKILL.md files and injects catalog into system prompt", Phases: []string{"before_agent", "modify_request"}, Configurable: true, Tools: []string{}},
		{Name: "memory", Description: "Loads AGENTS.md memory files and injects into system prompt", Phases: []string{"before_agent", "modify_request"}, Configurable: true, Tools: []string{}},
		{Name: "summarization", Description: "Compresses conversation when context window nears capacity", Phases: []string{"wrap_model_call"}, Configurable: false, Tools: []string{}},
	}

	writeJSON(w, http.StatusOK, map[string]any{"hooks": hooksInfo})
}

func (h *agentHandler) availableTools(w http.ResponseWriter, r *http.Request) {
	type toolEntry struct {
		Name   string `json:"name"`
		Source string `json:"source"` // "builtin", or hook name e.g. "filesystem", "todolist"
	}

	tools := []toolEntry{
		// Builtin tools (always available, not tied to any hook)
		{Name: "calculate", Source: "builtin"},
		{Name: "current_datetime", Source: "builtin"},
		// internet_search availability depends on per-agent builtin_config.tavily_api_key
		{Name: "internet_search", Source: "builtin"},
	}

	// Hook-provided tools (system tools — controlled via hook toggles)
	tools = append(tools,
		toolEntry{Name: "ls", Source: "filesystem"},
		toolEntry{Name: "read_file", Source: "filesystem"},
		toolEntry{Name: "write_file", Source: "filesystem"},
		toolEntry{Name: "edit_file", Source: "filesystem"},
		toolEntry{Name: "glob", Source: "filesystem"},
		toolEntry{Name: "grep", Source: "filesystem"},
		toolEntry{Name: "execute", Source: "filesystem"},
		toolEntry{Name: "write_todos", Source: "todolist"},
	)

	// External tools registered via HTTP callback
	if h.deps.ExternalTools != nil {
		for _, name := range h.deps.ExternalTools.Names() {
			tools = append(tools, toolEntry{Name: name, Source: "external"})
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"tools": tools})
}

func (h *agentHandler) registerTool(w http.ResponseWriter, r *http.Request) {
	if h.deps.ExternalTools == nil {
		writeJSONError(w, http.StatusInternalServerError, "external tools not initialized")
		return
	}

	var req struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		Parameters  map[string]any `json:"parameters"`
		CallbackURL string         `json:"callback_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Name == "" {
		writeJSONError(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.CallbackURL == "" {
		writeJSONError(w, http.StatusBadRequest, "callback_url is required")
		return
	}

	tool := agent.NewHTTPTool(req.Name, req.Description, req.Parameters, req.CallbackURL)
	h.deps.ExternalTools.Register(tool)
	h.deps.Registry.InvalidateAllAgents()

	log.Printf("Registered external tool %q (callback: %s)", req.Name, req.CallbackURL)
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "registered",
		"name":   req.Name,
	})
}

func (h *agentHandler) deregisterTool(w http.ResponseWriter, r *http.Request, name string) {
	if h.deps.ExternalTools == nil {
		writeJSONError(w, http.StatusInternalServerError, "external tools not initialized")
		return
	}

	if !h.deps.ExternalTools.Remove(name) {
		writeJSONError(w, http.StatusNotFound, fmt.Sprintf("tool %q not found", name))
		return
	}
	h.deps.Registry.InvalidateAllAgents()

	log.Printf("Deregistered external tool %q", name)
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "deregistered",
		"name":   name,
	})
}

// messagesTest exercises the Messages chain primitives and returns the results.
// GET /agents/messages/test — no LLM needed, pure unit test of message types.
func (h *agentHandler) messagesTest(w http.ResponseWriter, r *http.Request) {
	// 1. Build a chain using constructors
	chain := agent.NewMessages().
		System("You are helpful.").
		Human("What is 2+2?").
		AI("Let me calculate.", agent.ToolCall{ID: "call_1", Name: "calculate", Args: map[string]any{"expression": "2+2"}}).
		Tool("call_1", "calculate", "4").
		AI("2+2 = 4")

	// 2. Validate the full chain
	validateErr := ""
	if err := chain.Validate(); err != nil {
		validateErr = err.Error()
	}

	// 3. Test filtering
	userMsgs := chain.UserMessages()
	aiMsgs := chain.AssistantMessages()
	toolMsgs := chain.ToolMessages()
	sysMsgs := chain.SystemMessages()

	// 4. Test validation rejects bad input
	badChain := agent.NewMessages(agent.Message{Role: "hacker", Content: "inject"})
	badErr := ""
	if err := badChain.Validate(); err != nil {
		badErr = err.Error()
	}

	// 5. Test user input validation
	spoofChain := agent.NewMessages().Human("ok")
	spoofChain = append(spoofChain, agent.AI("spoofed"))
	userInputErr := ""
	if err := spoofChain.ValidateUserInput(); err != nil {
		userInputErr = err.Error()
	}

	// 6. Concat
	chain2 := agent.NewMessages().Human("follow-up question")
	merged := chain.Concat(chain2)

	// 7. Token estimate
	tokens := chain.EstimateTokens()

	writeJSON(w, http.StatusOK, map[string]any{
		"chain_length":    chain.Len(),
		"messages":        chain.Slice(),
		"pretty_print":    chain.PrettyPrint(),
		"last_content":    chain.LastContent(),
		"last_role":       chain.Last().Role,
		"validate_ok":     validateErr == "",
		"validate_error":  validateErr,
		"user_count":      userMsgs.Len(),
		"assistant_count": aiMsgs.Len(),
		"tool_count":      toolMsgs.Len(),
		"system_count":    sysMsgs.Len(),
		"bad_role_error":  badErr,
		"spoof_error":     userInputErr,
		"merged_length":   merged.Len(),
		"token_estimate":  tokens,
	})
}

func (h *agentHandler) patchBackend(w http.ResponseWriter, r *http.Request, agentID string) {
	if r.Method != http.MethodPatch {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if !h.requireAdmin(w, r) {
		return
	}

	var body struct {
		Mode       string  `json:"mode"`        // "local" or "remote"
		SandboxURL *string `json:"sandbox_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	username := h.resolveUsername(r)
	inst, err := h.deps.Registry.GetOrClone(agentID, username)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, err.Error())
		return
	}

	// Clean up old backend
	h.deps.Backends.Remove(agentID, username)

	var b backend.Backend
	containerStatus := ""

	// Read defaults from agent config backend (if set by Python caller)
	cfgBackend := inst.Config.Backend
	defaultWorkdir := "/workspace"
	defaultTimeout := 120.0
	defaultMaxOutput := 100_000
	defaultImage := "python:3.11-slim"
	defaultContainerName := fmt.Sprintf("wick-sandbox-%s", username)
	if cfgBackend != nil {
		if cfgBackend.Workdir != "" {
			defaultWorkdir = cfgBackend.Workdir
		}
		if cfgBackend.Timeout > 0 {
			defaultTimeout = cfgBackend.Timeout
		}
		if cfgBackend.MaxOutputBytes > 0 {
			defaultMaxOutput = cfgBackend.MaxOutputBytes
		}
		if cfgBackend.Image != "" {
			defaultImage = cfgBackend.Image
		}
		if cfgBackend.ContainerName != "" {
			defaultContainerName = cfgBackend.ContainerName
		}
	}

	switch body.Mode {
	case "local":
		b = backend.NewLocalBackend(defaultWorkdir, defaultTimeout, defaultMaxOutput, username)
		inst.Config.Backend = &agent.BackendCfg{Type: "local", Workdir: defaultWorkdir}
		containerStatus = "launched"

	case "remote":
		dockerHost := ""
		if body.SandboxURL != nil {
			dockerHost = *body.SandboxURL
		}
		db := backend.NewDockerBackend(defaultContainerName, defaultWorkdir, defaultTimeout, defaultMaxOutput, dockerHost, defaultImage, username)
		b = db
		inst.Config.Backend = &agent.BackendCfg{Type: "docker", DockerHost: dockerHost, ContainerName: defaultContainerName}

		// Async container launch
		db.LaunchContainerAsync(func(event, user string) {
			h.deps.EventBus.Broadcast(event + ":" + user)
		})
		containerStatus = "launching"

	default:
		writeJSONError(w, http.StatusBadRequest, "mode must be 'local' or 'remote'")
		return
	}

	h.deps.Backends.Set(agentID, username, b)
	inst.Agent = nil // force rebuild

	sandboxURL := ""
	if inst.Config.Backend != nil {
		sandboxURL = inst.Config.Backend.DockerHost
	}

	result := map[string]any{
		"agent_id":         agentID,
		"sandbox_url":      nilIfEmpty(sandboxURL),
		"backend_type":     inst.Config.Backend.Type,
		"container_status": nilIfEmpty(containerStatus),
		"container_error":  nil,
	}
	writeJSON(w, http.StatusOK, result)
}

// --- Terminal WebSocket ---

var wsUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func (h *agentHandler) terminal(w http.ResponseWriter, r *http.Request, agentID string) {
	username := h.resolveUsername(r)
	b := h.deps.Backends.Get(agentID, username)
	if b == nil {
		http.Error(w, "agent has no backend", http.StatusBadRequest)
		return
	}

	if b.ContainerStatus() != "launched" {
		http.Error(w, "container not launched", http.StatusBadRequest)
		return
	}

	cmdArgs := b.TerminalCmd()
	if len(cmdArgs) == 0 {
		http.Error(w, "terminal not supported", http.StatusBadRequest)
		return
	}

	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("terminal: websocket upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	cmd := exec.Command(cmdArgs[0], cmdArgs[1:]...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		conn.WriteMessage(websocket.TextMessage, []byte("Error: "+err.Error()))
		return
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		conn.WriteMessage(websocket.TextMessage, []byte("Error: "+err.Error()))
		return
	}
	cmd.Stderr = cmd.Stdout // merge stderr into stdout

	if err := cmd.Start(); err != nil {
		conn.WriteMessage(websocket.TextMessage, []byte("Error: "+err.Error()))
		return
	}

	done := make(chan struct{})

	// stdout → websocket
	go func() {
		defer close(done)
		buf := make([]byte, 4096)
		for {
			n, err := stdout.Read(buf)
			if n > 0 {
				if werr := conn.WriteMessage(websocket.BinaryMessage, buf[:n]); werr != nil {
					break
				}
			}
			if err != nil {
				break
			}
		}
	}()

	// websocket → stdin
	go func() {
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				break
			}
			if _, err := io.WriteString(stdin, string(msg)); err != nil {
				break
			}
		}
		stdin.Close()
	}()

	<-done
	cmd.Process.Kill()
	cmd.Wait()
}

// --- Files ---

func (h *agentHandler) listFiles(w http.ResponseWriter, r *http.Request, agentID string) {
	path := r.URL.Query().Get("path")

	username := h.resolveUsername(r)
	b := h.deps.Backends.Get(agentID, username)
	if b == nil {
		writeJSONError(w, http.StatusBadRequest, "agent has no executable backend")
		return
	}

	resolved, err := b.ResolvePath(path)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	cmd := backend.LsCommand(resolved)
	result := b.Execute(cmd)

	var entries []map[string]any
	if result.ExitCode == 0 && strings.TrimSpace(result.Output) != "" {
		for _, line := range strings.Split(strings.TrimSpace(result.Output), "\n") {
			parts := strings.SplitN(line, "\t", 3)
			if len(parts) == 3 {
				name := parts[0]
				if idx := strings.LastIndex(name, "/"); idx >= 0 {
					name = name[idx+1:]
				}
				ftype := "file"
				if strings.Contains(strings.ToLower(parts[1]), "directory") {
					ftype = "dir"
				}
				size := 0
				fmt.Sscanf(parts[2], "%d", &size)
				entries = append(entries, map[string]any{
					"name": name,
					"path": parts[0],
					"type": ftype,
					"size": size,
				})
			}
		}
	}
	if entries == nil {
		entries = []map[string]any{}
	}

	writeJSON(w, http.StatusOK, map[string]any{"path": resolved, "entries": entries})
}

func (h *agentHandler) readFile(w http.ResponseWriter, r *http.Request, agentID string) {
	path := r.URL.Query().Get("path")

	username := h.resolveUsername(r)
	b := h.deps.Backends.Get(agentID, username)
	if b == nil {
		writeJSONError(w, http.StatusBadRequest, "agent has no executable backend")
		return
	}

	resolved, err := b.ResolvePath(path)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	result := b.Execute(fmt.Sprintf("cat '%s'", strings.ReplaceAll(resolved, "'", "'\\''")))
	if result.ExitCode != 0 {
		writeJSONError(w, http.StatusNotFound, "file not found: "+resolved)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"path": resolved, "content": result.Output})
}

func (h *agentHandler) downloadFile(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	agentIDParam := r.URL.Query().Get("agent_id")
	if agentIDParam == "" {
		agentIDParam = "default"
	}

	username := h.resolveUsername(r)
	b := h.deps.Backends.Get(agentIDParam, username)
	if b == nil {
		writeJSONError(w, http.StatusBadRequest, "agent does not have a backend that supports file downloads")
		return
	}

	resolved, err := b.ResolvePath(path)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	results := b.DownloadFiles([]string{resolved})
	if len(results) == 0 || results[0].Error != "" {
		writeJSONError(w, http.StatusNotFound, "file not found: "+resolved)
		return
	}

	filename := resolved
	if idx := strings.LastIndex(resolved, "/"); idx >= 0 {
		filename = resolved[idx+1:]
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.Write(results[0].Content)
}

func (h *agentHandler) uploadFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Path    string `json:"path"`
		Content string `json:"content"`
		AgentID string `json:"agent_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	agentIDVal := req.AgentID
	if agentIDVal == "" {
		agentIDVal = "default"
	}

	username := h.resolveUsername(r)
	b := h.deps.Backends.Get(agentIDVal, username)
	if b == nil {
		writeJSONError(w, http.StatusBadRequest, "agent does not have a backend that supports file uploads")
		return
	}

	resolved, err := b.ResolvePath(req.Path)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	content := []byte(req.Content)
	results := b.UploadFiles([]backend.FileUpload{{Path: resolved, Content: content}})
	if len(results) > 0 && results[0].Error != "" {
		writeJSONError(w, http.StatusInternalServerError, "upload failed: "+results[0].Error)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"path":   resolved,
		"size":   len(content),
	})
}

// --- Events (SSE relay) ---

func (h *agentHandler) agentEvents(w http.ResponseWriter, r *http.Request) {
	sseWriter := sse.NewWriter(w)
	if sseWriter == nil {
		writeJSONError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	username := h.resolveUsername(r)
	ch := h.deps.EventBus.Subscribe()
	defer h.deps.EventBus.Unsubscribe(ch)

	ctx := r.Context()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case raw := <-ch:
			// Events are "event_name:username" or plain "event_name"
			if strings.Contains(raw, ":") {
				parts := strings.SplitN(raw, ":", 2)
				if parts[1] != username {
					continue // skip other users' events
				}
				sseWriter.SendEvent(parts[0], map[string]string{})
			} else {
				sseWriter.SendEvent(raw, map[string]string{})
			}
		case <-ticker.C:
			sseWriter.SendComment("keep-alive")
		}
	}
}

// --- Traces ---

func (h *agentHandler) traces(w http.ResponseWriter, r *http.Request, sub string) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sub = strings.TrimPrefix(sub, "/")

	// GET /agents/traces — list recent
	if sub == "" {
		limit := 50
		if l := r.URL.Query().Get("limit"); l != "" {
			if n, err := strconv.Atoi(l); err == nil && n > 0 {
				limit = n
			}
		}
		writeJSON(w, http.StatusOK, h.deps.TraceStore.List(limit))
		return
	}

	// GET /agents/traces/{trace_id}
	t := h.deps.TraceStore.Get(sub)
	if t == nil {
		writeJSONError(w, http.StatusNotFound, "trace not found")
		return
	}
	writeJSON(w, http.StatusOK, t)
}

// --- Flow & Hooks ---

func (h *agentHandler) getFlow(w http.ResponseWriter, r *http.Request, agentID string) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	username := h.resolveUsername(r)
	inst, err := h.deps.Registry.GetOrClone(agentID, username)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, err.Error())
		return
	}

	// Ensure agent is built so hooks are populated
	a, buildErr := h.buildAgent(inst, username)
	if buildErr != nil {
		writeJSONError(w, http.StatusInternalServerError, buildErr.Error())
		return
	}

	// Build hook info
	type hookInfo struct {
		Name   string   `json:"name"`
		Phases []string `json:"phases"`
	}
	var hooksInfo []hookInfo
	for _, hook := range a.Hooks {
		hooksInfo = append(hooksInfo, hookInfo{
			Name:   hook.Name(),
			Phases: hook.Phases(),
		})
	}

	// Collect tool names
	toolNames := make([]string, 0, len(a.Tools))
	for _, t := range a.Tools {
		toolNames = append(toolNames, t.Name())
	}

	// Build flow steps
	buildPhaseHooks := func(phase string) []string {
		var names []string
		for _, hook := range a.Hooks {
			for _, p := range hook.Phases() {
				if p == phase {
					names = append(names, hook.Name())
					break
				}
			}
		}
		return names
	}

	maxIter := agent.MaxIterations
	flow := []map[string]any{
		{"step": "before_agent", "hooks": buildPhaseHooks("before_agent")},
		{"step": "loop_start", "max_iterations": maxIter},
		{"step": "modify_request", "hooks": buildPhaseHooks("modify_request")},
		{"step": "model_call", "wraps": buildPhaseHooks("wrap_model_call"), "model": inst.Config.ModelStr()},
		{"step": "tool_execution", "wraps": buildPhaseHooks("wrap_tool_call"), "parallel": true},
		{"step": "loop_end", "exit_when": "no_tool_calls"},
		{"step": "done"},
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"agent_id":       agentID,
		"max_iterations": maxIter,
		"model":          inst.Config.ModelStr(),
		"hooks":          hooksInfo,
		"tools":          toolNames,
		"flow":           flow,
	})
}

func (h *agentHandler) patchHooks(w http.ResponseWriter, r *http.Request, agentID string) {
	if r.Method != http.MethodPatch {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if !h.requireAdmin(w, r) {
		return
	}

	var body struct {
		Add    []string       `json:"add"`
		Remove []string       `json:"remove"`
		Config map[string]any `json:"config"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	// Validate hook names
	knownHooks := map[string]bool{
		"tracing": true, "todolist": true, "filesystem": true,
		"skills": true, "memory": true, "summarization": true,
	}
	for _, name := range body.Add {
		if !knownHooks[name] {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("unknown hook: %q", name))
			return
		}
	}
	for _, name := range body.Remove {
		if !knownHooks[name] {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("unknown hook: %q", name))
			return
		}
	}

	username := h.resolveUsername(r)
	inst, err := h.deps.Registry.GetOrClone(agentID, username)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, err.Error())
		return
	}

	// Build new overrides by merging with existing (copy to avoid races)
	var overrides agent.HookOverrides
	if inst.HookOverrides != nil {
		overrides = *inst.HookOverrides
		// Deep-copy slices to avoid aliasing
		overrides.Remove = append([]string{}, inst.HookOverrides.Remove...)
		overrides.Add = append([]string{}, inst.HookOverrides.Add...)
		if inst.HookOverrides.Config != nil {
			overrides.Config = make(map[string]any, len(inst.HookOverrides.Config))
			for k, v := range inst.HookOverrides.Config {
				overrides.Config[k] = v
			}
		}
	}
	if len(body.Add) > 0 {
		overrides.Add = mergeStringSlice(overrides.Add, body.Add)
		// If adding something previously removed, un-remove it
		overrides.Remove = removeFromSlice(overrides.Remove, body.Add)
	}
	if len(body.Remove) > 0 {
		overrides.Remove = mergeStringSlice(overrides.Remove, body.Remove)
		// If removing something previously added, un-add it
		overrides.Add = removeFromSlice(overrides.Add, body.Remove)
	}
	if body.Config != nil {
		if overrides.Config == nil {
			overrides.Config = make(map[string]any)
		}
		for k, v := range body.Config {
			overrides.Config[k] = v
		}
	}

	// Atomically update overrides and force agent rebuild
	if err := h.deps.Registry.UpdateHookOverrides(agentID, username, &overrides); err != nil {
		writeJSONError(w, http.StatusNotFound, err.Error())
		return
	}

	// Rebuild agent to get updated hooks
	a, buildErr := h.buildAgent(inst, username)
	if buildErr != nil {
		writeJSONError(w, http.StatusInternalServerError, buildErr.Error())
		return
	}

	hookNames := make([]string, len(a.Hooks))
	for i, hook := range a.Hooks {
		hookNames[i] = hook.Name()
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"agent_id": agentID,
		"hooks":    hookNames,
	})
}

// mergeStringSlice appends items from b that are not already in a.
func mergeStringSlice(a, b []string) []string {
	set := make(map[string]bool, len(a))
	for _, s := range a {
		set[s] = true
	}
	for _, s := range b {
		if !set[s] {
			a = append(a, s)
			set[s] = true
		}
	}
	return a
}

// removeFromSlice returns a new slice containing elements of a not present in toRemove.
func removeFromSlice(a, toRemove []string) []string {
	set := make(map[string]bool, len(toRemove))
	for _, s := range toRemove {
		set[s] = true
	}
	var result []string
	for _, s := range a {
		if !set[s] {
			result = append(result, s)
		}
	}
	return result
}

// applyHookOverrides filters the default hooks based on overrides and adds extra hooks.
func applyHookOverrides(
	agentHooks []agent.Hook,
	overrides *agent.HookOverrides,
	b backend.Backend,
	llmClient llm.Client,
	cfg *agent.AgentConfig,
) []agent.Hook {
	// Build removal set
	removeSet := make(map[string]bool, len(overrides.Remove))
	for _, name := range overrides.Remove {
		removeSet[name] = true
	}

	// Filter out removed hooks
	var filtered []agent.Hook
	for _, h := range agentHooks {
		if !removeSet[h.Name()] {
			filtered = append(filtered, h)
		}
	}

	// Track what's already present
	present := make(map[string]bool, len(filtered))
	for _, h := range filtered {
		present[h.Name()] = true
	}

	// Add requested hooks that aren't already present
	for _, name := range overrides.Add {
		if present[name] {
			continue
		}
		hook := createHookByName(name, b, llmClient, cfg, overrides.Config)
		if hook != nil {
			filtered = append(filtered, hook)
			present[name] = true
		}
	}

	return filtered
}

// createHookByName creates a hook instance by its name.
func createHookByName(name string, b backend.Backend, llmClient llm.Client, cfg *agent.AgentConfig, hookConfig map[string]any) agent.Hook {
	switch name {
	case "tracing":
		return tracing.NewTracingHook()
	case "todolist":
		return hooks.NewTodoListHook()
	case "filesystem":
		if b != nil {
			return hooks.NewFilesystemHook(b)
		}
	case "skills":
		paths := getConfigPaths(hookConfig, "skills")
		if len(paths) == 0 && cfg.Skills != nil {
			paths = cfg.Skills.Paths
		}
		if len(paths) > 0 && b != nil {
			return hooks.NewSkillsHook(b, paths)
		}
	case "memory":
		paths := getConfigPaths(hookConfig, "memory")
		if len(paths) == 0 && cfg.Memory != nil {
			paths = cfg.Memory.Paths
		}
		if len(paths) > 0 && b != nil {
			return hooks.NewMemoryHook(b, paths)
		}
	case "summarization":
		return hooks.NewSummarizationHook(llmClient, 128_000)
	}
	return nil
}

// getConfigPaths extracts a paths []string from hookConfig[name]["paths"].
func getConfigPaths(hookConfig map[string]any, name string) []string {
	if hookConfig == nil {
		return nil
	}
	raw, ok := hookConfig[name]
	if !ok {
		return nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	rawPaths, ok := m["paths"]
	if !ok {
		return nil
	}
	switch v := rawPaths.(type) {
	case []any:
		paths := make([]string, 0, len(v))
		for _, p := range v {
			if s, ok := p.(string); ok {
				paths = append(paths, s)
			}
		}
		return paths
	case []string:
		return v
	}
	return nil
}

// --- Agent builder ---

func (h *agentHandler) buildAgent(inst *agent.Instance, username string) (*agent.Agent, error) {
	if inst.Agent != nil {
		return inst.Agent, nil
	}

	cfg := inst.Config

	// Resolve LLM client — model spec must be self-contained
	llmClient, _, err := llm.Resolve(cfg.Model)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve model for agent %s: %w", inst.AgentID, err)
	}

	// Resolve backend
	b := h.deps.Backends.Get(inst.AgentID, username)
	if b == nil && cfg.Backend != nil {
		switch cfg.Backend.Type {
		case "local":
			workdir := cfg.Backend.Workdir
			if workdir == "" {
				workdir = "/workspace"
			}
			b = backend.NewLocalBackend(workdir, cfg.Backend.Timeout, cfg.Backend.MaxOutputBytes, username)
		case "docker":
			containerName := cfg.Backend.ContainerName
			if containerName == "" {
				containerName = fmt.Sprintf("wick-sandbox-%s", username)
			}
			b = backend.NewDockerBackend(
				containerName, cfg.Backend.Workdir,
				cfg.Backend.Timeout, cfg.Backend.MaxOutputBytes,
				cfg.Backend.DockerHost, cfg.Backend.Image, username,
			)
		}
		if b != nil {
			h.deps.Backends.Set(inst.AgentID, username, b)
		}
	}

	// Build hooks
	var agentHooks []agent.Hook

	// Tracing hook (outermost — index 0 becomes outermost wrapper)
	agentHooks = append(agentHooks, tracing.NewTracingHook())

	// TodoList hook (always active)
	agentHooks = append(agentHooks, hooks.NewTodoListHook())

	// Filesystem hook (when backend is available)
	if b != nil {
		agentHooks = append(agentHooks, hooks.NewFilesystemHook(b))
	}

	// Skills hook
	if cfg.Skills != nil && len(cfg.Skills.Paths) > 0 && b != nil {
		agentHooks = append(agentHooks, hooks.NewSkillsHook(b, cfg.Skills.Paths))
	}

	// Memory hook
	if cfg.Memory != nil && len(cfg.Memory.Paths) > 0 && b != nil {
		agentHooks = append(agentHooks, hooks.NewMemoryHook(b, cfg.Memory.Paths))
	}

	// Summarization hook — use agent's ContextWindow (default 128k)
	contextWindow := cfg.ContextWindow
	if contextWindow <= 0 {
		contextWindow = 128_000
	}
	agentHooks = append(agentHooks, hooks.NewSummarizationHook(llmClient, contextWindow))

	// Apply hook overrides if configured
	if inst.HookOverrides != nil {
		agentHooks = applyHookOverrides(agentHooks, inst.HookOverrides, b, llmClient, cfg)
	}

	// Build tools (built-in tools from agent config)
	var tools []agent.Tool
	tools = append(tools, newBuiltinTools(cfg)...)

	// Append externally registered tools (HTTP callback tools)
	if h.deps.ExternalTools != nil {
		tools = append(tools, h.deps.ExternalTools.All()...)
	}

	a := agent.NewAgent(inst.AgentID, cfg, llmClient, tools, agentHooks)
	inst.Agent = a
	return a, nil
}

// --- Helpers ---

func buildAgentInfo(inst *agent.Instance, backends *BackendStore) agent.AgentInfo {
	cfg := inst.Config
	info := agent.AgentInfo{
		AgentID:      inst.AgentID,
		Tools:        nonNilStrings(cfg.Tools),
		Subagents:    subagentNames(cfg.Subagents),
		Middleware:   nonNilStrings(cfg.Middleware),
		Hooks:        []string{},
		BackendType:  "state",
		Skills:       []string{},
		LoadedSkills: []string{},
		Memory:       []string{},
		Model:        cfg.ModelStr(),
		Debug:        cfg.Debug,
	}
	// Populate hook names from the live Agent if available
	if inst.Agent != nil {
		for _, h := range inst.Agent.Hooks {
			info.Hooks = append(info.Hooks, h.Name())
		}
	}
	if cfg.Name != "" {
		info.Name = &cfg.Name
	}
	if cfg.SystemPrompt != "" {
		sp := cfg.SystemPrompt
		if len(sp) > 120 {
			sp = sp[:120] + "..."
		}
		info.SystemPrompt = &sp
	}
	if cfg.Backend != nil {
		info.BackendType = cfg.Backend.Type
		if cfg.Backend.DockerHost != "" {
			info.SandboxURL = &cfg.Backend.DockerHost
		}
	}
	if cfg.Skills != nil {
		info.Skills = cfg.Skills.Paths
	}
	if cfg.Memory != nil {
		info.Memory = cfg.Memory.Paths
	}

	// Container status from backend
	if backends != nil {
		b := backends.Get(inst.AgentID, inst.Username)
		if b != nil {
			if s := b.ContainerStatus(); s != "" {
				info.ContainerStatus = &s
			}
			if e := b.ContainerError(); e != "" {
				info.ContainerError = &e
			}
		}
	}

	return info
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func subagentNames(subs []agent.SubAgentCfg) []string {
	names := make([]string, len(subs))
	for i, sa := range subs {
		names[i] = sa.Name
	}
	return names
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
