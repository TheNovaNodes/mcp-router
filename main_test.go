package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/server"
)

func TestResolvePort(t *testing.T) {
	t.Run("default fallback", func(t *testing.T) {
		t.Setenv("PORT", "")
		if p := resolvePort(""); p != ":8090" {
			t.Errorf("expected default :8090, got %s", p)
		}
	})

	t.Run("from flag with colon", func(t *testing.T) {
		if p := resolvePort(":9090"); p != ":9090" {
			t.Errorf("expected :9090, got %s", p)
		}
	})

	t.Run("from flag without colon", func(t *testing.T) {
		if p := resolvePort("9090"); p != ":9090" {
			t.Errorf("expected :9090, got %s", p)
		}
	})

	t.Run("from env without flag", func(t *testing.T) {
		t.Setenv("PORT", "7777")
		if p := resolvePort(""); p != ":7777" {
			t.Errorf("expected :7777, got %s", p)
		}
	})

	t.Run("flag overrides env", func(t *testing.T) {
		t.Setenv("PORT", "7777")
		if p := resolvePort(":8888"); p != ":8888" {
			t.Errorf("expected :8888, got %s", p)
		}
	})
}

func TestSetupHandler(t *testing.T) {
	cfg := &Config{
		Auth: AuthConfig{Enabled: true, BearerToken: "test-secret", AllowQueryToken: true},
		Servers: map[string]ServerConfig{
			"test-backend": {Transport: "stdio", Command: "echo"},
		},
	}
	mux := NewMultiplexer(cfg)
	defer mux.Stop()

	mcpServer := BuildRouter(mux)
	sseServer := server.NewSSEServer(mcpServer, server.WithSessionIDGenerator(SessionIDExtractor))
	streamableServer := server.NewStreamableHTTPServer(mcpServer,
		server.WithSessionIdManagerResolver(&RouterSessionIdManagerResolver{}),
	)

	handler := SetupHandler(cfg, mux, sseServer, streamableServer)

	t.Run("/health endpoint", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/health", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK && rec.Code != http.StatusMultiStatus {
			t.Errorf("unexpected status code for /health: %d", rec.Code)
		}
	})

	t.Run("/metrics endpoint", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/metrics", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("expected 200 OK for /metrics, got %d", rec.Code)
		}
	})

	t.Run("/sse protected without auth", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/sse", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected 401 Unauthorized for /sse without token, got %d", rec.Code)
		}
	})

	t.Run("/message protected without auth", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/message", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected 401 Unauthorized for /message without token, got %d", rec.Code)
		}
	})

	t.Run("/sse authenticated with query token", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
		defer cancel()
		req := httptest.NewRequest("GET", "/sse?token=test-secret", nil).WithContext(ctx)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code == http.StatusUnauthorized {
			t.Errorf("expected request with valid query token not to be unauthorized")
		}
	})

	t.Run("/sse authenticated with Bearer header", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
		defer cancel()
		req := httptest.NewRequest("GET", "/sse", nil).WithContext(ctx)
		req.Header.Set("Authorization", "Bearer test-secret")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code == http.StatusUnauthorized {
			t.Errorf("expected request with valid Bearer token not to be unauthorized")
		}
	})

	t.Run("/random 404 endpoint", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/non-existent-route", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Errorf("expected 404 for non-existent route, got %d", rec.Code)
		}
	})

	t.Run("/sse POST streamable protected without auth", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/sse", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected 401 Unauthorized for POST /sse without token, got %d", rec.Code)
		}
	})

	t.Run("/sse POST streamable authenticated with Bearer header", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/sse", nil)
		req.Header.Set("Authorization", "Bearer test-secret")
		req.Header.Set("Mcp-Method", "server/discover")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code == http.StatusUnauthorized || rec.Code == http.StatusMethodNotAllowed {
			t.Errorf("expected POST /sse not to be unauthorized or 405, got %d", rec.Code)
		}
	})

	t.Run("/mcp endpoint protected without auth", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/mcp", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected 401 Unauthorized for /mcp without token, got %d", rec.Code)
		}
	})

	t.Run("nil sseServer does not register sse endpoints", func(t *testing.T) {
		nilHandler := SetupHandler(cfg, mux, nil, nil)
		req := httptest.NewRequest("GET", "/sse", nil)
		rec := httptest.NewRecorder()
		nilHandler.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Errorf("expected 404 when sseServer is nil, got %d", rec.Code)
		}
	})
}

func TestServer_GracefulShutdown(t *testing.T) {
	cfg := &Config{
		Servers: map[string]ServerConfig{
			"test": {Transport: "stdio", Command: "echo"},
		},
	}
	mux := NewMultiplexer(cfg)
	defer mux.Stop()

	handler := SetupHandler(cfg, mux, nil, nil)

	// Bind to an ephemeral port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to bind ephemeral listener: %v", err)
	}

	srv := &http.Server{
		Handler: handler,
	}

	serverErrCh := make(chan error, 1)
	go func() {
		if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
			serverErrCh <- err
		}
		close(serverErrCh)
	}()

	// Verify server responds
	resp, err := http.Get("http://" + listener.Addr().String() + "/health")
	if err != nil {
		t.Fatalf("failed to query health endpoint: %v", err)
	}
	resp.Body.Close()

	// Shutdown gracefully
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		t.Errorf("expected clean shutdown, got error: %v", err)
	}

	select {
	case err := <-serverErrCh:
		if err != nil {
			t.Errorf("unexpected server error on shutdown: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Errorf("server did not exit within timeout")
	}
}

func TestInitAuditLoggerFromConfig(t *testing.T) {
	tmpDir := t.TempDir()

	t.Run("primary succeeds", func(t *testing.T) {
		cfg := &Config{
			Audit: AuditConfig{
				Path: filepath.Join(tmpDir, "prim.jsonl"),
			},
		}
		al, err := initAuditLoggerFromConfig(cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if al == nil {
			t.Fatalf("expected non-nil logger")
		}
		_ = al.Close()
	})

	t.Run("primary fails, fallback succeeds", func(t *testing.T) {
		blocker := filepath.Join(tmpDir, "blocker")
		_ = os.WriteFile(blocker, []byte("x"), 0644)
		cfg := &Config{
			Audit: AuditConfig{
				Path:         filepath.Join(blocker, "prim.jsonl"),
				FallbackPath: filepath.Join(tmpDir, "fall.jsonl"),
			},
		}
		al, err := initAuditLoggerFromConfig(cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if al == nil || !al.IsFallback() {
			t.Fatalf("expected fallback logger")
		}
		_ = al.Close()
	})

	t.Run("both fail and required=true", func(t *testing.T) {
		blocker := filepath.Join(tmpDir, "blocker")
		cfg := &Config{
			Audit: AuditConfig{
				Path:         filepath.Join(blocker, "prim.jsonl"),
				FallbackPath: filepath.Join(blocker, "fall.jsonl"),
				Required:     true,
			},
		}
		al, err := initAuditLoggerFromConfig(cfg)
		if err == nil {
			t.Fatalf("expected error when audit is required and paths fail")
		}
		if al != nil {
			t.Fatalf("expected nil logger on error")
		}
	})

	t.Run("both fail and required=false", func(t *testing.T) {
		blocker := filepath.Join(tmpDir, "blocker")
		cfg := &Config{
			Audit: AuditConfig{
				Path:         filepath.Join(blocker, "prim.jsonl"),
				FallbackPath: filepath.Join(blocker, "fall.jsonl"),
				Required:     false,
			},
		}
		al, err := initAuditLoggerFromConfig(cfg)
		if err != nil {
			t.Fatalf("expected nil error when required=false: %v", err)
		}
		if al != nil {
			t.Fatalf("expected nil logger")
		}
	})
}

func TestRouterSessionIdManager(t *testing.T) {
	t.Run("ResolveSessionIdManager", func(t *testing.T) {
		resolver := &RouterSessionIdManagerResolver{}
		req := httptest.NewRequest("GET", "/mcp?agent=test-agent", nil)
		manager := resolver.ResolveSessionIdManager(req)
		
		routerManager, ok := manager.(*RouterSessionIdManager)
		if !ok {
			t.Fatalf("expected *RouterSessionIdManager, got %T", manager)
		}
		
		if routerManager.agentID != "test-agent" {
			t.Errorf("expected agentID test-agent, got %s", routerManager.agentID)
		}
	})

	t.Run("Generate", func(t *testing.T) {
		manager := &RouterSessionIdManager{agentID: "test-agent"}
		sessionID := manager.Generate()
		
		if !strings.HasPrefix(sessionID, "test-agent:") {
			t.Errorf("expected sessionID to start with test-agent:, got %s", sessionID)
		}
	})

	t.Run("Validate", func(t *testing.T) {
		manager := &RouterSessionIdManager{agentID: "test-agent"}
		valid, err := manager.Validate("test-agent:1234")
		if valid {
			t.Errorf("expected false for Validate")
		}
		if err != nil {
			t.Errorf("expected nil error for Validate")
		}
	})

	t.Run("Terminate", func(t *testing.T) {
		manager := &RouterSessionIdManager{agentID: "test-agent"}
		terminated, err := manager.Terminate("test-agent:1234")
		if terminated {
			t.Errorf("expected false for Terminate")
		}
		if err != nil {
			t.Errorf("expected nil error for Terminate")
		}
	})
}
