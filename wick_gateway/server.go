package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"

	"golang.org/x/crypto/bcrypt"
)

// sessionState holds per-session data (extensible for future use).
type sessionState struct {
	id       string
	username string
}

// MCPHandler implements the MCP Streamable HTTP server endpoint.
type MCPHandler struct {
	registry    *Registry
	auth        *AuthService
	resourceURL string

	mu       sync.RWMutex
	sessions map[string]*sessionState

	sseMu      sync.Mutex
	sseClients map[chan []byte]struct{}

	adminSSEMu      sync.Mutex
	adminSSEClients map[chan []byte]struct{}
}

func NewMCPHandler(reg *Registry, auth *AuthService, resourceURL string) *MCPHandler {
	h := &MCPHandler{
		registry:        reg,
		auth:            auth,
		resourceURL:     resourceURL,
		sessions:        make(map[string]*sessionState),
		sseClients:      make(map[chan []byte]struct{}),
		adminSSEClients: make(map[chan []byte]struct{}),
	}
	reg.OnChange = h.broadcastToolsChanged
	return h
}

// RegisterRoutes adds MCP, auth, and admin API routes to the given mux.
func (h *MCPHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.Handle("/mcp", h)

	// Auth routes
	mux.HandleFunc("/auth/login", h.handleLogin)
	mux.HandleFunc("/auth/oidc/login", h.handleOIDCLogin)
	mux.HandleFunc("/auth/oidc/callback", h.handleOIDCCallback)
	mux.HandleFunc("/auth/me", h.handleAuthMe)

	// OAuth 2.1 / MCP-spec endpoints
	mux.HandleFunc("/.well-known/oauth-protected-resource", h.handleProtectedResourceMetadata)
	mux.HandleFunc("/.well-known/oauth-authorization-server", h.handleAuthServerMetadata)
	mux.HandleFunc("/oauth/token", h.handleOAuthToken)
	mux.HandleFunc("/authorize", h.handleAuthorize)

	// Admin SSE event stream (public — no JWT required)
	mux.HandleFunc("/api/events", h.handleAdminEvents)

	// Admin API routes
	mux.HandleFunc("/api/servers", h.handleAPIServers)
	mux.HandleFunc("/api/servers/", h.handleAPIServerByName)
	mux.HandleFunc("/api/tools", h.handleAPITools)
	mux.HandleFunc("/api/users", h.handleAPIUsers)
	mux.HandleFunc("/api/users/", h.handleAPIUserByName)
	mux.HandleFunc("/api/roles", h.handleAPIRoles)
	mux.HandleFunc("/api/roles/", h.handleAPIRoleByName)

	mux.HandleFunc("/", h.handleUI)
}

func (h *MCPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.handleSSE(w, r)
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
		h.handleInitialize(w, &req, r)
	case "notifications/initialized":
		w.WriteHeader(http.StatusAccepted)
	case "tools/list":
		h.handleToolsList(w, &req, r)
	case "tools/call":
		h.handleToolsCall(w, &req, r)
	case "ping":
		h.handlePing(w, &req)
	default:
		writeJSON(w, http.StatusOK, newErrorResponse(req.ID, -32601, "Method not found: "+req.Method))
	}
}

func (h *MCPHandler) handleInitialize(w http.ResponseWriter, req *JSONRPCRequest, r *http.Request) {
	sessionID := generateSessionID()

	ss := &sessionState{id: sessionID}
	if user := userFromContext(r); user != nil {
		ss.username = user.Username
	}

	h.mu.Lock()
	h.sessions[sessionID] = ss
	h.mu.Unlock()

	result := InitializeResult{
		ProtocolVersion: "2025-03-26",
		Capabilities:    json.RawMessage(`{"tools":{"listChanged":true}}`),
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

func (h *MCPHandler) handleToolsList(w http.ResponseWriter, req *JSONRPCRequest, r *http.Request) {
	tools := h.registry.AllTools()

	if h.auth != nil {
		if user := userFromContext(r); user != nil {
			tools = h.auth.FilterTools(user, tools)
		}
	}

	if tools == nil {
		tools = []Tool{}
	}

	result := ToolsListResult{
		Tools: tools,
	}

	resp, err := newSuccessResponse(req.ID, result)
	if err != nil {
		writeJSON(w, http.StatusOK, newErrorResponse(req.ID, -32603, "internal error"))
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *MCPHandler) handleToolsCall(w http.ResponseWriter, req *JSONRPCRequest, r *http.Request) {
	var params ToolsCallParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeJSON(w, http.StatusOK, newErrorResponse(req.ID, -32602, "Invalid params: "+err.Error()))
		return
	}

	// Check tool authorization.
	if h.auth != nil {
		user := userFromContext(r)
		if user != nil && !h.auth.IsToolAllowed(user, params.Name) {
			writeJSON(w, http.StatusOK, newErrorResponse(req.ID, -32603, "Access denied: tool "+params.Name+" is not allowed for your role"))
			return
		}
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

// --- SSE ---

func (h *MCPHandler) handleSSE(w http.ResponseWriter, r *http.Request) {
	if !strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
		http.Error(w, "Accept header must be text/event-stream", http.StatusBadRequest)
		return
	}

	sessionID := r.Header.Get("Mcp-Session-Id")
	if sessionID == "" {
		http.Error(w, "Mcp-Session-Id header required", http.StatusBadRequest)
		return
	}

	h.mu.RLock()
	_, ok := h.sessions[sessionID]
	h.mu.RUnlock()
	if !ok {
		http.Error(w, "Invalid session", http.StatusNotFound)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	ch := make(chan []byte, 16)
	h.sseMu.Lock()
	h.sseClients[ch] = struct{}{}
	h.sseMu.Unlock()

	defer func() {
		h.sseMu.Lock()
		delete(h.sseClients, ch)
		h.sseMu.Unlock()
	}()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher.Flush()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case data := <-ch:
			fmt.Fprintf(w, "event: message\ndata: %s\n\n", data)
			flusher.Flush()
		}
	}
}

func (h *MCPHandler) broadcastToolsChanged() {
	msg, _ := json.Marshal(map[string]string{
		"jsonrpc": "2.0",
		"method":  "notifications/tools/list_changed",
	})

	h.sseMu.Lock()
	defer h.sseMu.Unlock()

	for ch := range h.sseClients {
		select {
		case ch <- msg:
		default:
			// Drop if buffer full — client is too slow.
		}
	}
}

// --- Admin SSE (config change events for external subscribers) ---

func (h *MCPHandler) broadcastAdminEvent(eventType string) {
	msg, _ := json.Marshal(map[string]string{"type": eventType})
	h.adminSSEMu.Lock()
	defer h.adminSSEMu.Unlock()
	for ch := range h.adminSSEClients {
		select {
		case ch <- msg:
		default: // drop if slow
		}
	}
}

func (h *MCPHandler) handleAdminEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	ch := make(chan []byte, 16)
	h.adminSSEMu.Lock()
	h.adminSSEClients[ch] = struct{}{}
	h.adminSSEMu.Unlock()

	defer func() {
		h.adminSSEMu.Lock()
		delete(h.adminSSEClients, ch)
		h.adminSSEMu.Unlock()
	}()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher.Flush()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case data := <-ch:
			fmt.Fprintf(w, "event: config_changed\ndata: %s\n\n", data)
			flusher.Flush()
		}
	}
}

// --- Auth handlers ---

func (h *MCPHandler) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if h.auth == nil {
		writeJSONError(w, http.StatusNotFound, "authentication is not enabled")
		return
	}

	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	user, err := h.auth.VerifyPassword(body.Username, body.Password)
	if err != nil {
		writeJSONError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	token, err := h.auth.GenerateToken(user)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to generate token")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"token": token,
		"user":  user,
	})
}

func (h *MCPHandler) handleOIDCLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if h.auth == nil {
		writeJSONError(w, http.StatusNotFound, "authentication is not enabled")
		return
	}

	if h.auth.cfg.OIDC == nil || h.auth.cfg.OIDC.ProviderURL == "" {
		writeJSONError(w, http.StatusNotFound, "OIDC is not configured")
		return
	}

	state, err := h.auth.GenerateOIDCState()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to generate state")
		return
	}

	authURL, err := h.auth.OIDCAuthURL(state)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to build auth URL: "+err.Error())
		return
	}

	http.Redirect(w, r, authURL, http.StatusFound)
}

func (h *MCPHandler) handleOIDCCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if h.auth == nil {
		writeJSONError(w, http.StatusNotFound, "authentication is not enabled")
		return
	}

	state := r.URL.Query().Get("state")
	code := r.URL.Query().Get("code")

	if state == "" || code == "" {
		writeJSONError(w, http.StatusBadRequest, "missing state or code parameter")
		return
	}

	if !h.auth.ValidateOIDCState(state) {
		writeJSONError(w, http.StatusBadRequest, "invalid or expired state")
		return
	}

	email, err := h.auth.OIDCExchangeCode(code)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "OIDC exchange failed: "+err.Error())
		return
	}

	user := h.auth.FindOrCreateOIDCUser(email)

	token, err := h.auth.GenerateToken(user)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to generate token")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"token": token,
		"user":  user,
	})
}

func (h *MCPHandler) handleAuthMe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if h.auth == nil {
		writeJSONError(w, http.StatusNotFound, "authentication is not enabled")
		return
	}

	user := userFromContext(r)
	if user == nil {
		writeJSONError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	writeJSON(w, http.StatusOK, user)
}

// --- OAuth 2.1 / MCP-spec handlers ---

// handleProtectedResourceMetadata serves RFC 9728 Protected Resource Metadata.
func (h *MCPHandler) handleProtectedResourceMetadata(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"resource":                 h.resourceURL,
		"authorization_servers":    []string{h.resourceURL},
		"scopes_supported":        []string{"mcp:tools"},
		"bearer_methods_supported": []string{"header"},
	})
}

// handleAuthServerMetadata serves RFC 8414 Authorization Server Metadata.
func (h *MCPHandler) handleAuthServerMetadata(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"issuer":                                h.resourceURL,
		"authorization_endpoint":                h.resourceURL + "/authorize",
		"token_endpoint":                        h.resourceURL + "/oauth/token",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"client_credentials"},
		"token_endpoint_auth_methods_supported": []string{"client_secret_basic", "client_secret_post"},
		"code_challenge_methods_supported":       []string{"S256"},
	})
}

// handleOAuthToken implements the OAuth 2.1 token endpoint (client_credentials grant).
func (h *MCPHandler) handleOAuthToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.auth == nil {
		writeJSONError(w, http.StatusNotFound, "authentication is not enabled")
		return
	}

	if err := r.ParseForm(); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid form data")
		return
	}

	grantType := r.FormValue("grant_type")
	if grantType != "client_credentials" {
		writeJSONError(w, http.StatusBadRequest, "unsupported grant_type")
		return
	}

	// Extract client credentials: try Basic auth first, then form body.
	clientID, clientSecret, basicOK := r.BasicAuth()
	if !basicOK || clientID == "" {
		clientID = r.FormValue("client_id")
		clientSecret = r.FormValue("client_secret")
	}

	if clientID == "" || clientSecret == "" {
		writeJSONError(w, http.StatusUnauthorized, "missing client credentials")
		return
	}

	user, err := h.auth.ValidateClientCredentials(clientID, clientSecret)
	if err != nil {
		writeJSONError(w, http.StatusUnauthorized, "invalid client credentials")
		return
	}

	token, err := h.auth.GenerateToken(user)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to generate token")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"access_token": token,
		"token_type":   "Bearer",
		"expires_in":   h.auth.ExpirySeconds(),
	})
}

// handleAuthorize is a placeholder for the authorization endpoint.
// Required by the OAuthMetadata model but unused for client_credentials flow.
func (h *MCPHandler) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "Authorization code flow is not supported", http.StatusNotImplemented)
}

// --- Admin API handlers ---

func (h *MCPHandler) handleAPIServers(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, h.registry.AllDownstreams())
	case http.MethodPost:
		if h.auth != nil {
			if requireAdmin(w, r) == nil {
				return
			}
		}
		h.handleAddServer(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *MCPHandler) handleAddServer(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "Invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if body.Name == "" || body.URL == "" {
		http.Error(w, "name and url are required", http.StatusBadRequest)
		return
	}

	c := h.registry.AddDownstream(body.Name, body.URL)
	writeJSON(w, http.StatusOK, c.Status())
}

func (h *MCPHandler) handleAPIServerByName(w http.ResponseWriter, r *http.Request) {
	// Extract name from /api/servers/{name}
	name := strings.TrimPrefix(r.URL.Path, "/api/servers/")
	if name == "" {
		http.Error(w, "server name required", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodDelete:
		if h.auth != nil {
			if requireAdmin(w, r) == nil {
				return
			}
		}
		if h.registry.RemoveDownstream(name) {
			w.WriteHeader(http.StatusNoContent)
		} else {
			http.Error(w, "server not found", http.StatusNotFound)
		}
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *MCPHandler) handleAPITools(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	tools := h.registry.AllTools()
	if tools == nil {
		tools = []Tool{}
	}

	// Filter tools by user's role when auth is enabled.
	if h.auth != nil {
		if user := userFromContext(r); user != nil {
			tools = h.auth.FilterTools(user, tools)
		}
	}

	if tools == nil {
		tools = []Tool{}
	}
	writeJSON(w, http.StatusOK, tools)
}

func (h *MCPHandler) handleAPIUsers(w http.ResponseWriter, r *http.Request) {
	if h.auth == nil {
		writeJSONError(w, http.StatusNotFound, "authentication is not enabled")
		return
	}

	switch r.Method {
	case http.MethodGet:
		if requireAdmin(w, r) == nil {
			return
		}
		writeJSON(w, http.StatusOK, h.auth.ListUsers())
	case http.MethodPost:
		if requireAdmin(w, r) == nil {
			return
		}
		var body struct {
			Username string `json:"username"`
			Password string `json:"password"`
			Role     string `json:"role"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if body.Username == "" || body.Password == "" || body.Role == "" {
			writeJSONError(w, http.StatusBadRequest, "username, password, and role are required")
			return
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "failed to hash password")
			return
		}
		user, err := h.auth.CreateUser(body.Username, string(hash), body.Role)
		if err != nil {
			writeJSONError(w, http.StatusConflict, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, user)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *MCPHandler) handleAPIUserByName(w http.ResponseWriter, r *http.Request) {
	if h.auth == nil {
		writeJSONError(w, http.StatusNotFound, "authentication is not enabled")
		return
	}

	name := strings.TrimPrefix(r.URL.Path, "/api/users/")
	if name == "" {
		writeJSONError(w, http.StatusBadRequest, "username required")
		return
	}

	switch r.Method {
	case http.MethodDelete:
		if requireAdmin(w, r) == nil {
			return
		}
		if err := h.auth.DeleteUser(name); err != nil {
			writeJSONError(w, http.StatusNotFound, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *MCPHandler) handleAPIRoles(w http.ResponseWriter, r *http.Request) {
	if h.auth == nil {
		writeJSONError(w, http.StatusNotFound, "authentication is not enabled")
		return
	}

	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, h.auth.ListRoles())
	case http.MethodPost:
		if requireAdmin(w, r) == nil {
			return
		}
		var body struct {
			Name  string   `json:"name"`
			Tools []string `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if body.Name == "" {
			writeJSONError(w, http.StatusBadRequest, "name is required")
			return
		}
		if body.Tools == nil {
			body.Tools = []string{}
		}
		if err := h.auth.CreateRole(body.Name, body.Tools); err != nil {
			writeJSONError(w, http.StatusConflict, err.Error())
			return
		}
		w.WriteHeader(http.StatusCreated)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *MCPHandler) handleAPIRoleByName(w http.ResponseWriter, r *http.Request) {
	if h.auth == nil {
		writeJSONError(w, http.StatusNotFound, "authentication is not enabled")
		return
	}

	name := strings.TrimPrefix(r.URL.Path, "/api/roles/")
	if name == "" {
		writeJSONError(w, http.StatusBadRequest, "role name required")
		return
	}

	switch r.Method {
	case http.MethodPut:
		if requireAdmin(w, r) == nil {
			return
		}
		var body struct {
			Tools []string `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if body.Tools == nil {
			body.Tools = []string{}
		}
		if err := h.auth.UpdateRole(name, body.Tools); err != nil {
			writeJSONError(w, http.StatusNotFound, err.Error())
			return
		}
		w.WriteHeader(http.StatusOK)
	case http.MethodDelete:
		if requireAdmin(w, r) == nil {
			return
		}
		if err := h.auth.DeleteRole(name); err != nil {
			if strings.Contains(err.Error(), "assigned") {
				writeJSONError(w, http.StatusConflict, err.Error())
			} else {
				writeJSONError(w, http.StatusNotFound, err.Error())
			}
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *MCPHandler) handleUI(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(adminHTML)
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
