package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to config file")
	flag.Parse()

	cfg, err := LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Create downstream clients.
	clients := make([]*DownstreamClient, len(cfg.Downstream))
	for i, ds := range cfg.Downstream {
		clients[i] = NewDownstreamClient(ds.Name, ds.URL)
	}

	// Discover tools from all downstreams.
	registry := NewRegistry()
	if err := registry.DiscoverAll(clients); err != nil {
		log.Fatalf("Failed to discover tools: %v", err)
	}

	// Set up HTTP server.
	handler := NewMCPHandler(registry)
	mux := http.NewServeMux()
	mux.Handle("/mcp", handler)

	srv := &http.Server{
		Addr:    cfg.Listen,
		Handler: mux,
	}

	// Graceful shutdown on SIGINT/SIGTERM.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		log.Printf("MCP Gateway listening on %s", cfg.Listen)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server error: %v", err)
		}
	}()

	<-stop
	log.Println("Shutting down...")

	// Close downstream sessions.
	for _, c := range clients {
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
