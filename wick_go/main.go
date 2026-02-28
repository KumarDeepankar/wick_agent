package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	wickserver "wick_server"
	"wick_server/agent"
)

func main() {
	here, _ := filepath.Abs(filepath.Dir(os.Args[0]))
	skillsDir := filepath.Join(here, "skills")
	workspaceDir := filepath.Join(here, "workspace")

	// If running via `go run`, use the source directory instead.
	if wd, err := os.Getwd(); err == nil {
		if _, err := os.Stat(filepath.Join(wd, "skills")); err == nil {
			skillsDir = filepath.Join(wd, "skills")
			workspaceDir = filepath.Join(wd, "workspace")
		}
	}

	systemPrompt := `You are a versatile AI assistant that creates high-quality content using your skills library.

When a user asks you to create documents, presentations, reports, spreadsheets, or other structured content:
1. Check your available skills first — read the relevant SKILL.md for full instructions before acting.
2. Always follow the skill's workflow (file format, markers, naming conventions).
3. Write output files to /workspace/ using write_file.

For presentations or slide decks: always use the slides skill.
For data analysis or CSV work: always use the csv-analyzer or data-analysis skill.
For research tasks: always use the research skill.

Prefer using skills over writing custom code. Skills give you proven, consistent workflows.`

	opts := []wickserver.Option{
		wickserver.WithPort(8000),
		wickserver.WithHost("0.0.0.0"),
	}
	if gw := os.Getenv("WICK_GATEWAY_URL"); gw != "" {
		opts = append(opts, wickserver.WithGateway(gw))
	}
	s := wickserver.New(opts...)

	// Default agent (Ollama local)
	s.RegisterAgent("default", &agent.AgentConfig{
		Name:         "Ollama Local",
		Model:        "ollama:llama3.1:8b",
		SystemPrompt: systemPrompt,
		Tools:        []string{"internet_search", "calculate", "current_datetime"},
		Skills:       &agent.SkillsCfg{Paths: []string{skillsDir}},
		Backend:      &agent.BackendCfg{Type: "local", Workdir: workspaceDir},
		Debug:        true,
		Subagents: []agent.SubAgentCfg{
			{
				Name:         "researcher",
				Description:  "Research a topic using web search and return a summary with sources.",
				SystemPrompt: "You are a research assistant. Search the web, verify facts, and provide a concise summary with sources.",
				Tools:        []string{"internet_search"},
			},
		},
	})

	// Gateway Claude agent — uses Anthropic direct API.
	// (The Python app.py used Bedrock with SigV4 via @model; the Go LLM resolver
	// doesn't have a Bedrock client yet, so this uses the Anthropic provider instead.
	// To add Bedrock support, implement an llm.BedrockClient in wick_server/llm/.)
	s.RegisterAgent("gateway-claude", &agent.AgentConfig{
		Name: "Claude",
		Model: map[string]any{
			"provider": "anthropic",
			"model":    "claude-sonnet-4-20250514",
		},
		SystemPrompt: systemPrompt,
		Tools:        []string{"internet_search", "calculate", "current_datetime"},
		Skills:       &agent.SkillsCfg{Paths: []string{skillsDir}},
		Backend:      &agent.BackendCfg{Type: "local", Workdir: workspaceDir},
		Debug:        true,
	})

	// App-level tools
	s.RegisterTool(&agent.FuncTool{
		ToolName: "add",
		ToolDesc: "Add two numbers together and return the sum.",
		ToolParams: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"a": map[string]any{"type": "number", "description": "First number"},
				"b": map[string]any{"type": "number", "description": "Second number"},
			},
			"required": []string{"a", "b"},
		},
		Fn: func(ctx context.Context, args map[string]any) (string, error) {
			a, _ := args["a"].(float64)
			b, _ := args["b"].(float64)
			return fmt.Sprintf("%g", a+b), nil
		},
	})

	s.RegisterTool(&agent.FuncTool{
		ToolName: "weather",
		ToolDesc: "Get the current weather for a city (demo — returns mock data).",
		ToolParams: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"city": map[string]any{"type": "string", "description": "City name"},
			},
			"required": []string{"city"},
		},
		Fn: func(ctx context.Context, args map[string]any) (string, error) {
			city, _ := args["city"].(string)
			return fmt.Sprintf("Weather in %s: 72°F, sunny", city), nil
		},
	})

	log.Println("Starting wick_go...")
	if err := s.Start(); err != nil {
		log.Fatal(err)
	}
}
