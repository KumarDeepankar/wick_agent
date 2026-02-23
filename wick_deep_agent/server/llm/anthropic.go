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

// AnthropicClient implements the Client interface for the Anthropic Messages API.
type AnthropicClient struct {
	apiKey string
	model  string
	client *http.Client
}

// NewAnthropicClient creates a new Anthropic client.
func NewAnthropicClient(apiKey, model string) *AnthropicClient {
	return &AnthropicClient{
		apiKey: apiKey,
		model:  model,
		client: &http.Client{Timeout: 5 * time.Minute},
	}
}

const anthropicBaseURL = "https://api.anthropic.com/v1"

// Anthropic API types
type anthropicRequest struct {
	Model     string             `json:"model"`
	Messages  []anthropicMessage `json:"messages"`
	System    string             `json:"system,omitempty"`
	MaxTokens int                `json:"max_tokens"`
	Stream    bool               `json:"stream"`
	Tools     []anthropicTool    `json:"tools,omitempty"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"` // string or []anthropicContentBlock
}

type anthropicContentBlock struct {
	Type      string         `json:"type"`                 // "text", "tool_use", "tool_result"
	Text      string         `json:"text,omitempty"`
	ID        string         `json:"id,omitempty"`         // tool_use
	Name      string         `json:"name,omitempty"`       // tool_use
	Input     map[string]any `json:"input,omitempty"`      // tool_use
	ToolUseID string         `json:"tool_use_id,omitempty"` // tool_result
	Content   string         `json:"content,omitempty"`     // tool_result (as string)
}

type anthropicTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

type anthropicResponse struct {
	Content    []anthropicContentBlock `json:"content"`
	StopReason string                  `json:"stop_reason"`
}

// Streaming event types
type anthropicStreamEvent struct {
	Type  string          `json:"type"`
	Delta json.RawMessage `json:"delta,omitempty"`
	Index int             `json:"index,omitempty"`
	ContentBlock *anthropicContentBlock `json:"content_block,omitempty"`
}

type anthropicDelta struct {
	Type         string `json:"type"`
	Text         string `json:"text,omitempty"`
	PartialJSON  string `json:"partial_json,omitempty"`
	StopReason   string `json:"stop_reason,omitempty"`
}

// Call makes a synchronous Anthropic API call.
func (c *AnthropicClient) Call(ctx context.Context, req Request) (*Response, error) {
	body := c.buildRequest(req, false)
	data, err := c.doRequest(ctx, body)
	if err != nil {
		return nil, err
	}

	var resp anthropicResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	result := &Response{}
	for _, block := range resp.Content {
		switch block.Type {
		case "text":
			result.Content += block.Text
		case "tool_use":
			result.ToolCalls = append(result.ToolCalls, ToolCallResult{
				ID:   block.ID,
				Name: block.Name,
				Args: block.Input,
			})
		}
	}

	return result, nil
}

// Stream makes a streaming Anthropic API call.
func (c *AnthropicClient) Stream(ctx context.Context, req Request, ch chan<- StreamChunk) error {
	defer close(ch)

	body := c.buildRequest(req, true)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", anthropicBaseURL+"/messages", bytes.NewReader(body))
	if err != nil {
		return err
	}
	c.setHeaders(httpReq)

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Anthropic API error %d: %s", resp.StatusCode, string(body))
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

	// Track current tool use block for argument accumulation
	var currentToolID string
	var currentToolName string
	var argsBuilder strings.Builder

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")

		var event anthropicStreamEvent
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}

		switch event.Type {
		case "content_block_start":
			if event.ContentBlock != nil && event.ContentBlock.Type == "tool_use" {
				currentToolID = event.ContentBlock.ID
				currentToolName = event.ContentBlock.Name
				argsBuilder.Reset()
			}

		case "content_block_delta":
			var delta anthropicDelta
			json.Unmarshal(event.Delta, &delta)

			if delta.Type == "text_delta" && delta.Text != "" {
				ch <- StreamChunk{Delta: delta.Text}
			}
			if delta.Type == "input_json_delta" && delta.PartialJSON != "" {
				argsBuilder.WriteString(delta.PartialJSON)
			}

		case "content_block_stop":
			if currentToolID != "" {
				var args map[string]any
				json.Unmarshal([]byte(argsBuilder.String()), &args)
				ch <- StreamChunk{
					ToolCall: &ToolCallResult{
						ID:   currentToolID,
						Name: currentToolName,
						Args: args,
					},
				}
				currentToolID = ""
				currentToolName = ""
				argsBuilder.Reset()
			}

		case "message_stop":
			ch <- StreamChunk{Done: true}
			return nil
		}
	}

	return scanner.Err()
}

func (c *AnthropicClient) buildRequest(req Request, stream bool) []byte {
	msgs := make([]anthropicMessage, 0, len(req.Messages))

	for _, m := range req.Messages {
		switch m.Role {
		case "system":
			// System handled separately
			continue
		case "assistant":
			if len(m.ToolCalls) > 0 {
				blocks := []anthropicContentBlock{}
				if m.Content != "" {
					blocks = append(blocks, anthropicContentBlock{Type: "text", Text: m.Content})
				}
				for _, tc := range m.ToolCalls {
					blocks = append(blocks, anthropicContentBlock{
						Type:  "tool_use",
						ID:    tc.ID,
						Name:  tc.Name,
						Input: tc.Args,
					})
				}
				msgs = append(msgs, anthropicMessage{Role: "assistant", Content: blocks})
			} else {
				msgs = append(msgs, anthropicMessage{Role: "assistant", Content: m.Content})
			}
		case "tool":
			msgs = append(msgs, anthropicMessage{
				Role: "user",
				Content: []anthropicContentBlock{{
					Type:      "tool_result",
					ToolUseID: m.ToolCallID,
					Content:   m.Content,
				}},
			})
		default:
			msgs = append(msgs, anthropicMessage{Role: m.Role, Content: m.Content})
		}
	}

	aReq := anthropicRequest{
		Model:     c.model,
		Messages:  msgs,
		MaxTokens: req.MaxTokens,
		Stream:    stream,
	}
	if aReq.MaxTokens == 0 {
		aReq.MaxTokens = 4096
	}

	// System prompt
	if req.SystemPrompt != "" {
		aReq.System = req.SystemPrompt
	}

	for _, t := range req.Tools {
		params := t.Parameters
		if params == nil {
			params = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		aReq.Tools = append(aReq.Tools, anthropicTool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: params,
		})
	}

	data, _ := json.Marshal(aReq)
	return data
}

func (c *AnthropicClient) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
}

func (c *AnthropicClient) doRequest(ctx context.Context, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", anthropicBaseURL+"/messages", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	c.setHeaders(req)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Anthropic API error %d: %s", resp.StatusCode, string(data))
	}

	return data, nil
}
