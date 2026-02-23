package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"wick_go/agent"
	"wick_go/handlers"
)

func main() {
	configPath := flag.String("config", "agents.yaml", "Path to agents.yaml config file")
	flag.Parse()

	appCfg := LoadAppConfig()
	appCfg.ConfigPath = *configPath

	// Load agent templates from YAML
	agentConfigs, err := LoadAgentsYAML(appCfg.ConfigPath)
	if err != nil {
		log.Printf("WARNING: Failed to load agents.yaml: %v", err)
		agentConfigs = make(map[string]*agent.AgentConfig)
	}

	// Create registry and register templates
	registry := agent.NewRegistry()
	for id, cfg := range agentConfigs {
		registry.RegisterTemplate(id, cfg)
		log.Printf("Registered agent template %q", id)
	}

	// Create handler context with shared dependencies
	deps := &handlers.Deps{
		Registry:    registry,
		AppConfig:   toHandlerConfig(appCfg),
		ResolveUser: ResolveUser,
	}

	// Build route mux
	mux := http.NewServeMux()

	// Health check (no auth required)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"status":        "ok",
			"agents_loaded": registry.TemplateCount(),
		})
	})

	// Agent routes (auth required when gateway configured)
	agentMux := http.NewServeMux()
	handlers.RegisterRoutes(agentMux, deps)
	mux.Handle("/agents/", authMiddleware(appCfg.WickGatewayURL, agentMux))
	mux.Handle("/agents", authMiddleware(appCfg.WickGatewayURL, agentMux))

	// Static file serving with SPA fallback
	staticDir := flag.Lookup("static")
	staticPath := "static"
	if staticDir != nil {
		staticPath = staticDir.Value.String()
	}
	if info, err := os.Stat(staticPath); err == nil && info.IsDir() {
		log.Printf("Serving static files from %s", staticPath)
		fs := http.FileServer(http.Dir(staticPath))
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			// Try to serve the file; if not found, serve index.html (SPA fallback)
			path := staticPath + r.URL.Path
			if _, err := os.Stat(path); os.IsNotExist(err) && r.URL.Path != "/" {
				http.ServeFile(w, r, staticPath+"/index.html")
				return
			}
			fs.ServeHTTP(w, r)
		})
	}

	// CORS middleware for development
	handler := corsMiddleware(mux)

	addr := fmt.Sprintf("%s:%d", appCfg.Host, appCfg.Port)
	srv := &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0, // disable for SSE
		IdleTimeout:  120 * time.Second,
	}

	// Graceful shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		log.Println("Shutting down...")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	}()

	log.Printf("wick_go starting on %s (agents=%d)", addr, registry.TemplateCount())
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("Server error: %v", err)
	}
}

func toHandlerConfig(cfg *AppConfig) *handlers.Config {
	return &handlers.Config{
		WickGatewayURL: cfg.WickGatewayURL,
		DefaultModel:   cfg.DefaultModel,
		OllamaBaseURL:  cfg.OllamaBaseURL,
		GatewayBaseURL:  cfg.GatewayBaseURL,
		GatewayAPIKey:   cfg.GatewayAPIKey,
		OpenAIAPIKey:    cfg.OpenAIAPIKey,
		AnthropicAPIKey: cfg.AnthropicAPIKey,
		TavilyAPIKey:    cfg.TavilyAPIKey,
		ConfigPath:      cfg.ConfigPath,
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}
