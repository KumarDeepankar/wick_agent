package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type DownstreamServer struct {
	Name string `yaml:"name"`
	URL  string `yaml:"url"`
}

type AuthConfig struct {
	Enabled     bool        `yaml:"enabled"`
	JWTSecret   string      `yaml:"jwt_secret"`
	TokenExpiry string      `yaml:"token_expiry"`
	ResourceURL string      `yaml:"resource_url"`
	OIDC        *OIDCConfig `yaml:"oidc,omitempty"`
}

type OIDCConfig struct {
	ProviderURL  string   `yaml:"provider_url"`
	ClientID     string   `yaml:"client_id"`
	ClientSecret string   `yaml:"client_secret"`
	RedirectURL  string   `yaml:"redirect_url"`
	Scopes       []string `yaml:"scopes"`
	DefaultRole  string   `yaml:"default_role"`
}

type RoleConfig struct {
	Tools []string `yaml:"tools" json:"tools"`
}

type UserConfig struct {
	Username     string `yaml:"username"`
	PasswordHash string `yaml:"password_hash"`
	Role         string `yaml:"role"`
}

type OAuthClientConfig struct {
	ClientID     string `yaml:"client_id"`
	ClientSecret string `yaml:"client_secret"`
	Role         string `yaml:"role"`
}

type Config struct {
	Listen       string                `yaml:"listen"`
	Auth         AuthConfig            `yaml:"auth"`
	Roles        map[string]RoleConfig `yaml:"roles"`
	Users        []UserConfig          `yaml:"users"`
	OAuthClients []OAuthClientConfig   `yaml:"oauth_clients"`
	Downstream   []DownstreamServer    `yaml:"downstream"`
}

func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	if cfg.Listen == "" {
		cfg.Listen = ":8080"
	}
	if cfg.Auth.Enabled {
		if err := validateAuth(&cfg); err != nil {
			return nil, fmt.Errorf("auth config: %w", err)
		}
	}
	// Environment variable overrides for Docker networking.
	// WICK_DOWNSTREAM_<NAME>_URL overrides the URL for the named downstream.
	for i := range cfg.Downstream {
		envKey := "WICK_DOWNSTREAM_" + strings.ToUpper(cfg.Downstream[i].Name) + "_URL"
		if v := os.Getenv(envKey); v != "" {
			cfg.Downstream[i].URL = v
		}
	}
	// WICK_AUTH_RESOURCE_URL overrides auth.resource_url.
	if v := os.Getenv("WICK_AUTH_RESOURCE_URL"); v != "" {
		cfg.Auth.ResourceURL = v
	}

	return &cfg, nil
}

func validateAuth(cfg *Config) error {
	if cfg.Auth.JWTSecret == "" {
		return fmt.Errorf("jwt_secret is required when auth is enabled")
	}
	if cfg.Auth.TokenExpiry == "" {
		cfg.Auth.TokenExpiry = "24h"
	}
	if _, err := time.ParseDuration(cfg.Auth.TokenExpiry); err != nil {
		return fmt.Errorf("invalid token_expiry %q: %w", cfg.Auth.TokenExpiry, err)
	}
	if cfg.Auth.OIDC != nil && cfg.Auth.OIDC.ProviderURL != "" {
		oidc := cfg.Auth.OIDC
		if oidc.ClientID == "" {
			return fmt.Errorf("oidc.client_id is required when provider_url is set")
		}
		if oidc.ClientSecret == "" {
			return fmt.Errorf("oidc.client_secret is required when provider_url is set")
		}
		if oidc.RedirectURL == "" {
			return fmt.Errorf("oidc.redirect_url is required when provider_url is set")
		}
		if oidc.DefaultRole == "" {
			oidc.DefaultRole = "viewer"
		}
		if len(oidc.Scopes) == 0 {
			oidc.Scopes = []string{"openid", "profile", "email"}
		}
	}
	for _, u := range cfg.Users {
		if u.Username == "" {
			return fmt.Errorf("user entry has empty username")
		}
		if u.PasswordHash == "" {
			return fmt.Errorf("user %q has empty password_hash", u.Username)
		}
		if u.Role == "" {
			return fmt.Errorf("user %q has empty role", u.Username)
		}
		if _, ok := cfg.Roles[u.Role]; !ok {
			return fmt.Errorf("user %q references undefined role %q", u.Username, u.Role)
		}
	}
	for _, oc := range cfg.OAuthClients {
		if oc.ClientID == "" {
			return fmt.Errorf("oauth_client entry has empty client_id")
		}
		if oc.ClientSecret == "" {
			return fmt.Errorf("oauth_client %q has empty client_secret", oc.ClientID)
		}
		if oc.Role == "" {
			return fmt.Errorf("oauth_client %q has empty role", oc.ClientID)
		}
		if _, ok := cfg.Roles[oc.Role]; !ok {
			return fmt.Errorf("oauth_client %q references undefined role %q", oc.ClientID, oc.Role)
		}
	}
	if cfg.Auth.ResourceURL == "" {
		cfg.Auth.ResourceURL = "http://localhost" + cfg.Listen
	}
	return nil
}
