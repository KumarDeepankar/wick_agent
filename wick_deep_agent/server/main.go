package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"wick_go/agent"
	"wick_go/handlers"
	"wick_go/tracing"
)

func main() {
	appCfg := LoadAppConfig()

	registry := agent.NewRegistry()

	// Create handler context with shared dependencies
	deps := &handlers.Deps{
		Registry:      registry,
		AppConfig:     &handlers.Config{WickGatewayURL: appCfg.WickGatewayURL},
		EventBus:      handlers.NewEventBus(),
		Backends:      handlers.NewBackendStore(),
		ResolveUser:   ResolveUser,
		ResolveRole:   ResolveRole,
		TraceStore:    tracing.NewStore(1000),
		ExternalTools: handlers.NewToolStore(),
	}

	// Load agents from config file if provided
	if appCfg.ConfigFile != "" {
		log.Printf("Loading config from %s", appCfg.ConfigFile)
		if err := loadConfigFile(appCfg.ConfigFile, deps); err != nil {
			log.Fatalf("Failed to load config: %v", err)
		}
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

	// Auth routing: proxy to gateway when configured, otherwise 501 stub
	// so the UI knows to skip login and use local mode.
	if appCfg.WickGatewayURL != "" {
		proxy := authProxy(appCfg.WickGatewayURL)
		mux.Handle("/auth/login", proxy)
		mux.Handle("/auth/me", proxy)
	} else {
		mux.HandleFunc("/auth/me", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotImplemented) // 501
		})
	}

	// Agent routes (auth required when gateway configured)
	agentMux := http.NewServeMux()
	handlers.RegisterRoutes(agentMux, deps)
	mux.Handle("/agents/", authMiddleware(appCfg.WickGatewayURL, agentMux))
	mux.Handle("/agents", authMiddleware(appCfg.WickGatewayURL, agentMux))

	// Static file serving with SPA fallback
	staticPath := "static"
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

	if appCfg.WickGatewayURL != "" {
		log.Printf("wick_go starting on %s (agents=%d, gateway=%s)", addr, registry.TemplateCount(), appCfg.WickGatewayURL)
	} else {
		log.Printf("wick_go starting on %s (agents=%d, auth=disabled)", addr, registry.TemplateCount())
	}
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("Server error: %v", err)
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
