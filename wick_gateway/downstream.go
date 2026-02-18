package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// DownstreamStatus is a point-in-time snapshot of a downstream's health.
type DownstreamStatus struct {
	Name      string `json:"name"`
	URL       string `json:"url"`
	Connected bool   `json:"connected"`
	ToolCount int    `json:"toolCount"`
	LastError string `json:"lastError,omitempty"`
	LastCheck string `json:"lastCheck,omitempty"`
}

// DownstreamClient is an MCP Streamable-HTTP client for one downstream server.
type DownstreamClient struct {
	Name      string
	URL       string
	SessionID string

	client *http.Client
	idSeq  atomic.Int64

	mu        sync.RWMutex
	connected bool
	lastError string
	lastCheck time.Time
	toolCount int
}

func NewDownstreamClient(name, url string) *DownstreamClient {
	return &DownstreamClient{
		Name:   name,
		URL:    url,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

// Status returns a point-in-time snapshot of this downstream's health.
func (d *DownstreamClient) Status() DownstreamStatus {
	d.mu.RLock()
	defer d.mu.RUnlock()
	var lastCheck string
	if !d.lastCheck.IsZero() {
		lastCheck = d.lastCheck.Format(time.RFC3339)
	}
	return DownstreamStatus{
		Name:      d.Name,
		URL:       d.URL,
		Connected: d.connected,
		ToolCount: d.toolCount,
		LastError: d.lastError,
		LastCheck: lastCheck,
	}
}

// SetHealth updates the health tracking fields.
func (d *DownstreamClient) SetHealth(connected bool, lastError string, toolCount int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.connected = connected
	d.lastError = lastError
	d.lastCheck = time.Now()
	d.toolCount = toolCount
}

// nextID returns a monotonically increasing JSON-RPC request ID.
func (d *DownstreamClient) nextID() json.RawMessage {
	id := d.idSeq.Add(1)
	return json.RawMessage(fmt.Sprintf("%d", id))
}

// post sends a JSON-RPC request to the downstream and returns the raw HTTP response.
func (d *DownstreamClient) post(req *JSONRPCRequest) (*http.Response, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequest("POST", d.URL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json, text/event-stream")
	if d.SessionID != "" {
		httpReq.Header.Set("Mcp-Session-Id", d.SessionID)
	}

	return d.client.Do(httpReq)
}

// call sends a JSON-RPC request and decodes the JSON-RPC response.
func (d *DownstreamClient) call(method string, params interface{}) (*JSONRPCResponse, http.Header, error) {
	var rawParams json.RawMessage
	if params != nil {
		p, err := json.Marshal(params)
		if err != nil {
			return nil, nil, fmt.Errorf("marshal params: %w", err)
		}
		rawParams = p
	}

	req := &JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      d.nextID(),
		Method:  method,
		Params:  rawParams,
	}

	resp, err := d.post(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, resp.Header, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	// Some responses (like 202 for notifications) have no body.
	if len(respBody) == 0 {
		return nil, resp.Header, nil
	}

	// If the downstream responded with SSE, extract the JSON from the
	// first "data:" line instead of trying to unmarshal the raw body.
	jsonBody := respBody
	ct := resp.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "text/event-stream") {
		jsonBody = extractSSEData(respBody)
		if jsonBody == nil {
			return nil, resp.Header, fmt.Errorf("no data field in SSE response (body: %s)", string(respBody))
		}
	}

	var rpcResp JSONRPCResponse
	if err := json.Unmarshal(jsonBody, &rpcResp); err != nil {
		return nil, resp.Header, fmt.Errorf("unmarshal response: %w (body: %s)", err, string(respBody))
	}

	return &rpcResp, resp.Header, nil
}

// notify sends a JSON-RPC notification (no ID, no response expected).
func (d *DownstreamClient) notify(method string) error {
	req := &JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  method,
	}

	resp, err := d.post(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// extractSSEData scans an SSE body and returns the contents of the first
// "data:" line as raw bytes, or nil if none is found.
func extractSSEData(body []byte) []byte {
	scanner := bufio.NewScanner(bytes.NewReader(body))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			return []byte(strings.TrimPrefix(line, "data: "))
		}
		if strings.HasPrefix(line, "data:") {
			return []byte(strings.TrimPrefix(line, "data:"))
		}
	}
	return nil
}

// Connect initializes the MCP session with the downstream server.
func (d *DownstreamClient) Connect() error {
	params := InitializeParams{
		ProtocolVersion: "2025-03-26",
		Capabilities:    json.RawMessage(`{}`),
		ClientInfo: ClientInfo{
			Name:    "wick_gateway",
			Version: "1.0.0",
		},
	}

	rpcResp, headers, err := d.call("initialize", params)
	if err != nil {
		return fmt.Errorf("initialize %s: %w", d.Name, err)
	}

	// Capture the session ID from the response header.
	if sid := headers.Get("Mcp-Session-Id"); sid != "" {
		d.SessionID = sid
	}

	if rpcResp.Error != nil {
		return fmt.Errorf("initialize %s: code=%d msg=%s", d.Name, rpcResp.Error.Code, rpcResp.Error.Message)
	}

	// Send initialized notification.
	if err := d.notify("notifications/initialized"); err != nil {
		return fmt.Errorf("initialized notification %s: %w", d.Name, err)
	}

	return nil
}

// ListTools fetches the list of tools from the downstream server.
func (d *DownstreamClient) ListTools() ([]Tool, error) {
	rpcResp, _, err := d.call("tools/list", nil)
	if err != nil {
		return nil, fmt.Errorf("tools/list %s: %w", d.Name, err)
	}
	if rpcResp.Error != nil {
		return nil, fmt.Errorf("tools/list %s: code=%d msg=%s", d.Name, rpcResp.Error.Code, rpcResp.Error.Message)
	}

	var result ToolsListResult
	if err := json.Unmarshal(rpcResp.Result, &result); err != nil {
		return nil, fmt.Errorf("unmarshal tools/list %s: %w", d.Name, err)
	}

	return result.Tools, nil
}

// CallTool invokes a tool on the downstream server and returns the raw result.
func (d *DownstreamClient) CallTool(name string, arguments json.RawMessage) (json.RawMessage, error) {
	params := ToolsCallParams{
		Name:      name,
		Arguments: arguments,
	}

	rpcResp, _, err := d.call("tools/call", params)
	if err != nil {
		return nil, fmt.Errorf("tools/call %s/%s: %w", d.Name, name, err)
	}
	if rpcResp.Error != nil {
		return nil, fmt.Errorf("tools/call %s/%s: code=%d msg=%s", d.Name, name, rpcResp.Error.Code, rpcResp.Error.Message)
	}

	return rpcResp.Result, nil
}

// Close terminates the MCP session with the downstream server.
func (d *DownstreamClient) Close() error {
	if d.SessionID == "" {
		return nil
	}

	req, err := http.NewRequest("DELETE", d.URL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Mcp-Session-Id", d.SessionID)

	resp, err := d.client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}
