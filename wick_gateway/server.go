package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"sync"
)

// sessionState holds per-session data (extensible for future use).
type sessionState struct {
	id string
}

// MCPHandler implements the MCP Streamable HTTP server endpoint.
type MCPHandler struct {
	registry *Registry

	mu       sync.RWMutex
	sessions map[string]*sessionState
}

func NewMCPHandler(reg *Registry) *MCPHandler {
	return &MCPHandler{
		registry: reg,
		sessions: make(map[string]*sessionState),
	}
}

func (h *MCPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.handlePost(w, r)
	case http.MethodDelete:
		h.handleDelete(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *MCPHandler) handlePost(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}

	var req JSONRPCRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, http.StatusOK, newErrorResponse(nil, -32700, "Parse error"))
		return
	}

	switch req.Method {
	case "initialize":
		h.handleInitialize(w, &req)
	case "notifications/initialized":
		w.WriteHeader(http.StatusAccepted)
	case "tools/list":
		h.handleToolsList(w, &req)
	case "tools/call":
		h.handleToolsCall(w, &req)
	case "ping":
		h.handlePing(w, &req)
	default:
		writeJSON(w, http.StatusOK, newErrorResponse(req.ID, -32601, "Method not found: "+req.Method))
	}
}

func (h *MCPHandler) handleInitialize(w http.ResponseWriter, req *JSONRPCRequest) {
	sessionID := generateSessionID()

	h.mu.Lock()
	h.sessions[sessionID] = &sessionState{id: sessionID}
	h.mu.Unlock()

	result := InitializeResult{
		ProtocolVersion: "2025-03-26",
		Capabilities:    json.RawMessage(`{"tools":{}}`),
		ServerInfo: ServerInfo{
			Name:    "wick_gateway",
			Version: "1.0.0",
		},
	}

	resp, err := newSuccessResponse(req.ID, result)
	if err != nil {
		writeJSON(w, http.StatusOK, newErrorResponse(req.ID, -32603, "internal error"))
		return
	}

	w.Header().Set("Mcp-Session-Id", sessionID)
	writeJSON(w, http.StatusOK, resp)
}

func (h *MCPHandler) handleToolsList(w http.ResponseWriter, req *JSONRPCRequest) {
	result := ToolsListResult{
		Tools: h.registry.AllTools(),
	}

	resp, err := newSuccessResponse(req.ID, result)
	if err != nil {
		writeJSON(w, http.StatusOK, newErrorResponse(req.ID, -32603, "internal error"))
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *MCPHandler) handleToolsCall(w http.ResponseWriter, req *JSONRPCRequest) {
	var params ToolsCallParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeJSON(w, http.StatusOK, newErrorResponse(req.ID, -32602, "Invalid params: "+err.Error()))
		return
	}

	client := h.registry.Lookup(params.Name)
	if client == nil {
		writeJSON(w, http.StatusOK, newErrorResponse(req.ID, -32602, "Unknown tool: "+params.Name))
		return
	}

	log.Printf("Proxying tools/call %q to %s", params.Name, client.Name)

	result, err := client.CallTool(params.Name, params.Arguments)
	if err != nil {
		log.Printf("tools/call %q error: %v", params.Name, err)
		writeJSON(w, http.StatusOK, newErrorResponse(req.ID, -32603, "Downstream error: "+err.Error()))
		return
	}

	resp := &JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  result,
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *MCPHandler) handlePing(w http.ResponseWriter, req *JSONRPCRequest) {
	resp, _ := newSuccessResponse(req.ID, json.RawMessage(`{}`))
	writeJSON(w, http.StatusOK, resp)
}

func (h *MCPHandler) handleDelete(w http.ResponseWriter, r *http.Request) {
	sessionID := r.Header.Get("Mcp-Session-Id")
	if sessionID != "" {
		h.mu.Lock()
		delete(h.sessions, sessionID)
		h.mu.Unlock()
		log.Printf("Session %s terminated", sessionID)
	}
	w.WriteHeader(http.StatusOK)
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("writeJSON error: %v", err)
	}
}

func generateSessionID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}
