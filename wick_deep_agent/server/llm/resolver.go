package llm

import (
	"fmt"
	"strings"
)

// ResolverConfig holds provider-specific configuration.
type ResolverConfig struct {
	OllamaBaseURL       string
	GatewayBaseURL      string
	GatewayAPIKey       string
	OpenAIAPIKey        string
	AnthropicAPIKey     string
	GatewayTokenURL     string
	GatewayClientID     string
	GatewayClientSecret string
}

// Resolve parses a model string (e.g. "ollama:llama3.1:8b") and returns a Client.
func Resolve(modelSpec any, cfg *ResolverConfig) (Client, string, error) {
	switch v := modelSpec.(type) {
	case string:
		return resolveString(v, cfg)
	case map[string]any:
		return resolveMap(v, cfg)
	default:
		return nil, "", fmt.Errorf("unsupported model spec type: %T", modelSpec)
	}
}

func resolveString(spec string, cfg *ResolverConfig) (Client, string, error) {
	parts := strings.SplitN(spec, ":", 2)
	provider := parts[0]
	model := ""
	if len(parts) > 1 {
		model = parts[1]
	}

	switch provider {
	case "ollama":
		return NewOpenAIClient(cfg.OllamaBaseURL+"/v1", "ollama", model), model, nil
	case "openai":
		return NewOpenAIClient("https://api.openai.com/v1", cfg.OpenAIAPIKey, model), model, nil
	case "anthropic":
		return NewAnthropicClient(cfg.AnthropicAPIKey, model), model, nil
	case "gateway":
		return NewOpenAIClient(cfg.GatewayBaseURL+"/v1", cfg.GatewayAPIKey, model), model, nil
	default:
		// Try as an Ollama model (e.g. "llama3.1:8b")
		return NewOpenAIClient(cfg.OllamaBaseURL+"/v1", "ollama", spec), spec, nil
	}
}

func resolveMap(spec map[string]any, cfg *ResolverConfig) (Client, string, error) {
	provider, _ := spec["provider"].(string)
	model, _ := spec["model"].(string)
	baseURL, _ := spec["base_url"].(string)
	apiKey, _ := spec["api_key"].(string)

	switch provider {
	case "ollama":
		if baseURL == "" {
			baseURL = cfg.OllamaBaseURL + "/v1"
		}
		return NewOpenAIClient(baseURL, "ollama", model), model, nil
	case "openai":
		if baseURL == "" {
			baseURL = "https://api.openai.com/v1"
		}
		if apiKey == "" {
			apiKey = cfg.OpenAIAPIKey
		}
		return NewOpenAIClient(baseURL, apiKey, model), model, nil
	case "anthropic":
		if apiKey == "" {
			apiKey = cfg.AnthropicAPIKey
		}
		return NewAnthropicClient(apiKey, model), model, nil
	case "gateway":
		if baseURL == "" {
			baseURL = cfg.GatewayBaseURL + "/v1"
		}
		if apiKey == "" {
			apiKey = cfg.GatewayAPIKey
		}
		return NewOpenAIClient(baseURL, apiKey, model), model, nil
	default:
		return nil, "", fmt.Errorf("unknown provider: %q", provider)
	}
}
