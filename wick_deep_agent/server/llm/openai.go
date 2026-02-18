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

// OpenAIClient implements the Client interface for OpenAI-compatible APIs
// (OpenAI, Ollama, vLLM, LiteLLM, etc.).
type OpenAIClient struct {
	baseURL string
	apiKey  string
	model   string
	client  *http.Client
}

// NewOpenAIClient creates a new OpenAI-compatible client.
func NewOpenAIClient(baseURL, apiKey, model string) *OpenAIClient {
	return &OpenAIClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		model:   model,
		client:  &http.Client{Timeout: 5 * time.Minute},
	}
}

// openaiRequest is the request body for the chat completions API.
type openaiRequest struct {
	Model       string           `json:"model"`
	Messages    []openaiMessage  `json:"messages"`
	Tools       []openaiTool     `json:"tools,omitempty"`
	Stream      bool             `json:"stream"`
	MaxTokens   int              `json:"max_tokens,omitempty"`
	Temperature *float64         `json:"temperature,omitempty"`
}

type openaiMessage struct {
	Role       string              `json:"role"`
	Content    string              `json:"content"`
	ToolCalls  []openaiToolCall    `json:"tool_calls,omitempty"`
	ToolCallID string              `json:"tool_call_id,omitempty"`
	Name       string              `json:"name,omitempty"`
}

type openaiTool struct {
	Type     string         `json:"type"`
	Function openaiFunction `json:"function"`
}

type openaiFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type openaiToolCall struct {
	ID       string             `json:"id"`
	Type     string             `json:"type"`
	Function openaiToolCallFunc `json:"function"`
}

type openaiToolCallFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type openaiResponse struct {
	Choices []openaiChoice `json:"choices"`
}

type openaiChoice struct {
	Message      openaiMessage      `json:"message"`
	Delta        openaiMessage      `json:"delta"`
	FinishReason string             `json:"finish_reason"`
}

// Call makes a synchronous LLM call.
func (c *OpenAIClient) Call(ctx context.Context, req Request) (*Response, error) {
	body := c.buildRequest(req, false)
	data, err := c.doRequest(ctx, body)
	if err != nil {
		return nil, err
	}

	var resp openaiResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	if len(resp.Choices) == 0 {
		return &Response{}, nil
	}

	msg := resp.Choices[0].Message
	result := &Response{Content: msg.Content}

	for _, tc := range msg.ToolCalls {
		var args map[string]any
		json.Unmarshal([]byte(tc.Function.Arguments), &args)
		result.ToolCalls = append(result.ToolCalls, ToolCallResult{
			ID:   tc.ID,
			Name: tc.Function.Name,
			Args: args,
		})
	}

	return result, nil
}

// Stream makes a streaming LLM call.
func (c *OpenAIClient) Stream(ctx context.Context, req Request, ch chan<- StreamChunk) error {
	defer close(ch)

	body := c.buildRequest(req, true)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" && c.apiKey != "ollama" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("LLM API error %d: %s", resp.StatusCode, string(body))
	}

	scanner := bufio.NewScanner(resp.Body)
	// Track tool call state for accumulation across chunks
	toolCalls := make(map[int]*ToolCallResult) // index → accumulating tool call
	toolCallArgs := make(map[int]*strings.Builder)

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var chunk openaiResponse
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		if len(chunk.Choices) == 0 {
			continue
		}

		delta := chunk.Choices[0].Delta

		// Text content
		if delta.Content != "" {
			ch <- StreamChunk{Delta: delta.Content}
		}

		// Tool calls (accumulated across multiple chunks)
		for _, tc := range delta.ToolCalls {
			// Parse the index from the tool call (OpenAI sends index field)
			idx := 0 // default
			if existing, ok := toolCalls[idx]; ok {
				// Accumulate arguments
				if tc.Function.Arguments != "" {
					toolCallArgs[idx].WriteString(tc.Function.Arguments)
				}
				_ = existing
			} else {
				// New tool call
				toolCalls[idx] = &ToolCallResult{
					ID:   tc.ID,
					Name: tc.Function.Name,
				}
				toolCallArgs[idx] = &strings.Builder{}
				if tc.Function.Arguments != "" {
					toolCallArgs[idx].WriteString(tc.Function.Arguments)
				}
			}
		}

		// On finish_reason="tool_calls", emit accumulated tool calls
		if chunk.Choices[0].FinishReason == "tool_calls" || chunk.Choices[0].FinishReason == "stop" {
			for idx, tc := range toolCalls {
				if builder, ok := toolCallArgs[idx]; ok {
					var args map[string]any
					json.Unmarshal([]byte(builder.String()), &args)
					tc.Args = args
				}
				ch <- StreamChunk{ToolCall: tc}
			}
			// Reset for potential multi-turn
			toolCalls = make(map[int]*ToolCallResult)
			toolCallArgs = make(map[int]*strings.Builder)
		}
	}

	ch <- StreamChunk{Done: true}
	return scanner.Err()
}

func (c *OpenAIClient) buildRequest(req Request, stream bool) []byte {
	msgs := make([]openaiMessage, 0, len(req.Messages)+1)

	// System prompt as first message
	if req.SystemPrompt != "" {
		msgs = append(msgs, openaiMessage{Role: "system", Content: req.SystemPrompt})
	}

	for _, m := range req.Messages {
		msg := openaiMessage{
			Role:       m.Role,
			Content:    m.Content,
			ToolCallID: m.ToolCallID,
			Name:       m.Name,
		}
		for _, tc := range m.ToolCalls {
			argsJSON, _ := json.Marshal(tc.Args)
			msg.ToolCalls = append(msg.ToolCalls, openaiToolCall{
				ID:   tc.ID,
				Type: "function",
				Function: openaiToolCallFunc{
					Name:      tc.Name,
					Arguments: string(argsJSON),
				},
			})
		}
		msgs = append(msgs, msg)
	}

	oReq := openaiRequest{
		Model:    c.model,
		Messages: msgs,
		Stream:   stream,
	}

	if req.MaxTokens > 0 {
		oReq.MaxTokens = req.MaxTokens
	}
	if req.Temperature != nil {
		oReq.Temperature = req.Temperature
	}

	for _, t := range req.Tools {
		params := t.Parameters
		if params == nil {
			params = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		oReq.Tools = append(oReq.Tools, openaiTool{
			Type: "function",
			Function: openaiFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  params,
			},
		})
	}

	data, _ := json.Marshal(oReq)
	return data
}

func (c *OpenAIClient) doRequest(ctx context.Context, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" && c.apiKey != "ollama" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

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
		return nil, fmt.Errorf("LLM API error %d: %s", resp.StatusCode, string(data))
	}

	return data, nil
}
