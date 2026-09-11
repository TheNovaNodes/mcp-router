package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestHealthHandler_HealthyAndDegraded(t *testing.T) {
	t.Run("all backends healthy with tools", func(t *testing.T) {
		cfg := &Config{
			Servers: map[string]ServerConfig{
				"backend1": {Transport: "stdio", Command: "echo"},
			},
		}
		mux := NewMultiplexer(cfg)
		defer mux.Stop()

		mux.backends["backend1"].Client = &mockMCPClient{}
		mux.backends["backend1"].CachedTools = []mcp.Tool{{Name: "dummy_tool"}}

		handler := HealthHandler(mux)
		req := httptest.NewRequest("GET", "/health", nil)
		rec := httptest.NewRecorder()

		handler(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("expected status 200 OK, got %d", rec.Code)
		}

		var resp DetailedHealthStatus
		json.Unmarshal(rec.Body.Bytes(), &resp)

		if resp.Status != "healthy" {
			t.Errorf("expected status 'healthy', got '%s'", resp.Status)
		}
		if resp.Backends["backend1"] != "READY" {
			t.Errorf("expected backend status 'READY', got '%s'", resp.Backends["backend1"])
		}
	})

	t.Run("backend connected with zero tools reports UP and healthy", func(t *testing.T) {
		cfg := &Config{
			Servers: map[string]ServerConfig{
				"backend1": {Transport: "stdio", Command: "echo"},
			},
		}
		mux := NewMultiplexer(cfg)
		defer mux.Stop()

		mux.backends["backend1"].Client = &mockMCPClient{}
		mux.backends["backend1"].CachedTools = []mcp.Tool{} // zero tools

		handler := HealthHandler(mux)
		req := httptest.NewRequest("GET", "/health", nil)
		rec := httptest.NewRecorder()

		handler(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("expected status 200 OK for connected backend with 0 tools, got %d", rec.Code)
		}

		var resp DetailedHealthStatus
		json.Unmarshal(rec.Body.Bytes(), &resp)

		if resp.Status != "healthy" {
			t.Errorf("expected status 'healthy', got '%s'", resp.Status)
		}
		if resp.Backends["backend1"] != "UP" {
			t.Errorf("expected backend status 'UP', got '%s'", resp.Backends["backend1"])
		}
	})

	t.Run("backend degraded due to nil client with tools present", func(t *testing.T) {
		cfg := &Config{
			Servers: map[string]ServerConfig{
				"backend1": {Transport: "stdio", Command: "echo"},
			},
		}
		mux := NewMultiplexer(cfg)
		defer mux.Stop()

		mux.backends["backend1"].Client = nil
		mux.backends["backend1"].CachedTools = []mcp.Tool{{Name: "dummy_tool"}}

		handler := HealthHandler(mux)
		req := httptest.NewRequest("GET", "/health", nil)
		rec := httptest.NewRecorder()

		handler(rec, req)

		if rec.Code != http.StatusMultiStatus {
			t.Errorf("expected status 207 MultiStatus for degraded, got %d", rec.Code)
		}

		var resp DetailedHealthStatus
		json.Unmarshal(rec.Body.Bytes(), &resp)

		if resp.Status != "degraded" {
			t.Errorf("expected status 'degraded', got '%s'", resp.Status)
		}
		if resp.Backends["backend1"] != "DEGRADED" {
			t.Errorf("expected backend status 'DEGRADED', got '%s'", resp.Backends["backend1"])
		}
	})

	t.Run("backend degraded due to nil client and no tools", func(t *testing.T) {
		cfg := &Config{
			Servers: map[string]ServerConfig{
				"backend1": {Transport: "stdio", Command: "echo"},
			},
		}
		mux := NewMultiplexer(cfg)
		defer mux.Stop()

		mux.backends["backend1"].Client = nil
		mux.backends["backend1"].CachedTools = nil

		handler := HealthHandler(mux)
		req := httptest.NewRequest("GET", "/health", nil)
		rec := httptest.NewRecorder()

		handler(rec, req)

		if rec.Code != http.StatusMultiStatus {
			t.Errorf("expected status 207 MultiStatus for degraded, got %d", rec.Code)
		}

		var resp DetailedHealthStatus
		json.Unmarshal(rec.Body.Bytes(), &resp)

		if resp.Status != "degraded" {
			t.Errorf("expected status 'degraded', got '%s'", resp.Status)
		}
		if resp.Backends["backend1"] != "DEGRADED" {
			t.Errorf("expected backend status 'DEGRADED', got '%s'", resp.Backends["backend1"])
		}
	})

	t.Run("no backends configured", func(t *testing.T) {
		cfg := &Config{
			Servers: map[string]ServerConfig{},
		}
		mux := NewMultiplexer(cfg)
		defer mux.Stop()

		handler := HealthHandler(mux)
		req := httptest.NewRequest("GET", "/health", nil)
		rec := httptest.NewRecorder()

		handler(rec, req)

		if rec.Code != http.StatusMultiStatus {
			t.Errorf("expected status 207 MultiStatus for empty backends, got %d", rec.Code)
		}

		var resp DetailedHealthStatus
		json.Unmarshal(rec.Body.Bytes(), &resp)

		if resp.Status != "degraded" {
			t.Errorf("expected status 'degraded', got '%s'", resp.Status)
		}
		if len(resp.Backends) != 0 {
			t.Errorf("expected 0 backends, got %d", len(resp.Backends))
		}
	})
}

func TestBackend_IsHealthy(t *testing.T) {
	var nilBackend *Backend
	if nilBackend.IsConnected() {
		t.Errorf("nil backend should not be connected")
	}
	if nilBackend.HasTools() {
		t.Errorf("nil backend should not have tools")
	}
	if nilBackend.IsHealthy() {
		t.Errorf("nil backend should not be healthy")
	}

	b := &Backend{Name: "test"}
	if b.IsConnected() {
		t.Errorf("backend with nil client should not be connected")
	}
	if b.HasTools() {
		t.Errorf("backend without cached tools should not have tools")
	}
	if b.IsHealthy() {
		t.Errorf("backend with nil client should not be healthy")
	}

	b.CachedTools = []mcp.Tool{{Name: "tool1"}}
	if b.IsConnected() {
		t.Errorf("backend with nil client should not be connected even with cached tools")
	}
	if !b.HasTools() {
		t.Errorf("backend with cached tools should report HasTools true")
	}
	if b.IsHealthy() {
		t.Errorf("backend with nil client should not be healthy even with cached tools")
	}

	b.Client = &mockMCPClient{}
	b.CachedTools = nil
	if !b.IsConnected() {
		t.Errorf("backend with active client should be connected")
	}
	if b.HasTools() {
		t.Errorf("backend with nil tools should report HasTools false")
	}
	if !b.IsHealthy() {
		t.Errorf("backend with active client should be healthy (IsHealthy aliased to IsConnected)")
	}

	b.CachedTools = []mcp.Tool{{Name: "tool1"}}
	if !b.IsConnected() || !b.HasTools() || !b.IsHealthy() {
		t.Errorf("backend with active client and tools should report connected, hasTools, and healthy")
	}
}

func TestHealthHandler_AuditLoggerField(t *testing.T) {
	mux := NewMultiplexer(&Config{})
	defer mux.Stop()
	handler := HealthHandler(mux)

	// Case 1: logger UP
	tmpDir := t.TempDir()
	al, err := InitAuditLogger(filepath.Join(tmpDir, "health_audit.jsonl"))
	if err != nil {
		t.Fatalf("InitAuditLogger failed: %v", err)
	}
	defer al.Close()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/health", nil)
	handler(rec, req)

	var resp DetailedHealthStatus
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.AuditLogger != "UP" {
		t.Errorf("expected audit_logger 'UP', got '%s'", resp.AuditLogger)
	}

	// Case 2: logger FALLBACK
	al.isFallback = true
	rec = httptest.NewRecorder()
	handler(rec, req)
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.AuditLogger != "FALLBACK" {
		t.Errorf("expected audit_logger 'FALLBACK', got '%s'", resp.AuditLogger)
	}

	// Case 3: logger DOWN
	old := globalAuditLogger
	globalAuditLogger = nil
	rec = httptest.NewRecorder()
	handler(rec, req)
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.AuditLogger != "DOWN" {
		t.Errorf("expected audit_logger 'DOWN', got '%s'", resp.AuditLogger)
	}
	globalAuditLogger = old
}

