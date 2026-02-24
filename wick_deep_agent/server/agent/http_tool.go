package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// HTTPTool implements Tool by forwarding execution to a remote HTTP callback.
// Used for external tools registered by Python (or other) processes.
type HTTPTool struct {
	ToolName    string
	ToolDesc    string
	ToolParams  map[string]any
	CallbackURL string // base URL, e.g. "http://127.0.0.1:9100"
	Client      *http.Client
}

// NewHTTPTool creates a new HTTP-backed tool.
func NewHTTPTool(name, desc string, params map[string]any, callbackURL string) *HTTPTool {
	return &HTTPTool{
		ToolName:    name,
		ToolDesc:    desc,
		ToolParams:  params,
		CallbackURL: callbackURL,
		Client: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

func (t *HTTPTool) Name() string              { return t.ToolName }
func (t *HTTPTool) Description() string       { return t.ToolDesc }
func (t *HTTPTool) Parameters() map[string]any { return t.ToolParams }

func (t *HTTPTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	payload, err := json.Marshal(map[string]any{
		"name": t.ToolName,
		"args": args,
	})
	if err != nil {
		return "", fmt.Errorf("http_tool: marshal args: %w", err)
	}

	url := fmt.Sprintf("%s/tools/%s", t.CallbackURL, t.ToolName)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("http_tool: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.Client.Do(req)
	if err != nil {
		return "", fmt.Errorf("http_tool: call %s: %w", t.ToolName, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("http_tool: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("http_tool: %s returned %d: %s", t.ToolName, resp.StatusCode, string(body))
	}

	var result struct {
		Result string `json:"result"`
		Error  string `json:"error"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("http_tool: parse response: %w", err)
	}

	if result.Error != "" {
		return "", fmt.Errorf("http_tool: %s: %s", t.ToolName, result.Error)
	}

	return result.Result, nil
}
