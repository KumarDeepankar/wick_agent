package main

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
	"wick_go/agent"
)

// YAMLConfig is the top-level structure of agents.yaml.
type YAMLConfig struct {
	Defaults map[string]any            `yaml:"defaults"`
	Agents   map[string]map[string]any `yaml:"agents"`
}

// AppConfig holds runtime configuration loaded from env and flags.
type AppConfig struct {
	// Server
	Host string
	Port int

	// LLM provider keys
	AnthropicAPIKey string
	OpenAIAPIKey    string
	TavilyAPIKey    string

	// Ollama
	OllamaBaseURL string

	// Gateway (OpenAI-compatible proxy)
	GatewayBaseURL      string
	GatewayAPIKey       string
	GatewayTokenURL     string
	GatewayClientID     string
	GatewayClientSecret string

	// Agent defaults
	DefaultModel   string
	DefaultBackend string

	// Auth
	WickGatewayURL string

	// Config file path
	ConfigPath string
}

// LoadAppConfig reads configuration from environment variables with sensible defaults.
func LoadAppConfig() *AppConfig {
	return &AppConfig{
		Host: envOr("HOST", "0.0.0.0"),
		Port: envIntOr("PORT", 8000),

		AnthropicAPIKey: os.Getenv("ANTHROPIC_API_KEY"),
		OpenAIAPIKey:    os.Getenv("OPENAI_API_KEY"),
		TavilyAPIKey:    os.Getenv("TAVILY_API_KEY"),

		OllamaBaseURL: envOr("OLLAMA_BASE_URL", "http://localhost:11434"),

		GatewayBaseURL:      envOr("GATEWAY_BASE_URL", "http://localhost:4000"),
		GatewayAPIKey:       os.Getenv("GATEWAY_API_KEY"),
		GatewayTokenURL:     os.Getenv("GATEWAY_TOKEN_URL"),
		GatewayClientID:     os.Getenv("GATEWAY_CLIENT_ID"),
		GatewayClientSecret: os.Getenv("GATEWAY_CLIENT_SECRET"),

		DefaultModel:   envOr("DEFAULT_MODEL", "ollama:llama3.1:8b"),
		DefaultBackend: envOr("DEFAULT_BACKEND", "state"),

		WickGatewayURL: os.Getenv("WICK_GATEWAY_URL"),
	}
}

// LoadAgentsYAML reads agents.yaml and returns parsed agent configs.
func LoadAgentsYAML(path string) (map[string]*agent.AgentConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading agents.yaml: %w", err)
	}

	var raw YAMLConfig
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing agents.yaml: %w", err)
	}

	defaults := raw.Defaults
	if defaults == nil {
		defaults = map[string]any{}
	}

	configs := make(map[string]*agent.AgentConfig)
	for id, rawCfg := range raw.Agents {
		merged := applyDefaults(rawCfg, defaults)
		cfg, err := parseAgentConfig(merged)
		if err != nil {
			return nil, fmt.Errorf("agent %q: %w", id, err)
		}
		configs[id] = cfg
	}

	return configs, nil
}

// applyDefaults merges defaults into an agent config (agent values take precedence).
func applyDefaults(agentCfg, defaults map[string]any) map[string]any {
	merged := make(map[string]any)
	// Copy defaults first
	for k, v := range defaults {
		merged[k] = v
	}
	// Agent values override
	for k, v := range agentCfg {
		if existing, ok := merged[k]; ok {
			// Deep-merge one level for nested maps
			if existingMap, ok := existing.(map[string]any); ok {
				if vMap, ok := v.(map[string]any); ok {
					m := make(map[string]any)
					for ek, ev := range existingMap {
						m[ek] = ev
					}
					for vk, vv := range vMap {
						m[vk] = vv
					}
					merged[k] = m
					continue
				}
			}
		}
		merged[k] = v
	}
	return merged
}

// parseAgentConfig converts a raw map into a typed AgentConfig.
func parseAgentConfig(m map[string]any) (*agent.AgentConfig, error) {
	// Re-marshal through YAML for clean typed parsing
	data, err := yaml.Marshal(m)
	if err != nil {
		return nil, err
	}
	var cfg agent.AgentConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	// Preserve the raw Model field since it can be string or map
	if rawModel, ok := m["model"]; ok {
		cfg.Model = rawModel
	}
	return &cfg, nil
}

// envOr returns the environment variable or a default value.
func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// envIntOr returns the environment variable as int or a default value.
func envIntOr(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	var n int
	if _, err := fmt.Sscanf(v, "%d", &n); err != nil {
		return def
	}
	return n
}
