package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/server"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// resolvePort determines the listening port with precedence: flag > PORT env > default :8090
func resolvePort(flagPort string) string {
	port := strings.TrimSpace(flagPort)
	if port == "" {
		port = strings.TrimSpace(os.Getenv("PORT"))
	}
	if port == "" {
		port = ":8090"
	}
	if !strings.HasPrefix(port, ":") {
		port = ":" + port
	}
	return port
}

// RouterSessionIdManagerResolver dynamically resolves session ID using SessionIDExtractor
type RouterSessionIdManagerResolver struct{}

func (r *RouterSessionIdManagerResolver) ResolveSessionIdManager(req *http.Request) server.SessionIdManager {
	sessionID, _ := SessionIDExtractor(req.Context(), req)
	agentID := ExtractAgentID(sessionID)
	return &RouterSessionIdManager{agentID: agentID}
}

type RouterSessionIdManager struct {
	agentID string
}

func (m *RouterSessionIdManager) Generate() string {
	return fmt.Sprintf("%s:%s", m.agentID, uuid.New().String())
}

func (m *RouterSessionIdManager) Validate(sessionID string) (bool, error) {
	return false, nil
}

func (m *RouterSessionIdManager) Terminate(sessionID string) (bool, error) {
	return false, nil
}

// SetupHandler constructs an isolated ServeMux with health, metrics, protected SSE and Streamable HTTP endpoints
func SetupHandler(cfg *Config, mux *Multiplexer, sseServer *server.SSEServer, streamableServer *server.StreamableHTTPServer) http.Handler {
	httpMux := http.NewServeMux()
	httpMux.HandleFunc("/health", HealthHandler(mux))
	httpMux.Handle("/metrics", promhttp.Handler())

	var sseHandler http.Handler
	if sseServer != nil {
		sseHandler = sseServer.SSEHandler()
		httpMux.Handle("/message", AuthMiddleware(cfg.Auth, sseServer.MessageHandler()))
	}

	if sseServer != nil || streamableServer != nil {
		dualSSEHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			isStreamable := r.Header.Get("Mcp-Protocol-Version") != "" ||
				r.Header.Get("Mcp-Session-Id") != "" ||
				r.Header.Get("Mcp-Method") != "" ||
				r.Method == http.MethodPost ||
				r.Method == http.MethodDelete

			if isStreamable && streamableServer != nil {
				streamableServer.ServeHTTP(w, r)
				return
			}
			if r.Method == http.MethodGet && sseHandler != nil {
				sseHandler.ServeHTTP(w, r)
				return
			}
			if streamableServer != nil {
				streamableServer.ServeHTTP(w, r)
				return
			}
			if sseHandler != nil {
				sseHandler.ServeHTTP(w, r)
				return
			}
			http.Error(w, "Endpoint unavailable", http.StatusNotFound)
		})
		httpMux.Handle("/sse", AuthMiddleware(cfg.Auth, dualSSEHandler))
	}

	if streamableServer != nil {
		httpMux.Handle("/mcp", AuthMiddleware(cfg.Auth, streamableServer))
	}

	return httpMux
}

func initAuditLoggerFromConfig(cfg *Config) (*AuditLogger, error) {
	auditPath := "/var/log/mcp-router/audit.jsonl"
	if cfg.Audit.Path != "" {
		auditPath = cfg.Audit.Path
	}
	fallbackPath := "audit.jsonl"
	if cfg.Audit.FallbackPath != "" {
		fallbackPath = cfg.Audit.FallbackPath
	}

	al, err := InitAuditLoggerWithFallback(auditPath, fallbackPath)
	if err != nil {
		if cfg.Audit.Required {
			return nil, fmt.Errorf("audit logger initialization failed and audit is required: %w", err)
		}
		log.Printf("[WARNING] Audit Logger initialization failed on primary (%s) and fallback (%s): %v. Audit events will fail-safe to stderr.", auditPath, fallbackPath, err)
		return nil, nil
	}
	return al, nil
}

func main() {
	configPath := flag.String("config", "config.yaml", "Path to mcp-router configuration YAML file")
	portFlag := flag.String("port", "", "HTTP port to listen on (default :8090, or PORT env var)")
	flag.Parse()

	listenPort := resolvePort(*portFlag)
	log.Printf("MCP Router Gateway initializing with config: %s (port: %s)...", *configPath, listenPort)

	// 1. Load Configuration (Strict Fail-Fast validation)
	cfg, err := LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("❌ Configuration Error: %v", err)
	}
	log.Printf("Loaded %d server configurations", len(cfg.Servers))

	if cfg.Auth.Enabled {
		log.Printf("🔒 Bearer Authentication is ENABLED")
	}
	if cfg.TLS.Enabled {
		log.Printf("🔑 TLS Transport Encryption is ENABLED (cert: %s)", cfg.TLS.CertFile)
	}

	// 2. Init Audit Logger with automatic fallback
	al, err := initAuditLoggerFromConfig(cfg)
	if err != nil {
		log.Fatalf("❌ Fatal: %v", err)
	} else if al != nil {
		defer al.Close()
	}

	// 3. Setup Multiplexer
	mux := NewMultiplexer(cfg)
	if err := mux.StartBackends(); err != nil {
		log.Fatalf("Failed to start backends: %v", err)
	}
	defer mux.Stop()

	// 4. Build MCP Router Server
	mcpServer := BuildRouter(mux)
	sseServer := server.NewSSEServer(mcpServer, server.WithSessionIDGenerator(SessionIDExtractor))
	streamableServer := server.NewStreamableHTTPServer(mcpServer,
		server.WithSessionIdManagerResolver(&RouterSessionIdManagerResolver{}),
	)

	// 5. Setup HTTP Routes & Server
	handler := SetupHandler(cfg, mux, sseServer, streamableServer)
	srv := &http.Server{
		Addr:              listenPort,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// 6. Signal handling & TLS / Plain Server startup
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	serverErrChan := make(chan error, 1)

	go func() {
		if cfg.TLS.Enabled {
			log.Printf("🔒 Listening on HTTPS port %s (TLS Enabled)", listenPort)
			if err := srv.ListenAndServeTLS(cfg.TLS.CertFile, cfg.TLS.KeyFile); err != nil && err != http.ErrServerClosed {
				serverErrChan <- err
			}
		} else {
			log.Printf("Listening on HTTP port %s", listenPort)
			if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				serverErrChan <- err
			}
		}
	}()

	for {
		select {
		case err := <-serverErrChan:
			mux.Stop()
			log.Fatalf("Server failed: %v", err)
		case sig := <-sigChan:
			if sig == syscall.SIGHUP {
				log.Println("🔄 [SIGHUP Received] Reloading configuration...")
				if globalAuditLogger != nil {
					if err := globalAuditLogger.Reopen(); err != nil {
						log.Printf("⚠️ [SIGHUP] Failed to reopen audit log file: %v", err)
					} else {
						log.Println("📁 [SIGHUP] Audit log file reopened successfully for log rotation")
					}
				}
				newCfg, err := LoadConfig(*configPath)
				if err != nil {
					log.Printf("❌ [SIGHUP ERROR] Config reload rejected! Keeping existing config. Error: %v", err)
					continue
				}
				report := mux.Reload(newCfg)
				log.Printf("✅ [SIGHUP SUCCESS] Configuration reloaded successfully (%d backends parsed: %d kept, %d added, %d updated, %d removed).",
					len(newCfg.Servers), len(report.Kept), len(report.Added), len(report.Updated), len(report.Removed))
			} else {
				log.Printf("Shutting down MCP Router gracefully (signal: %v)...", sig)
				shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer shutdownCancel()
				if err := srv.Shutdown(shutdownCtx); err != nil {
					log.Printf("HTTP server shutdown error: %v", err)
				} else {
					log.Printf("HTTP server stopped cleanly.")
				}
				mux.Stop()
				return
			}
		}
	}
}
