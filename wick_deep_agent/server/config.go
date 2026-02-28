package wickserver

import (
	"flag"
	"fmt"
	"os"
)

// AppConfig holds server-level runtime configuration loaded from env.
type AppConfig struct {
	Host           string
	Port           int
	WickGatewayURL string
	ConfigFile     string
}

// LoadAppConfig reads configuration from CLI flags and environment variables.
// CLI flags take precedence over env vars.
func LoadAppConfig() *AppConfig {
	host := flag.String("host", "", "Listen host (env: HOST, default: 0.0.0.0)")
	port := flag.Int("port", 0, "Listen port (env: PORT, default: 8000)")
	gateway := flag.String("gateway", "", "Gateway URL for auth & RBAC (env: WICK_GATEWAY_URL)")
	configFile := flag.String("config", "", "Path to agents.yaml config file")
	flag.Parse()

	cfg := &AppConfig{
		Host:           envOr("HOST", "0.0.0.0"),
		Port:           envIntOr("PORT", 8000),
		WickGatewayURL: os.Getenv("WICK_GATEWAY_URL"),
	}

	// CLI flags override env
	if *host != "" {
		cfg.Host = *host
	}
	if *port != 0 {
		cfg.Port = *port
	}
	if *gateway != "" {
		cfg.WickGatewayURL = *gateway
	}
	if *configFile != "" {
		cfg.ConfigFile = *configFile
	}

	return cfg
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
