package main

import (
	"context"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/server"
)

func TestMultiplexer_IsAgentAllowed(t *testing.T) {
	cfg := &Config{
		Servers: map[string]ServerConfig{
			"public-backend": {
				Transport: "stdio",
				Command:   "echo",
				AllowedAgents: []string{},
			},
			"restricted-backend": {
				Transport: "stdio",
				Command:   "echo",
				AllowedAgents: []string{"agent-alpha", "agent-beta"},
			},
		},
	}

	mux := NewMultiplexer(cfg)
	defer mux.Stop()

	// Test non-existent backend
	if mux.IsAgentAllowed("non-existent", "agent-alpha") {
		t.Errorf("Expected IsAgentAllowed to return false for non-existent backend")
	}

	// Test backend with empty AllowedAgents (allowed for everyone)
	if !mux.IsAgentAllowed("public-backend", "agent-anyone") {
		t.Errorf("Expected public-backend to allow any agent")
	}

	// Test backend with explicit AllowedAgents
	if !mux.IsAgentAllowed("restricted-backend", "agent-alpha") {
		t.Errorf("Expected restricted-backend to allow agent-alpha")
	}
	if !mux.IsAgentAllowed("restricted-backend", "agent-beta") {
		t.Errorf("Expected restricted-backend to allow agent-beta")
	}
	if mux.IsAgentAllowed("restricted-backend", "agent-gamma") {
		t.Errorf("Expected restricted-backend to deny agent-gamma")
	}
}

func TestMultiplexer_StartBackends_EmptyCommandAndInvalidTransport(t *testing.T) {
	cfg := &Config{
		Servers: map[string]ServerConfig{
			"empty-cmd": {
				Transport: "stdio",
				Command:   "",
			},
			"invalid-transport": {
				Transport: "ftp",
			},
		},
	}

	mux := NewMultiplexer(cfg)
	defer mux.Stop()

	err := mux.StartBackends()
	if err != nil {
		t.Errorf("StartBackends should handle invalid configs gracefully, got: %v", err)
	}
}

func TestSessionIDExtractor(t *testing.T) {
	t.Run("with X-Agent-ID header generates unique session ID with prefix", func(t *testing.T) {
		req1 := httptest.NewRequest("GET", "/mcp", nil)
		req1.Header.Set("X-Agent-ID", "jules-bot")

		id1, err := SessionIDExtractor(context.Background(), req1)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.HasPrefix(id1, "jules-bot:") {
			t.Errorf("expected session ID to start with 'jules-bot:', got '%s'", id1)
		}
		if ExtractAgentID(id1) != "jules-bot" {
			t.Errorf("expected extracted agent ID to be 'jules-bot', got '%s'", ExtractAgentID(id1))
		}

		// Verify uniqueness across concurrent / consecutive requests
		req2 := httptest.NewRequest("GET", "/mcp", nil)
		req2.Header.Set("X-Agent-ID", "jules-bot")
		id2, err := SessionIDExtractor(context.Background(), req2)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if id1 == id2 {
			t.Errorf("expected unique session IDs for consecutive requests, both got '%s'", id1)
		}
	})

	t.Run("without X-Agent-ID header fallback", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/mcp", nil)

		id, err := SessionIDExtractor(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.HasPrefix(id, "anonymous:") {
			t.Errorf("expected session ID to start with 'anonymous:', got '%s'", id)
		}
		if ExtractAgentID(id) != "anonymous" {
			t.Errorf("expected extracted agent ID to be 'anonymous', got '%s'", ExtractAgentID(id))
		}
	})
}

func TestExtractAgentID(t *testing.T) {
	tests := []struct {
		name      string
		sessionID string
		expected  string
	}{
		{
			name:      "composite session ID with uuid",
			sessionID: "prometheus:123e4567-e89b-12d3-a456-426614174000",
			expected:  "prometheus",
		},
		{
			name:      "composite session ID with colon in agent name",
			sessionID: "ns:agent:uuid-123",
			expected:  "ns",
		},
		{
			name:      "legacy session ID without colon",
			sessionID: "jules-bot",
			expected:  "jules-bot",
		},
		{
			name:      "anonymous with uuid",
			sessionID: "anonymous:550e8400-e29b-41d4-a716-446655440000",
			expected:  "anonymous",
		},
		{
			name:      "empty session ID",
			sessionID: "",
			expected:  "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ExtractAgentID(tc.sessionID)
			if got != tc.expected {
				t.Errorf("ExtractAgentID(%q) = %q, want %q", tc.sessionID, got, tc.expected)
			}
		})
	}
}

func TestBuildRouter(t *testing.T) {
	cfg := &Config{
		Servers: map[string]ServerConfig{
			"test-backend": {
				Transport: "stdio",
				Command:   "echo",
			},
		},
	}

	mux := NewMultiplexer(cfg)
	defer mux.Stop()

	server := BuildRouter(mux)
	if server == nil {
		t.Fatalf("BuildRouter returned nil server")
	}
}

func TestMultiplexer_GetBackends(t *testing.T) {
	cfg := &Config{
		Servers: map[string]ServerConfig{
			"b1": {Transport: "stdio", Command: "echo"},
			"b2": {Transport: "stdio", Command: "echo"},
		},
	}
	mux := NewMultiplexer(cfg)
	defer mux.Stop()

	backends := mux.GetBackends()
	if len(backends) != 2 {
		t.Fatalf("expected 2 backends, got %d", len(backends))
	}

	b1, exists := mux.GetBackend("b1")
	if !exists || b1 == nil || b1.Name != "b1" {
		t.Errorf("expected to find backend b1")
	}

	_, exists = mux.GetBackend("non-existent")
	if exists {
		t.Errorf("expected non-existent backend to not exist")
	}

	// Verify slice mutation does not affect internal map
	backends[0] = nil
	newBackends := mux.GetBackends()
	for _, b := range newBackends {
		if b == nil {
			t.Errorf("internal backends map corrupted by slice mutation")
		}
	}
}

func TestMultiplexer_Reload(t *testing.T) {
	initialCfg := &Config{
		Servers: map[string]ServerConfig{
			"keep-me": {
				Transport: "stdio",
				Command:   "echo",
				Args:      []string{"keep"},
			},
			"update-me": {
				Transport: "stdio",
				Command:   "echo",
				Args:      []string{"old-arg"},
			},
			"remove-me": {
				Transport: "stdio",
				Command:   "echo",
				Args:      []string{"bye"},
			},
		},
	}

	mux := NewMultiplexer(initialCfg)
	defer mux.Stop()

	var keepClosed, updateClosed, removeClosed int32

	keepBackend, _ := mux.GetBackend("keep-me")
	keepBackend.Client = &mockMCPClient{
		closeFunc: func() error {
			atomic.AddInt32(&keepClosed, 1)
			return nil
		},
	}

	updateBackend, _ := mux.GetBackend("update-me")
	updateBackend.Client = &mockMCPClient{
		closeFunc: func() error {
			atomic.AddInt32(&updateClosed, 1)
			return nil
		},
	}

	removeBackend, _ := mux.GetBackend("remove-me")
	removeBackend.Client = &mockMCPClient{
		closeFunc: func() error {
			atomic.AddInt32(&removeClosed, 1)
			return nil
		},
	}

	newCfg := &Config{
		Servers: map[string]ServerConfig{
			"keep-me": {
				Transport: "stdio",
				Command:   "echo",
				Args:      []string{"keep"},
			},
			"update-me": {
				Transport: "stdio",
				Command:   "echo",
				Args:      []string{"new-arg"}, // Changed!
			},
			"add-me": {
				Transport: "stdio",
				Command:   "echo",
				Args:      []string{"hello"},
			},
		},
	}

	report := mux.Reload(newCfg)

	// Verify report
	if len(report.Kept) != 1 || report.Kept[0] != "keep-me" {
		t.Errorf("expected Kept=[keep-me], got %v", report.Kept)
	}
	if len(report.Updated) != 1 || report.Updated[0] != "update-me" {
		t.Errorf("expected Updated=[update-me], got %v", report.Updated)
	}
	if len(report.Removed) != 1 || report.Removed[0] != "remove-me" {
		t.Errorf("expected Removed=[remove-me], got %v", report.Removed)
	}
	if len(report.Added) != 1 || report.Added[0] != "add-me" {
		t.Errorf("expected Added=[add-me], got %v", report.Added)
	}

	// Verify client closures
	if atomic.LoadInt32(&keepClosed) != 0 {
		t.Errorf("expected keep-me client NOT to be closed, got %d", keepClosed)
	}
	if atomic.LoadInt32(&updateClosed) != 1 {
		t.Errorf("expected update-me old client to be closed once, got %d", updateClosed)
	}
	if atomic.LoadInt32(&removeClosed) != 1 {
		t.Errorf("expected remove-me client to be closed once, got %d", removeClosed)
	}

	// Verify state of Multiplexer
	currentKeep, exists := mux.GetBackend("keep-me")
	if !exists || currentKeep != keepBackend {
		t.Errorf("expected keep-me backend pointer to remain identical")
	}

	_, exists = mux.GetBackend("remove-me")
	if exists {
		t.Errorf("expected remove-me to be removed from mux")
	}

	currentUpdate, exists := mux.GetBackend("update-me")
	if !exists || len(currentUpdate.Config.Args) == 0 || currentUpdate.Config.Args[0] != "new-arg" {
		t.Errorf("expected update-me to have updated config")
	}

	currentAdd, exists := mux.GetBackend("add-me")
	if !exists || currentAdd.Name != "add-me" {
		t.Errorf("expected add-me to be present in mux")
	}
}

func TestMultiplexer_Reload_Nil(t *testing.T) {
	cfg := &Config{
		Servers: map[string]ServerConfig{
			"b1": {Transport: "stdio", Command: "echo"},
		},
	}
	mux := NewMultiplexer(cfg)
	defer mux.Stop()

	report := mux.Reload(nil)
	if len(report.Kept) != 0 || len(report.Added) != 0 || len(report.Updated) != 0 || len(report.Removed) != 0 {
		t.Errorf("expected empty report on nil reload")
	}

	if len(mux.GetBackends()) != 1 {
		t.Errorf("expected backends to remain untouched on nil reload")
	}
}

func TestMultiplexer_Reload_ConcurrentAccess(t *testing.T) {
	cfgA := &Config{
		Servers: map[string]ServerConfig{
			"shared": {Transport: "stdio", Command: "echo", Args: []string{"a"}},
		},
	}
	cfgB := &Config{
		Servers: map[string]ServerConfig{
			"shared": {Transport: "stdio", Command: "echo", Args: []string{"b"}},
		},
	}

	mux := NewMultiplexer(cfgA)
	defer mux.Stop()

	stopCh := make(chan struct{})
	var wg sync.WaitGroup

	// Reader goroutines
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stopCh:
					return
				default:
					_ = mux.GetBackends()
					_, _ = mux.GetBackend("shared")
					_ = mux.IsAgentAllowed("shared", "test-agent")
					time.Sleep(1 * time.Millisecond)
				}
			}
		}()
	}

	// Writer reload goroutine
	for i := 0; i < 10; i++ {
		if i%2 == 0 {
			mux.Reload(cfgB)
		} else {
			mux.Reload(cfgA)
		}
		time.Sleep(5 * time.Millisecond)
	}

	close(stopCh)
	wg.Wait()
}

func TestMultiplexer_Reload_NoDowntimeWindow(t *testing.T) {
	cfgA := &Config{
		Servers: map[string]ServerConfig{
			"worker": {
				Transport: "stdio",
				Command:   "echo",
				Args:      []string{"a"},
				Prefix:    "worker__",
			},
		},
	}
	cfgB := &Config{
		Servers: map[string]ServerConfig{
			"worker": {
				Transport: "stdio",
				Command:   "echo",
				Args:      []string{"b"},
				Prefix:    "worker__",
			},
		},
	}

	mux := NewMultiplexer(cfgA)
	defer mux.Stop()

	// Populate mock client
	workerBackend, ok := mux.GetBackend("worker")
	if !ok {
		t.Fatalf("worker backend not found initially")
	}
	workerBackend.Client = &mockMCPClient{}

	stopCh := make(chan struct{})
	var missingCount int64
	var wg sync.WaitGroup

	// Tight loop querying GetBackend("worker") concurrently
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stopCh:
					return
				default:
					b, exists := mux.GetBackend("worker")
					if !exists || b == nil {
						atomic.AddInt64(&missingCount, 1)
					}
					time.Sleep(100 * time.Microsecond)
				}
			}
		}()
	}

	// Trigger reload
	mux.Reload(cfgB)
	close(stopCh)
	wg.Wait()

	if missingCount > 0 {
		t.Errorf("zero-downtime violated: worker backend was missing %d times during reload", missingCount)
	}
}

func TestStartSingleBackend_EdgeCases(t *testing.T) {
	mux := NewMultiplexer(&Config{Servers: map[string]ServerConfig{}})
	defer mux.Stop()

	t.Run("empty stdio command", func(t *testing.T) {
		b := &Backend{
			Name: "empty-cmd",
			Config: ServerConfig{
				Transport: "stdio",
				Command:   "",
			},
		}
		mux.startSingleBackend(b)
		if b.Client != nil {
			t.Errorf("expected client to remain nil on empty command")
		}
	})

	t.Run("stdio command with spaces and no args", func(t *testing.T) {
		b := &Backend{
			Name: "space-cmd",
			Config: ServerConfig{
				Transport: "stdio",
				Command:   "echo hello world",
				Args:      nil,
			},
		}
		mux.startSingleBackend(b)
		// echo terminates immediately, but startSingleBackend handles strings.Fields cleanly
	})

	t.Run("stdio command non-existent binary", func(t *testing.T) {
		b := &Backend{
			Name: "bad-bin",
			Config: ServerConfig{
				Transport: "stdio",
				Command:   "/non/existent/executable/binary/mcp",
			},
		}
		mux.startSingleBackend(b)
		if b.Client != nil {
			t.Errorf("expected client to remain nil on non-existent binary")
		}
	})

	t.Run("http transport without headers", func(t *testing.T) {
		b := &Backend{
			Name: "http-no-headers",
			Config: ServerConfig{
				Transport: "http",
				URL:       "http://127.0.0.1:59999/mcp", // offline port
			},
		}
		mux.startSingleBackend(b)
		if b.Client != nil {
			t.Errorf("expected client to remain nil on unreachable http endpoint")
		}
	})

	t.Run("http transport with custom headers", func(t *testing.T) {
		b := &Backend{
			Name: "http-headers",
			Config: ServerConfig{
				Transport: "http",
				URL:       "http://127.0.0.1:59999/mcp",
				Headers: map[string]string{
					"Authorization": "Bearer test-key",
					"X-Custom":      "value",
				},
			},
		}
		mux.startSingleBackend(b)
		if b.Client != nil {
			t.Errorf("expected client to remain nil on unreachable http endpoint")
		}
	})

	t.Run("streamable_http transport without headers", func(t *testing.T) {
		b := &Backend{
			Name: "streamable-no-headers",
			Config: ServerConfig{
				Transport: "streamable_http",
				URL:       "http://127.0.0.1:59999/mcp",
			},
		}
		mux.startSingleBackend(b)
		if b.Client != nil {
			t.Errorf("expected client to remain nil on unreachable streamable_http endpoint")
		}
	})

	t.Run("streamable_http transport with custom headers", func(t *testing.T) {
		b := &Backend{
			Name: "streamable-headers",
			Config: ServerConfig{
				Transport: "streamable_http",
				URL:       "http://127.0.0.1:59999/mcp",
				Headers: map[string]string{
					"Authorization": "Bearer streamable-test-key",
				},
			},
		}
		mux.startSingleBackend(b)
		if b.Client != nil {
			t.Errorf("expected client to remain nil on unreachable streamable_http endpoint")
		}
	})

	t.Run("streamable transport alias", func(t *testing.T) {
		b := &Backend{
			Name: "streamable-alias",
			Config: ServerConfig{
				Transport: "streamable",
				URL:       "http://127.0.0.1:59999/mcp",
			},
		}
		mux.startSingleBackend(b)
		if b.Client != nil {
			t.Errorf("expected client to remain nil on unreachable streamable endpoint")
		}
	})

	t.Run("unknown transport protocol", func(t *testing.T) {
		b := &Backend{
			Name: "unknown-proto",
			Config: ServerConfig{
				Transport: "websocket",
			},
		}
		mux.startSingleBackend(b)
		if b.Client != nil {
			t.Errorf("expected client to remain nil on unknown transport")
		}
	})

	t.Run("stdio command with space and empty args", func(t *testing.T) {
		b := &Backend{
			Name: "stdio-space",
			Config: ServerConfig{
				Transport: "stdio",
				Command:   "echo arg1",
			},
		}
		// echo terminates immediately on stdin close, but initialization starts correctly
		mux.startSingleBackend(b)
	})

	t.Run("http without headers", func(t *testing.T) {
		b := &Backend{
			Name: "http-no-headers",
			Config: ServerConfig{
				Transport: "http",
				URL:       "http://127.0.0.1:59998/sse",
			},
		}
		mux.startSingleBackend(b)
		if b.Client != nil {
			t.Errorf("expected client to remain nil on unreachable http endpoint")
		}
	})
}




func TestStartSingleBackend_EmptyCommandError(t *testing.T) {
	mux := NewMultiplexer(&Config{})
	b := &Backend{
		Name: "b1",
		Config: ServerConfig{
			Transport: "stdio",
			Command: "", // Missing command
		},
	}
	
	// This should not panic and should print "empty command for backend b1"
	mux.startSingleBackend(b)
	
	if b.Client != nil {
		t.Errorf("expected client to be nil")
	}
}

func TestStartSingleBackend_InvalidTransport(t *testing.T) {
	mux := NewMultiplexer(&Config{})
	b := &Backend{
		Name: "b1",
		Config: ServerConfig{
			Transport: "invalid",
		},
	}
	
	// This should not panic and should print "unsupported transport: invalid"
	mux.startSingleBackend(b)
	
	if b.Client != nil {
		t.Errorf("expected client to be nil")
	}
}

func TestStartSingleBackend_SSE(t *testing.T) {
	mux := NewMultiplexer(&Config{})
	b := &Backend{
		Name: "b1",
		Config: ServerConfig{
			Transport: "sse",
			URL:       "http://localhost:1234/sse", // This will fail to start and print "failed to start sse transport"
		},
	}
	
	mux.startSingleBackend(b)
	
	if b.Client != nil {
		t.Errorf("expected client to be nil on start failure")
	}
}

func TestStartSingleBackend_StreamableHTTP(t *testing.T) {
	mux := NewMultiplexer(&Config{})
	b := &Backend{
		Name: "b2",
		Config: ServerConfig{
			Transport: "streamable_http",
			URL:       "http://localhost:1234/mcp", // Will fail similarly
		},
	}
	
	mux.startSingleBackend(b)
	
	if b.Client != nil {
		t.Errorf("expected client to be nil on start failure")
	}
}

func TestStartSingleBackend_SSE_Success(t *testing.T) {
	mcpServer := server.NewMCPServer("mock-sse-backend", "1.0.0")
	ts := server.NewTestServer(mcpServer)
	defer ts.Close()

	cfg := &Config{
		Servers: map[string]ServerConfig{
			"live-sse": {
				Transport: "sse",
				URL:       ts.URL + "/sse",
			},
		},
	}
	mux := NewMultiplexer(cfg)
	defer mux.Stop()

	b, ok := mux.GetBackend("live-sse")
	if !ok {
		t.Fatalf("backend live-sse not found")
	}

	mux.startSingleBackend(b)

	if b.Client == nil {
		t.Fatalf("expected b.Client to be non-nil after successful SSE handshake")
	}
	if !b.IsConnected() {
		t.Errorf("expected backend to report IsConnected = true")
	}
}

func TestStartSingleBackend_StreamableHTTP_Success(t *testing.T) {
	mcpServer := server.NewMCPServer("mock-streamable-backend", "1.0.0")
	streamableServer := server.NewStreamableHTTPServer(mcpServer)

	ts := httptest.NewServer(streamableServer)
	defer ts.Close()

	cfg := &Config{
		Servers: map[string]ServerConfig{
			"live-streamable": {
				Transport: "streamable_http",
				URL:       ts.URL,
			},
		},
	}
	mux := NewMultiplexer(cfg)
	defer mux.Stop()

	b, ok := mux.GetBackend("live-streamable")
	if !ok {
		t.Fatalf("backend live-streamable not found")
	}

	mux.startSingleBackend(b)

	if b.Client == nil {
		t.Fatalf("expected b.Client to be non-nil after successful StreamableHTTP handshake")
	}
	if !b.IsConnected() {
		t.Errorf("expected backend to report IsConnected = true")
	}
}

func TestMultiplexer_Stop_ClosesAllBackends_Idempotent(t *testing.T) {
	cfg := &Config{
		Servers: map[string]ServerConfig{
			"b1": {Transport: "stdio", Command: "echo"},
			"b2": {Transport: "stdio", Command: "echo"},
		},
	}
	mux := NewMultiplexer(cfg)
	defer mux.Stop()

	var b1Closed, b2Closed int32

	b1, _ := mux.GetBackend("b1")
	b1.Client = &mockMCPClient{
		closeFunc: func() error {
			atomic.AddInt32(&b1Closed, 1)
			return nil
		},
	}

	b2, _ := mux.GetBackend("b2")
	b2.Client = &mockMCPClient{
		closeFunc: func() error {
			atomic.AddInt32(&b2Closed, 1)
			return nil
		},
	}

	// First explicit stop
	mux.Stop()

	if atomic.LoadInt32(&b1Closed) != 1 {
		t.Errorf("expected b1 to be closed exactly once, got %d", b1Closed)
	}
	if atomic.LoadInt32(&b2Closed) != 1 {
		t.Errorf("expected b2 to be closed exactly once, got %d", b2Closed)
	}

	// Second explicit stop (should be a no-op due to sync.Once)
	mux.Stop()

	if atomic.LoadInt32(&b1Closed) != 1 {
		t.Errorf("expected b1 closed count to remain 1, got %d", b1Closed)
	}
	if atomic.LoadInt32(&b2Closed) != 1 {
		t.Errorf("expected b2 closed count to remain 1, got %d", b2Closed)
	}
}
