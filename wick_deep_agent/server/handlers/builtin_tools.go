package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"wick_server/agent"
)

// NewBuiltinTools returns the set of built-in tools (not backend-dependent).
// Tavily API key is read from the agent's BuiltinConfig map.
func NewBuiltinTools(cfg *agent.AgentConfig) []agent.Tool {
	var tools []agent.Tool

	// internet_search — uses Tavily API from agent's builtin_config
	tavilyKey := ""
	if cfg.BuiltinConfig != nil {
		tavilyKey = cfg.BuiltinConfig["tavily_api_key"]
	}
	if tavilyKey != "" {
		key := tavilyKey // capture for closure
		tools = append(tools, &agent.FuncTool{
			ToolName: "internet_search",
			ToolDesc: "Search the internet for information. Returns relevant search results with snippets.",
			ToolParams: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{"type": "string", "description": "Search query"},
				},
				"required": []string{"query"},
			},
			Fn: func(ctx context.Context, args map[string]any) (string, error) {
				query, _ := args["query"].(string)
				if query == "" {
					return "Error: query is required", nil
				}
				return tavilySearch(ctx, key, query)
			},
		})
	}

	// calculate
	tools = append(tools, &agent.FuncTool{
		ToolName: "calculate",
		ToolDesc: "Evaluate a mathematical expression. Supports basic arithmetic (+, -, *, /, ^, %, sqrt).",
		ToolParams: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"expression": map[string]any{"type": "string", "description": "Mathematical expression to evaluate"},
			},
			"required": []string{"expression"},
		},
		Fn: func(ctx context.Context, args map[string]any) (string, error) {
			expr, _ := args["expression"].(string)
			if expr == "" {
				return "Error: expression is required", nil
			}
			return calculate(expr), nil
		},
	})

	// current_datetime
	tools = append(tools, &agent.FuncTool{
		ToolName: "current_datetime",
		ToolDesc: "Get the current date and time in UTC and local timezone.",
		ToolParams: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
		Fn: func(ctx context.Context, args map[string]any) (string, error) {
			now := time.Now()
			return fmt.Sprintf("UTC: %s\nLocal: %s",
				now.UTC().Format(time.RFC3339),
				now.Format(time.RFC3339),
			), nil
		},
	})

	return tools
}

// tavilySearch calls the Tavily search API.
func tavilySearch(ctx context.Context, apiKey, query string) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"api_key":            apiKey,
		"query":              query,
		"search_depth":       "basic",
		"include_answer":     true,
		"max_results":        5,
	})

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.tavily.com/search", strings.NewReader(string(body)))
	if err != nil {
		return "Error: " + err.Error(), nil
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "Error: search request failed: " + err.Error(), nil
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return fmt.Sprintf("Error: search API returned %d: %s", resp.StatusCode, string(data)), nil
	}

	var result struct {
		Answer  string `json:"answer"`
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}

	if err := json.Unmarshal(data, &result); err != nil {
		return "Error parsing search results: " + err.Error(), nil
	}

	var sb strings.Builder
	if result.Answer != "" {
		sb.WriteString("Answer: " + result.Answer + "\n\n")
	}
	sb.WriteString("Sources:\n")
	for _, r := range result.Results {
		sb.WriteString(fmt.Sprintf("- [%s](%s)\n  %s\n\n", r.Title, r.URL, r.Content))
	}

	return sb.String(), nil
}

// calculate evaluates a simple math expression.
func calculate(expr string) string {
	expr = strings.TrimSpace(expr)

	// Handle sqrt
	if strings.HasPrefix(expr, "sqrt(") && strings.HasSuffix(expr, ")") {
		inner := expr[5 : len(expr)-1]
		val, err := strconv.ParseFloat(inner, 64)
		if err != nil {
			return "Error: invalid number in sqrt"
		}
		return fmt.Sprintf("%g", math.Sqrt(val))
	}

	// Simple two-operand expression
	for _, op := range []string{"+", "-", "*", "/", "^", "%"} {
		// Find the operator (skip if it's at the start for negative numbers)
		idx := -1
		for i := 1; i < len(expr); i++ {
			if string(expr[i]) == op {
				idx = i
				break
			}
		}
		if idx < 0 {
			continue
		}

		left, err1 := strconv.ParseFloat(strings.TrimSpace(expr[:idx]), 64)
		right, err2 := strconv.ParseFloat(strings.TrimSpace(expr[idx+1:]), 64)
		if err1 != nil || err2 != nil {
			continue
		}

		switch op {
		case "+":
			return fmt.Sprintf("%g", left+right)
		case "-":
			return fmt.Sprintf("%g", left-right)
		case "*":
			return fmt.Sprintf("%g", left*right)
		case "/":
			if right == 0 {
				return "Error: division by zero"
			}
			return fmt.Sprintf("%g", left/right)
		case "^":
			return fmt.Sprintf("%g", math.Pow(left, right))
		case "%":
			return fmt.Sprintf("%g", math.Mod(left, right))
		}
	}

	// Try as a plain number
	if val, err := strconv.ParseFloat(expr, 64); err == nil {
		return fmt.Sprintf("%g", val)
	}

	return "Error: could not evaluate expression: " + expr
}
