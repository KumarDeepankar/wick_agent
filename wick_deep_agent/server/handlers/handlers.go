package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"wick_go/agent"
	"wick_go/backend"
	"wick_go/hooks"
	"wick_go/llm"
	"wick_go/sse"
)

// Config holds handler-level configuration.
type Config struct {
	WickGatewayURL  string
	DefaultModel    string
	OllamaBaseURL   string
	GatewayBaseURL  string
	GatewayAPIKey   string
	OpenAIAPIKey    string
	AnthropicAPIKey string
	TavilyAPIKey    string
	ConfigPath      string
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
		case "tools":
			h.patchTools(w, r, agentID)
		case "backend":
			h.patchBackend(w, r, agentID)
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

// --- CRUD ---

func (h *agentHandler) listAgents(w http.ResponseWriter, r *http.Request) {
	username := h.resolveUsername(r)
	agents := h.deps.Registry.ListAgents(username)
	if agents == nil {
		agents = []agent.AgentInfo{}
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
	info := buildAgentInfo(inst, h.deps.Backends)
	writeJSON(w, http.StatusOK, info)
}

func (h *agentHandler) createAgent(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AgentID      string   `json:"agent_id"`
		Name         string   `json:"name"`
		Model        any      `json:"model"`
		SystemPrompt string   `json:"system_prompt"`
		Tools        []string `json:"tools"`
		Debug        bool     `json:"debug"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	cfg := &agent.AgentConfig{
		Name:         req.Name,
		Model:        req.Model,
		SystemPrompt: req.SystemPrompt,
		Tools:        req.Tools,
		Debug:        req.Debug,
	}

	username := h.resolveUsername(r)
	h.deps.Registry.RegisterTemplate(req.AgentID, cfg)
	inst, _ := h.deps.Registry.GetOrClone(req.AgentID, username)
	writeJSON(w, http.StatusCreated, buildAgentInfo(inst, h.deps.Backends))
}

func (h *agentHandler) deleteAgent(w http.ResponseWriter, r *http.Request, agentID string) {
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
	a := h.buildAgent(inst, username)

	threadID := fmt.Sprintf("%d", time.Now().UnixNano())
	if req.ThreadID != nil {
		threadID = *req.ThreadID
	}

	state, err := a.Run(r.Context(), msgs, threadID)
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
	a := h.buildAgent(inst, username)

	threadID := fmt.Sprintf("%d", time.Now().UnixNano())
	if req.ThreadID != nil {
		threadID = *req.ThreadID
	}

	startTime := time.Now()

	// Run agent in goroutine, stream events to SSE
	eventCh := make(chan agent.StreamEvent, 64)
	go a.RunStream(r.Context(), msgs, threadID, eventCh)

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
			elapsed := time.Since(startTime).Milliseconds()
			sseWriter.SendEvent("done", map[string]any{
				"thread_id":         threadID,
				"total_duration_ms": elapsed,
			})

		case "error":
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

func (h *agentHandler) availableTools(w http.ResponseWriter, r *http.Request) {
	// Return built-in tool names
	tools := []string{
		"ls", "read_file", "write_file", "edit_file",
		"glob", "grep", "execute",
		"internet_search", "calculate", "current_datetime",
		"write_todos",
	}
	writeJSON(w, http.StatusOK, map[string]any{"tools": tools})
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

func (h *agentHandler) patchTools(w http.ResponseWriter, r *http.Request, agentID string) {
	if r.Method != http.MethodPatch {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Tools []string `json:"tools"`
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

	inst.Config.Tools = body.Tools
	inst.Agent = nil // force rebuild
	writeJSON(w, http.StatusOK, map[string]any{
		"agent_id": agentID,
		"tools":    body.Tools,
	})
}

func (h *agentHandler) patchBackend(w http.ResponseWriter, r *http.Request, agentID string) {
	if r.Method != http.MethodPatch {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
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

	switch body.Mode {
	case "local":
		workdir := "/workspace"
		if inst.Config.Backend != nil && inst.Config.Backend.Workdir != "" {
			workdir = inst.Config.Backend.Workdir
		}
		b = backend.NewLocalBackend(workdir, 120, 100_000, username)
		inst.Config.Backend = &agent.BackendCfg{Type: "local", Workdir: workdir}
		containerStatus = "launched"

	case "remote":
		dockerHost := ""
		if body.SandboxURL != nil {
			dockerHost = *body.SandboxURL
		}
		containerName := fmt.Sprintf("wick-sandbox-%s", username)
		db := backend.NewDockerBackend(containerName, "/workspace", 120, 100_000, dockerHost, "python:3.11-slim", username)
		b = db
		inst.Config.Backend = &agent.BackendCfg{Type: "docker", DockerHost: dockerHost, ContainerName: containerName}

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

// --- Files ---

func (h *agentHandler) listFiles(w http.ResponseWriter, r *http.Request, agentID string) {
	path := r.URL.Query().Get("path")
	if path == "" {
		path = "/workspace"
	}

	username := h.resolveUsername(r)
	b := h.deps.Backends.Get(agentID, username)
	if b == nil {
		writeJSONError(w, http.StatusBadRequest, "agent has no executable backend")
		return
	}

	result := b.Execute(fmt.Sprintf(
		`stat -c "%%n\t%%F\t%%s" %s/* %s/.* 2>/dev/null | grep -v "/\.$" | grep -v "/\.\.$"`,
		path, path,
	))

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

	writeJSON(w, http.StatusOK, map[string]any{"path": path, "entries": entries})
}

func (h *agentHandler) readFile(w http.ResponseWriter, r *http.Request, agentID string) {
	path := r.URL.Query().Get("path")
	if path == "" {
		path = "/workspace"
	}

	username := h.resolveUsername(r)
	b := h.deps.Backends.Get(agentID, username)
	if b == nil {
		writeJSONError(w, http.StatusBadRequest, "agent has no executable backend")
		return
	}

	result := b.Execute(fmt.Sprintf("cat '%s'", strings.ReplaceAll(path, "'", "'\\''")))
	if result.ExitCode != 0 {
		writeJSONError(w, http.StatusNotFound, "file not found: "+path)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"path": path, "content": result.Output})
}

func (h *agentHandler) downloadFile(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	agentIDParam := r.URL.Query().Get("agent_id")
	if path == "" {
		path = "/workspace"
	}
	if agentIDParam == "" {
		agentIDParam = "default"
	}

	username := h.resolveUsername(r)
	b := h.deps.Backends.Get(agentIDParam, username)
	if b == nil {
		writeJSONError(w, http.StatusBadRequest, "agent does not have a backend that supports file downloads")
		return
	}

	results := b.DownloadFiles([]string{path})
	if len(results) == 0 || results[0].Error != "" {
		writeJSONError(w, http.StatusNotFound, "file not found: "+path)
		return
	}

	filename := path
	if idx := strings.LastIndex(path, "/"); idx >= 0 {
		filename = path[idx+1:]
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

	content := []byte(req.Content)
	results := b.UploadFiles([]backend.FileUpload{{Path: req.Path, Content: content}})
	if len(results) > 0 && results[0].Error != "" {
		writeJSONError(w, http.StatusInternalServerError, "upload failed: "+results[0].Error)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"path":   req.Path,
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

// --- Agent builder ---

func (h *agentHandler) buildAgent(inst *agent.Instance, username string) *agent.Agent {
	if inst.Agent != nil {
		return inst.Agent
	}

	cfg := inst.Config

	// Resolve LLM client
	resolverCfg := &llm.ResolverConfig{
		OllamaBaseURL:   h.deps.AppConfig.OllamaBaseURL,
		GatewayBaseURL:  h.deps.AppConfig.GatewayBaseURL,
		GatewayAPIKey:   h.deps.AppConfig.GatewayAPIKey,
		OpenAIAPIKey:    h.deps.AppConfig.OpenAIAPIKey,
		AnthropicAPIKey: h.deps.AppConfig.AnthropicAPIKey,
	}

	llmClient, _, err := llm.Resolve(cfg.Model, resolverCfg)
	if err != nil {
		log.Printf("WARNING: failed to resolve model for agent %s: %v", inst.AgentID, err)
		// Fall back to default
		llmClient, _, _ = llm.Resolve(h.deps.AppConfig.DefaultModel, resolverCfg)
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

	// Summarization hook
	agentHooks = append(agentHooks, hooks.NewSummarizationHook(llmClient, 128_000))

	// Build tools (built-in tools from config)
	var tools []agent.Tool
	tools = append(tools, newBuiltinTools(h.deps.AppConfig)...)

	a := agent.NewAgent(inst.AgentID, cfg, llmClient, tools, agentHooks)
	inst.Agent = a
	return a
}

// --- Helpers ---

func buildAgentInfo(inst *agent.Instance, backends *BackendStore) agent.AgentInfo {
	cfg := inst.Config
	info := agent.AgentInfo{
		AgentID:      inst.AgentID,
		Tools:        nonNilStrings(cfg.Tools),
		Subagents:    subagentNames(cfg.Subagents),
		Middleware:    nonNilStrings(cfg.Middleware),
		BackendType:  "state",
		Skills:       []string{},
		LoadedSkills: []string{},
		Memory:       []string{},
		Model:        cfg.ModelStr(),
		Debug:        cfg.Debug,
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
