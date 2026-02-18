package hooks

import (
	"context"
	"fmt"
	"strings"

	"wick_go/agent"
	"wick_go/llm"
)

// SummarizationHook compresses conversation context when it exceeds 85% of
// the model's context window. Uses a heuristic of len(content)/4 for token counting.
type SummarizationHook struct {
	agent.BaseHook
	llmClient    llm.Client
	contextWindow int // max tokens for the model
}

// NewSummarizationHook creates a summarization hook.
func NewSummarizationHook(client llm.Client, contextWindow int) *SummarizationHook {
	if contextWindow == 0 {
		contextWindow = 128_000 // default for most modern models
	}
	return &SummarizationHook{
		llmClient:     client,
		contextWindow: contextWindow,
	}
}

func (h *SummarizationHook) Name() string { return "summarization" }

// WrapModelCall checks token count and summarizes if needed.
func (h *SummarizationHook) WrapModelCall(ctx context.Context, msgs []agent.Message, next agent.ModelCallWrapFunc) (*llm.Response, error) {
	totalTokens := estimateTokens(msgs)
	threshold := int(float64(h.contextWindow) * 0.85)

	if totalTokens <= threshold {
		return next(ctx, msgs)
	}

	// Split: keep recent 10% of messages, summarize the rest
	keepCount := len(msgs) / 10
	if keepCount < 2 {
		keepCount = 2
	}

	oldMsgs := msgs[:len(msgs)-keepCount]
	recentMsgs := msgs[len(msgs)-keepCount:]

	// Build summarization prompt
	var sb strings.Builder
	sb.WriteString("Summarize the following conversation context concisely. ")
	sb.WriteString("Preserve key decisions, file paths, tool results, and important details. ")
	sb.WriteString("Keep the summary under 2000 words.\n\n")
	for _, m := range oldMsgs {
		content := m.Content
		// Truncate large write_file/edit_file content in old messages
		if len(content) > 2000 && (m.Name == "write_file" || m.Name == "edit_file") {
			content = content[:2000] + "... [truncated]"
		}
		sb.WriteString(fmt.Sprintf("[%s] %s\n\n", m.Role, content))
	}

	// Call LLM for summarization
	summaryReq := llm.Request{
		Messages: []llm.Message{
			{Role: "user", Content: sb.String()},
		},
		MaxTokens: 2000,
	}

	summaryResp, err := h.llmClient.Call(ctx, summaryReq)
	if err != nil {
		// On failure, just pass through (degraded but functional)
		return next(ctx, msgs)
	}

	// Replace old messages with summary
	summaryMsg := agent.Message{
		Role:    "system",
		Content: fmt.Sprintf("[Conversation Summary]\n%s", summaryResp.Content),
	}

	compressed := append([]agent.Message{summaryMsg}, recentMsgs...)
	return next(ctx, compressed)
}

func (h *SummarizationHook) ModifyRequest(ctx context.Context, msgs []agent.Message) ([]agent.Message, error) {
	return msgs, nil
}

func (h *SummarizationHook) WrapToolCall(ctx context.Context, call agent.ToolCall, next agent.ToolCallFunc) (*agent.ToolResult, error) {
	return next(ctx, call)
}

// estimateTokens gives a rough token count (len/4 heuristic).
func estimateTokens(msgs []agent.Message) int {
	total := 0
	for _, m := range msgs {
		total += len(m.Content) / 4
	}
	return total
}
