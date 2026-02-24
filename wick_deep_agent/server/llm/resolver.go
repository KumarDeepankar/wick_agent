package llm

import (
	"fmt"
	"strings"
)

// Resolve parses a model spec (string or map) and returns a Client.
// String specs only work for ollama (e.g. "ollama:llama3.1:8b").
// All other providers require map format with explicit credentials.
func Resolve(modelSpec any) (Client, string, error) {
	switch v := modelSpec.(type) {
	case string:
		return resolveString(v)
	case map[string]any:
		return resolveMap(v)
	default:
		return nil, "", fmt.Errorf("unsupported model spec type: %T", modelSpec)
	}
}

func resolveString(spec string) (Client, string, error) {
	parts := strings.SplitN(spec, ":", 2)
	provider := parts[0]
	model := ""
	if len(parts) > 1 {
		model = parts[1]
	}

	switch provider {
	case "ollama":
		return NewOpenAIClient("http://localhost:11434/v1", "ollama", model), model, nil
	case "openai":
		return nil, "", fmt.Errorf("openai provider requires map format with api_key (e.g. {\"provider\":\"openai\",\"model\":\"gpt-4\",\"api_key\":\"...\"})")
	case "anthropic":
		return nil, "", fmt.Errorf("anthropic provider requires map format with api_key (e.g. {\"provider\":\"anthropic\",\"model\":\"claude-3\",\"api_key\":\"...\"})")
	case "gateway":
		return nil, "", fmt.Errorf("gateway provider requires map format with base_url and api_key")
	default:
		// Try as an Ollama model (e.g. "llama3.1:8b")
		return NewOpenAIClient("http://localhost:11434/v1", "ollama", spec), spec, nil
	}
}

func resolveMap(spec map[string]any) (Client, string, error) {
	provider, _ := spec["provider"].(string)
	model, _ := spec["model"].(string)
	baseURL, _ := spec["base_url"].(string)
	apiKey, _ := spec["api_key"].(string)

	switch provider {
	case "ollama":
		if baseURL == "" {
			baseURL = "http://localhost:11434/v1"
		}
		return NewOpenAIClient(baseURL, "ollama", model), model, nil
	case "openai":
		if apiKey == "" {
			return nil, "", fmt.Errorf("openai provider requires api_key in model spec")
		}
		if baseURL == "" {
			baseURL = "https://api.openai.com/v1"
		}
		return NewOpenAIClient(baseURL, apiKey, model), model, nil
	case "anthropic":
		if apiKey == "" {
			return nil, "", fmt.Errorf("anthropic provider requires api_key in model spec")
		}
		return NewAnthropicClient(apiKey, model), model, nil
	case "gateway":
		if baseURL == "" {
			return nil, "", fmt.Errorf("gateway provider requires base_url in model spec")
		}
		if apiKey == "" {
			return nil, "", fmt.Errorf("gateway provider requires api_key in model spec")
		}
		return NewOpenAIClient(baseURL, apiKey, model), model, nil
	case "proxy":
		callbackURL, _ := spec["callback_url"].(string)
		if callbackURL == "" {
			return nil, "", fmt.Errorf("proxy provider requires callback_url")
		}
		return NewHTTPProxyClient(callbackURL, model), model, nil
	default:
		return nil, "", fmt.Errorf("unknown provider: %q", provider)
	}
}
