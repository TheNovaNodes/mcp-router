package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuthMiddleware_Disabled(t *testing.T) {
	cfg := AuthConfig{Enabled: false}
	handler := AuthMiddleware(cfg, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/sse", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected StatusOK when auth disabled, got %d", rec.Code)
	}
}

func TestAuthMiddleware_ValidHeaderToken(t *testing.T) {
	cfg := AuthConfig{Enabled: true, BearerToken: "secret123"}
	handler := AuthMiddleware(cfg, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/sse", nil)
	req.Header.Set("Authorization", "Bearer secret123")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected StatusOK with valid header, got %d", rec.Code)
	}
}

func TestAuthMiddleware_RejectsQueryTokenByDefault(t *testing.T) {
	// Default AllowQueryToken is false -> query param ?token= must be rejected with 401
	cfg := AuthConfig{Enabled: true, BearerToken: "secret123", AllowQueryToken: false}
	handler := AuthMiddleware(cfg, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/sse?token=secret123", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected StatusUnauthorized when AllowQueryToken=false, got %d", rec.Code)
	}

	wwwAuth := rec.Header().Get("WWW-Authenticate")
	if !strings.Contains(wwwAuth, `Bearer realm="mcp-router"`) {
		t.Errorf("expected WWW-Authenticate header with realm, got '%s'", wwwAuth)
	}
}

func TestAuthMiddleware_ValidQueryTokenWhenAllowed(t *testing.T) {
	// Explicitly enabled AllowQueryToken -> query param ?token= is accepted
	cfg := AuthConfig{Enabled: true, BearerToken: "secret123", AllowQueryToken: true}
	handler := AuthMiddleware(cfg, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/sse?token=secret123", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected StatusOK with valid query param when AllowQueryToken=true, got %d", rec.Code)
	}
}

func TestAuthMiddleware_InvalidToken(t *testing.T) {
	cfg := AuthConfig{Enabled: true, BearerToken: "secret123"}
	handler := AuthMiddleware(cfg, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/sse", nil)
	req.Header.Set("Authorization", "Bearer wrongtoken")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected StatusUnauthorized for wrong token, got %d", rec.Code)
	}
}

func TestAuthMiddleware_TokenWithSpaces(t *testing.T) {
	cfg := AuthConfig{Enabled: true, BearerToken: "secret123"}
	handler := AuthMiddleware(cfg, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/sse", nil)
	req.Header.Set("Authorization", "Bearer  secret123 ")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected StatusOK with trimmed token, got %d", rec.Code)
	}
}

func TestAuthMiddleware_EmptyOrDifferentLength(t *testing.T) {
	cfg := AuthConfig{Enabled: true, BearerToken: "secret123"}
	handler := AuthMiddleware(cfg, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for _, tc := range []struct {
		name   string
		header string
	}{
		{"empty bearer", "Bearer "},
		{"no header", ""},
		{"shorter token", "Bearer sec"},
		{"longer token", "Bearer secret123extra"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/sse", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("expected StatusUnauthorized for %s, got %d", tc.name, rec.Code)
			}
		})
	}
}

func TestAuthMiddleware_Logs401ToAudit(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "auth_audit.jsonl")

	al, err := InitAuditLogger(logPath)
	if err != nil {
		t.Fatalf("failed to init audit logger: %v", err)
	}
	defer al.Close()

	cfg := AuthConfig{Enabled: true, BearerToken: "correct-token"}
	handler := AuthMiddleware(cfg, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/sse", nil)
	req.Header.Set("Authorization", "Bearer invalid-attempt")
	req.Header.Set("X-Agent-ID", "rogue-agent")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized, got %d", rec.Code)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("failed to read audit log: %v", err)
	}

	logStr := string(data)
	if !strings.Contains(logStr, `"event_type":"SECURITY_BLOCK"`) {
		t.Errorf("expected audit event_type SECURITY_BLOCK in audit log, got: %s", logStr)
	}
	if !strings.Contains(logStr, `"agent_id":"rogue-agent"`) {
		t.Errorf("expected agent_id rogue-agent in audit log, got: %s", logStr)
	}
	if !strings.Contains(logStr, `"backend":"auth"`) {
		t.Errorf("expected backend auth in audit log, got: %s", logStr)
	}
}

func TestAuthMiddleware_CaseInsensitiveBearer(t *testing.T) {
	cfg := AuthConfig{Enabled: true, BearerToken: "my-secret-key"}
	handler := AuthMiddleware(cfg, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	variations := []string{
		"Bearer my-secret-key",
		"bearer my-secret-key",
		"BEARER my-secret-key",
		"bEaReR my-secret-key",
	}

	for _, v := range variations {
		req := httptest.NewRequest("GET", "/sse", nil)
		req.Header.Set("Authorization", v)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("expected StatusOK for authorization header '%s', got %d", v, rec.Code)
		}
	}
}
