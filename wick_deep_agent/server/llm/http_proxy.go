package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// HTTPProxyClient implements the Client interface by proxying LLM calls
// to a Python sidecar over HTTP. This allows Python apps to define custom
// model handlers with full control over auth, request/response transforms.
type HTTPProxyClient struct {
	callbackURL string
	modelName   string
	client      *http.Client
}

// NewHTTPProxyClient creates a new proxy client that forwards LLM calls
// to the given callback URL (e.g. "http://127.0.0.1:9100").
func NewHTTPProxyClient(callbackURL, modelName string) *HTTPProxyClient {
	return &HTTPProxyClient{
		callbackURL: strings.TrimRight(callbackURL, "/"),
		modelName:   modelName,
		client:      &http.Client{Timeout: 5 * time.Minute},
	}
}

// Call makes a synchronous LLM call via the Python sidecar.
func (c *HTTPProxyClient) Call(ctx context.Context, req Request) (*Response, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/llm/%s/call", c.callbackURL, c.modelName)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("proxy LLM error %d: %s", resp.StatusCode, string(data))
	}

	var result Response
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parse proxy response: %w", err)
	}

	return &result, nil
}

// Stream makes a streaming LLM call via the Python sidecar.
// The sidecar returns SSE events (data: {...}\n\n) which are parsed
// as StreamChunk and pushed to the channel.
func (c *HTTPProxyClient) Stream(ctx context.Context, req Request, ch chan<- StreamChunk) error {
	defer close(ch)

	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/llm/%s/stream", c.callbackURL, c.modelName)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("proxy LLM stream error %d: %s", resp.StatusCode, string(data))
	}

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")

		var chunk StreamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		ch <- chunk

		if chunk.Done {
			break
		}
	}

	return scanner.Err()
}
