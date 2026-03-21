package main

import (
	"log"
	"os"
	"path/filepath"

	wickserver "wick_server"
	"wick_server/agent"
)

func main() {
	// Resolve host-side skills directory (for docker cp into container)
	here, _ := filepath.Abs(filepath.Dir(os.Args[0]))
	hostSkillsDir := filepath.Join(here, "skills")

	// If running via `go run`, use the source directory instead.
	if wd, err := os.Getwd(); err == nil {
		if _, err := os.Stat(filepath.Join(wd, "skills")); err == nil {
			hostSkillsDir = filepath.Join(wd, "skills")
		}
	}

	// Container-side path — this is what the backend.Execute() sees
	containerSkillsDir := "/workspace/skills"

	systemPrompt := `You are a helpful AI assistant. Use your available tools and skills to complete tasks. Write output files to /workspace/.`

	opts := []wickserver.Option{
		wickserver.WithPort(8000),
		wickserver.WithHost("0.0.0.0"),
	}
	if gw := os.Getenv("WICK_GATEWAY_URL"); gw != "" {
		opts = append(opts, wickserver.WithGateway(gw))
	}
	s := wickserver.New(opts...)

	containerName := "wick-sandbox-local"

	// Default agent (Ollama local)
	s.RegisterAgent("default", &agent.AgentConfig{
		Name:         "Ollama Local",
		Model:        "ollama:llama3.1:8b",
		SystemPrompt: systemPrompt,
		Tools:        []string{"internet_search", "calculate", "current_datetime"},
		Skills:       &agent.SkillsCfg{Paths: []string{containerSkillsDir}, HostPaths: []string{hostSkillsDir}},
		Backend:      &agent.BackendCfg{Type: "docker", Workdir: "/workspace", Image: "wick-sandbox", ContainerName: containerName},
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
	s.RegisterAgent("gateway-claude", &agent.AgentConfig{
		Name: "Claude",
		Model: map[string]any{
			"provider": "anthropic",
			"model":    "claude-sonnet-4-20250514",
		},
		SystemPrompt: systemPrompt,
		Tools:        []string{"internet_search", "calculate", "current_datetime"},
		Skills:       &agent.SkillsCfg{Paths: []string{containerSkillsDir}, HostPaths: []string{hostSkillsDir}},
		Backend:      &agent.BackendCfg{Type: "docker", Workdir: "/workspace", Image: "wick-sandbox", ContainerName: containerName},
		Debug:        true,
	})

	registerTools(s)

	log.Println("Starting wick_go...")
	if err := s.Start(); err != nil {
		log.Fatal(err)
	}
}
