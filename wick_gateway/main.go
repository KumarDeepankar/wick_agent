package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to config file")
	hashPwd := flag.String("hash-password", "", "print bcrypt hash for a password and exit")
	flag.Parse()

	if *hashPwd != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(*hashPwd), bcrypt.DefaultCost)
		if err != nil {
			log.Fatalf("Failed to hash password: %v", err)
		}
		fmt.Println(string(hash))
		os.Exit(0)
	}

	cfg, err := LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Create downstream clients from config.
	clients := make([]*DownstreamClient, len(cfg.Downstream))
	for i, ds := range cfg.Downstream {
		clients[i] = NewDownstreamClient(ds.Name, ds.URL)
	}

	// Discover tools from all downstreams (non-fatal — failures are retried in background).
	registry := NewRegistry()
	registry.DiscoverAll(clients)

	// Start background health loop (retries disconnected, pings connected).
	registry.StartHealthLoop(10 * time.Second)

	// Initialize auth service if enabled.
	var authSvc *AuthService
	resourceURL := cfg.Auth.ResourceURL
	if resourceURL == "" {
		resourceURL = "http://localhost" + cfg.Listen
	}
	if cfg.Auth.Enabled {
		authSvc, err = NewAuthService(cfg.Auth, cfg.Roles, cfg.Users, cfg.OAuthClients, *configPath)
		if err != nil {
			log.Fatalf("Failed to initialize auth service: %v", err)
		}
		log.Printf("Auth enabled: %d users, %d roles, %d oauth_clients", len(cfg.Users), len(cfg.Roles), len(cfg.OAuthClients))
	}

	// Set up HTTP server with MCP + admin routes.
	handler := NewMCPHandler(registry, authSvc, resourceURL)

	// Wire auth config changes to admin SSE broadcast.
	if authSvc != nil {
		authSvc.OnChange = func() {
			handler.broadcastAdminEvent("config_changed")
		}
	}

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	srv := &http.Server{
		Addr:    cfg.Listen,
		Handler: AuthMiddleware(authSvc, resourceURL, mux),
	}

	// Graceful shutdown on SIGINT/SIGTERM.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		log.Printf("MCP Gateway listening on %s (admin UI at http://localhost%s/)", cfg.Listen, cfg.Listen)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server error: %v", err)
		}
	}()

	<-stop
	log.Println("Shutting down...")

	// Stop background health checks.
	registry.StopHealthLoop()

	// Close downstream sessions.
	for _, c := range registry.Clients() {
		if err := c.Close(); err != nil {
			log.Printf("Error closing downstream %s: %v", c.Name, err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("Shutdown error: %v", err)
	}

	log.Println("Gateway stopped")
}
