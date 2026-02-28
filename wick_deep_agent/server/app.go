package wickserver

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"wick_server/agent"
	"wick_server/handlers"
	"wick_server/tracing"
)

// Server is the main wickserver instance. Create one with New(), register
// agents and tools, then call Start() to run the HTTP server.
type Server struct {
	host       string
	port       int
	gatewayURL string
	configFile string
	staticPath string

	tools  []agent.Tool
	agents map[string]*agent.AgentConfig

	deps *handlers.Deps
	srv  *http.Server
}

// Option configures a Server.
type Option func(*Server)

// WithPort sets the listen port (default 8000).
func WithPort(port int) Option {
	return func(s *Server) { s.port = port }
}

// WithHost sets the listen host (default "0.0.0.0").
func WithHost(host string) Option {
	return func(s *Server) { s.host = host }
}

// WithGateway sets the gateway URL for auth & RBAC proxying.
func WithGateway(url string) Option {
	return func(s *Server) { s.gatewayURL = url }
}

// WithConfigFile sets the path to an agents.yaml config file.
func WithConfigFile(path string) Option {
	return func(s *Server) { s.configFile = path }
}

// WithStaticPath sets the directory for static file serving with SPA fallback.
func WithStaticPath(path string) Option {
	return func(s *Server) { s.staticPath = path }
}

// New creates a new Server with the given options.
func New(opts ...Option) *Server {
	s := &Server{
		host:       "0.0.0.0",
		port:       8000,
		staticPath: "static",
		agents:     make(map[string]*agent.AgentConfig),
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// RegisterAgent registers an agent template before Start().
func (s *Server) RegisterAgent(id string, cfg *agent.AgentConfig) {
	s.agents[id] = cfg
}

// RegisterTool registers a native tool (e.g. *agent.FuncTool) before Start().
func (s *Server) RegisterTool(t agent.Tool) {
	s.tools = append(s.tools, t)
}

// Start initializes dependencies, builds routes, and runs the HTTP server.
// It blocks until the server is shut down via signal or Shutdown().
func (s *Server) Start() error {
	registry := agent.NewRegistry()

	s.deps = &handlers.Deps{
		Registry:      registry,
		AppConfig:     &handlers.Config{WickGatewayURL: s.gatewayURL},
		EventBus:      handlers.NewEventBus(),
		Backends:      handlers.NewBackendStore(),
		ResolveUser:   ResolveUser,
		ResolveRole:   ResolveRole,
		TraceStore:    tracing.NewStore(1000),
		ExternalTools: handlers.NewToolStore(),
	}

	// Register agents added via RegisterAgent()
	for id, cfg := range s.agents {
		registry.RegisterTemplate(id, cfg)
		log.Printf("  registered agent %q (%s)", id, cfg.Name)
	}

	// Register native tools added via RegisterTool()
	for _, t := range s.tools {
		s.deps.ExternalTools.AddTool(t)
		log.Printf("  registered tool %q", t.Name())
	}

	// Load agents from config file if provided
	if s.configFile != "" {
		log.Printf("Loading config from %s", s.configFile)
		if err := LoadConfigFile(s.configFile, s.deps); err != nil {
			return fmt.Errorf("failed to load config: %w", err)
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
	if s.gatewayURL != "" {
		proxy := authProxy(s.gatewayURL)
		mux.Handle("/auth/login", proxy)
		mux.Handle("/auth/me", proxy)
	} else {
		mux.HandleFunc("/auth/me", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotImplemented) // 501
		})
	}

	// Agent routes (auth required when gateway configured)
	agentMux := http.NewServeMux()
	handlers.RegisterRoutes(agentMux, s.deps)
	mux.Handle("/agents/", authMiddleware(s.gatewayURL, agentMux))
	mux.Handle("/agents", authMiddleware(s.gatewayURL, agentMux))

	// Static file serving with SPA fallback
	if info, err := os.Stat(s.staticPath); err == nil && info.IsDir() {
		log.Printf("Serving static files from %s", s.staticPath)
		fs := http.FileServer(http.Dir(s.staticPath))
		staticPath := s.staticPath
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			path := staticPath + r.URL.Path
			if _, err := os.Stat(path); os.IsNotExist(err) && r.URL.Path != "/" {
				http.ServeFile(w, r, staticPath+"/index.html")
				return
			}
			fs.ServeHTTP(w, r)
		})
	}

	handler := corsMiddleware(mux)

	addr := fmt.Sprintf("%s:%d", s.host, s.port)
	s.srv = &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0, // disable for SSE
		IdleTimeout:  120 * time.Second,
	}

	// Graceful shutdown on signal
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		log.Println("Shutting down...")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		s.srv.Shutdown(ctx)
	}()

	if s.gatewayURL != "" {
		log.Printf("wick_server starting on %s (agents=%d, gateway=%s)", addr, registry.TemplateCount(), s.gatewayURL)
	} else {
		log.Printf("wick_server starting on %s (agents=%d, auth=disabled)", addr, registry.TemplateCount())
	}

	if err := s.srv.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	return nil
}

// Shutdown gracefully shuts down the server.
func (s *Server) Shutdown() error {
	if s.srv == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return s.srv.Shutdown(ctx)
}
