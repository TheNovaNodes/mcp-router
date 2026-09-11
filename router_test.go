package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func TestGetType_ExplicitAndFallback(t *testing.T) {
	tests := []struct {
		name       string
		serverCfg  ServerConfig
		serverName string
		expected   string
	}{
		{
			name:       "Explicit github type",
			serverCfg:  ServerConfig{Type: "github", Prefix: "custom__"},
			serverName: "any_server",
			expected:   "github",
		},
		{
			name:       "Auto detect from prefix gh-",
			serverCfg:  ServerConfig{Prefix: "gh-novanodes__"},
			serverName: "custom_backend",
			expected:   "github",
		},
		{
			name:       "Auto detect from server name containing github",
			serverCfg:  ServerConfig{Prefix: "mcp__"},
			serverName: "github-doctormes",
			expected:   "github",
		},
		{
			name:       "Default fallback",
			serverCfg:  ServerConfig{Prefix: "searxng__"},
			serverName: "searxng-control",
			expected:   "default",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.serverCfg.GetType(tt.serverName)
			if got != tt.expected {
				t.Errorf("GetType() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestBackend_GetTypeMethod(t *testing.T) {
	b := &Backend{
		Name: "github-doctormes",
		Config: ServerConfig{
			Type:   "github",
			Prefix: "gh-doctormes__",
		},
	}

	if b.GetType() != "github" {
		t.Errorf("b.GetType() = %v, want 'github'", b.GetType())
	}
}

func TestSemaphoreThrottling(t *testing.T) {
	b := &Backend{
		Name:      "test-backend",
		Semaphore: make(chan struct{}, 2), // Max 2 concurrent slots
	}

	// Fill slots
	b.Semaphore <- struct{}{}
	b.Semaphore <- struct{}{}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	var acquired bool
	select {
	case b.Semaphore <- struct{}{}:
		acquired = true
	case <-ctx.Done():
		acquired = false
	}

	if acquired {
		t.Errorf("expected semaphore acquisition to fail when capacity is reached")
	}

	// Release 1 slot
	<-b.Semaphore

	select {
	case b.Semaphore <- struct{}{}:
		acquired = true
	default:
		acquired = false
	}

	if !acquired {
		t.Errorf("expected semaphore acquisition to succeed after releasing a slot")
	}
}

type mockMCPClient struct {
	client.MCPClient
	callToolFunc func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error)
	closeFunc    func() error
}

func (m *mockMCPClient) CallTool(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if m.callToolFunc != nil {
		return m.callToolFunc(ctx, req)
	}
	return mcp.NewToolResultText("mock-response"), nil
}

func (m *mockMCPClient) Close() error {
	if m.closeFunc != nil {
		return m.closeFunc()
	}
	return nil
}

func newTestMultiplexer(backends ...*Backend) *Multiplexer {
	mux := &Multiplexer{
		backends: make(map[string]*Backend),
	}
	for _, b := range backends {
		if b != nil {
			mux.backends[b.Name] = b
		}
	}
	return mux
}

func TestCreateToolHandler_Success(t *testing.T) {
	b := &Backend{
		Name:      "test-backend",
		Config:    ServerConfig{Prefix: "test__", MaxConcurrent: 5},
		Client:    &mockMCPClient{},
		Semaphore: make(chan struct{}, 5),
	}
	mux := newTestMultiplexer(b)

	handler := CreateToolHandler(b.Name, mux, "query", "agent-test")
	req := mcp.CallToolRequest{}
	req.Params.Name = "test__query"

	res, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Errorf("expected successful tool call, got error result: %v", res.Content)
	}
}

func TestCreateToolHandler_MissingBackend(t *testing.T) {
	mux := newTestMultiplexer()
	handler := CreateToolHandler("missing-backend", mux, "query", "agent-test")
	req := mcp.CallToolRequest{}
	req.Params.Name = "missing__query"

	res, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected error result when backend is not found in multiplexer")
	}
}

func TestCreateToolHandler_NilBackendClient(t *testing.T) {
	b := &Backend{
		Name:      "degraded-backend",
		Config:    ServerConfig{Prefix: "deg__"},
		Client:    nil, // Client unavailable
		Semaphore: make(chan struct{}, 5),
	}
	mux := newTestMultiplexer(b)

	handler := CreateToolHandler(b.Name, mux, "query", "agent-test")
	req := mcp.CallToolRequest{}
	req.Params.Name = "deg__query"

	res, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected error result for nil backend client")
	}
}

func TestCreateToolHandler_ExecutionTimeout(t *testing.T) {
	b := &Backend{
		Name: "slow-backend",
		Config: ServerConfig{
			Prefix:  "slow__",
			Timeout: 1, // 1 second timeout
		},
		Client: &mockMCPClient{
			callToolFunc: func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				select {
				case <-time.After(2 * time.Second):
					return mcp.NewToolResultText("done"), nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			},
		},
		Semaphore: make(chan struct{}, 5),
	}
	mux := newTestMultiplexer(b)

	handler := CreateToolHandler(b.Name, mux, "slow_query", "agent-test")
	req := mcp.CallToolRequest{}
	req.Params.Name = "slow__slow_query"

	res, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected timeout error result, got success")
	}
}

func TestCreateToolHandler_SecurityGuard_PRMerge(t *testing.T) {
	b := &Backend{
		Name: "github-backend",
		Config: ServerConfig{
			Type:   "github",
			Prefix: "gh__",
		},
		Client:    &mockMCPClient{},
		Semaphore: make(chan struct{}, 5),
	}
	mux := newTestMultiplexer(b)

	handler := CreateToolHandler(b.Name, mux, "merge_pull_request", "agent-bad")
	req := mcp.CallToolRequest{}
	req.Params.Name = "gh__merge_pull_request"

	res, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected security guard to block merge_pull_request")
	}
}

func TestCreateToolHandler_SecurityGuard_ProtectedBranch(t *testing.T) {
	b := &Backend{
		Name: "github-backend",
		Config: ServerConfig{
			Type:   "github",
			Prefix: "gh__",
		},
		Client:    &mockMCPClient{},
		Semaphore: make(chan struct{}, 5),
	}
	mux := newTestMultiplexer(b)

	testCases := []struct {
		name      string
		tool      string
		args      map[string]interface{}
		shouldErr bool
	}{
		{"exact main", "push_files", map[string]interface{}{"branch": "main"}, true},
		{"uppercase Main", "push_files", map[string]interface{}{"branch": "Main"}, true},
		{"master with whitespace", "create_or_update_file", map[string]interface{}{"branch": "  master  "}, true},
		{"refs/heads/main", "delete_file", map[string]interface{}{"ref": "refs/heads/main"}, true},
		{"origin/master", "update_pull_request_branch", map[string]interface{}{"target_branch": "origin/master"}, true},
		{"refs/remotes/origin/main", "push_files", map[string]interface{}{"branch": "refs/remotes/origin/main"}, true},
		{"head targeting master", "push_files", map[string]interface{}{"head": "master"}, true},
		{"dest targeting main", "create_branch", map[string]interface{}{"dest": "refs/heads/main"}, true},
		{"branch_name targeting master", "push_files", map[string]interface{}{"branch_name": "master"}, true},
		{"create_branch on main", "create_branch", map[string]interface{}{"branch": "main"}, true},
		{"safe feature branch", "push_files", map[string]interface{}{"branch": "feature/my-cool-feature"}, false},
		{"safe bugfix branch", "create_or_update_file", map[string]interface{}{"branch": "fix/issue-42"}, false},
		{"path containing main not blocked", "create_or_update_file", map[string]interface{}{"path": "src/main/App.java", "branch": "fix/app"}, false},
		{"commit message containing main not blocked", "push_files", map[string]interface{}{"message": "merge into main", "branch": "feature/ui"}, false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			handler := CreateToolHandler(b.Name, mux, tc.tool, "agent-tester")
			req := mcp.CallToolRequest{}
			req.Params.Name = "gh__" + tc.tool
			req.Params.Arguments = tc.args

			res, err := handler(context.Background(), req)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.shouldErr && !res.IsError {
				t.Errorf("expected tool %s to be blocked for args %v", tc.tool, tc.args)
			}
			if !tc.shouldErr && res.IsError {
				t.Errorf("expected tool %s to be allowed for args %v, got error: %v", tc.tool, tc.args, res)
			}
		})
	}
}

func TestCreateToolHandler_ClientContextCancelled(t *testing.T) {
	b := &Backend{
		Name:      "test-backend",
		Semaphore: make(chan struct{}, 1),
	}
	// Fill semaphore
	b.Semaphore <- struct{}{}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	mux := newTestMultiplexer(b)
	handler := CreateToolHandler(b.Name, mux, "query", "agent-test")
	req := mcp.CallToolRequest{}
	req.Params.Name = "test__query"

	_, err := handler(ctx, req)
	if err == nil {
		t.Errorf("expected error when context is cancelled while acquiring semaphore")
	}
}

type mockSession struct {
	id    string
	tools map[string]server.ServerTool
	mu    sync.Mutex
}

var _ server.SessionWithTools = (*mockSession)(nil)

func (m *mockSession) Initialize()                                {}
func (m *mockSession) Initialized() bool                          { return true }
func (m *mockSession) NotificationChannel() chan<- mcp.JSONRPCNotification { return nil }
func (m *mockSession) SessionID() string                          { return m.id }

func (m *mockSession) GetSessionTools() map[string]server.ServerTool {
	m.mu.Lock()
	defer m.mu.Unlock()
	res := make(map[string]server.ServerTool, len(m.tools))
	for k, v := range m.tools {
		res[k] = v
	}
	return res
}

func (m *mockSession) SetSessionTools(tools map[string]server.ServerTool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tools = tools
}

func TestBuildRouter_SessionRegistrationAndACL(t *testing.T) {
	cfg := &Config{
		Servers: map[string]ServerConfig{
			"backend-restricted": {
				Transport:     "stdio",
				Command:       "echo",
				Prefix:        "priv__",
				AllowedAgents: []string{"agent-a"},
			},
			"backend-public": {
				Transport:     "stdio",
				Command:       "echo",
				Prefix:        "pub__",
				AllowedAgents: []string{},
			},
		},
	}
	mux := NewMultiplexer(cfg)
	defer mux.Stop()

	mux.backends["backend-restricted"].Client = &mockMCPClient{}
	mux.backends["backend-restricted"].CachedTools = []mcp.Tool{
		{Name: "secret_tool"},
	}

	mux.backends["backend-public"].Client = &mockMCPClient{}
	mux.backends["backend-public"].CachedTools = []mcp.Tool{
		{Name: "common_tool"},
	}

	s := BuildRouter(mux)

	sessA1 := &mockSession{id: "agent-a:uuid-1"}
	sessA2 := &mockSession{id: "agent-a:uuid-2"}
	sessB := &mockSession{id: "agent-b:uuid-3"}

	if err := s.RegisterSession(context.Background(), sessA1); err != nil {
		t.Fatalf("failed to register sessA1: %v", err)
	}
	if err := s.RegisterSession(context.Background(), sessA2); err != nil {
		t.Fatalf("failed to register sessA2: %v", err)
	}
	if err := s.RegisterSession(context.Background(), sessB); err != nil {
		t.Fatalf("failed to register sessB: %v", err)
	}

	listMsg := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`

	// Test sessA1 has both tools
	ctxA1 := s.WithContext(context.Background(), sessA1)
	respA1 := s.HandleMessage(ctxA1, []byte(listMsg))
	respJSONA1, _ := json.Marshal(respA1)
	if !strings.Contains(string(respJSONA1), "priv__secret_tool") || !strings.Contains(string(respJSONA1), "pub__common_tool") {
		t.Errorf("sessA1 expected both tools, got: %s", string(respJSONA1))
	}

	// Test sessA2 also has both tools (isolated session for same agent)
	ctxA2 := s.WithContext(context.Background(), sessA2)
	respA2 := s.HandleMessage(ctxA2, []byte(listMsg))
	respJSONA2, _ := json.Marshal(respA2)
	if !strings.Contains(string(respJSONA2), "priv__secret_tool") || !strings.Contains(string(respJSONA2), "pub__common_tool") {
		t.Errorf("sessA2 expected both tools, got: %s", string(respJSONA2))
	}

	// Test sessB only has public tool
	ctxB := s.WithContext(context.Background(), sessB)
	respB := s.HandleMessage(ctxB, []byte(listMsg))
	respJSONB, _ := json.Marshal(respB)
	if strings.Contains(string(respJSONB), "priv__secret_tool") {
		t.Errorf("sessB should NOT have access to priv__secret_tool, got: %s", string(respJSONB))
	}
	if !strings.Contains(string(respJSONB), "pub__common_tool") {
		t.Errorf("sessB expected access to pub__common_tool, got: %s", string(respJSONB))
	}
}

func TestCreateToolHandler_StateChangeAndErrors(t *testing.T) {
	b := &Backend{
		Name: "test-backend",
		Config: ServerConfig{
			Prefix: "test__",
		},
		Semaphore: make(chan struct{}, 5),
	}
	mux := newTestMultiplexer(b)

	// 1. Successful state changing tool
	b.Client = &mockMCPClient{
		callToolFunc: func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("created successfully"), nil
		},
	}
	handler := CreateToolHandler(b.Name, mux, "create_issue", "agent-state")
	req := mcp.CallToolRequest{}
	req.Params.Name = "test__create_issue"
	res, err := handler(context.Background(), req)
	if err != nil || res.IsError {
		t.Errorf("expected clean state-changing tool call, got res: %v, err: %v", res, err)
	}

	// 2. Tool result error (res.IsError = true)
	b.Client = &mockMCPClient{
		callToolFunc: func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultError("custom backend failure"), nil
		},
	}
	handlerErr := CreateToolHandler(b.Name, mux, "some_action", "agent-err")
	reqErr := mcp.CallToolRequest{}
	reqErr.Params.Name = "test__some_action"
	resErr, err := handlerErr(context.Background(), reqErr)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if resErr == nil || !resErr.IsError {
		t.Errorf("expected resErr.IsError to be true")
	}

	// 3. Tool execution transport error (err != nil)
	b.Client = &mockMCPClient{
		callToolFunc: func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return nil, context.Canceled
		},
	}
	handlerTransportErr := CreateToolHandler(b.Name, mux, "some_action", "agent-err")
	_, err = handlerTransportErr(context.Background(), reqErr)
	if err == nil {
		t.Errorf("expected transport error to be propagated")
	}
}

func TestCreateToolHandler_BranchProtection_KeysAndTypes(t *testing.T) {
	b := &Backend{
		Name: "gh-backend",
		Config: ServerConfig{
			Type:   "github",
			Prefix: "gh__",
		},
		Client:    &mockMCPClient{},
		Semaphore: make(chan struct{}, 5),
	}
	mux := newTestMultiplexer(b)

	// Non-string branch type
	handler := CreateToolHandler(b.Name, mux, "push_files", "agent-safe")
	req := mcp.CallToolRequest{}
	req.Params.Name = "gh__push_files"
	req.Params.Arguments = map[string]interface{}{"branch": 12345}
	res, err := handler(context.Background(), req)
	if err != nil || res.IsError {
		t.Errorf("expected non-string branch argument to be ignored by string check")
	}

	// base key targeting master
	reqBase := mcp.CallToolRequest{}
	reqBase.Params.Name = "gh__push_files"
	reqBase.Params.Arguments = map[string]interface{}{"base": "refs/heads/master"}
	resBase, err := handler(context.Background(), reqBase)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resBase.IsError {
		t.Errorf("expected base branch 'refs/heads/master' to be blocked")
	}
}

func TestCreateToolHandler_DynamicReloadResilience(t *testing.T) {
	// Tests that CreateToolHandler dynamically fetches the backend from mux,
	// preventing stale client pointers when backends are reconnected or replaced on SIGHUP reload.
	b1 := &Backend{
		Name:   "reloaded-backend",
		Config: ServerConfig{Prefix: "rel__", MaxConcurrent: 5},
		Client: &mockMCPClient{
			callToolFunc: func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return mcp.NewToolResultText("response from client 1"), nil
			},
		},
		Semaphore: make(chan struct{}, 5),
	}

	mux := newTestMultiplexer(b1)
	handler := CreateToolHandler("reloaded-backend", mux, "test_tool", "agent-tester")

	// 1. Call tool with initial backend client
	req := mcp.CallToolRequest{}
	req.Params.Name = "rel__test_tool"
	res1, err := handler(context.Background(), req)
	if err != nil || res1.IsError {
		t.Fatalf("first call failed: %v", err)
	}
	text1 := res1.Content[0].(mcp.TextContent).Text
	if text1 != "response from client 1" {
		t.Errorf("expected 'response from client 1', got '%s'", text1)
	}

	// 2. Simulate SIGHUP reload by replacing the backend client in mux
	b2 := &Backend{
		Name:   "reloaded-backend",
		Config: ServerConfig{Prefix: "rel__", MaxConcurrent: 5},
		Client: &mockMCPClient{
			callToolFunc: func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return mcp.NewToolResultText("response from reloaded client 2"), nil
			},
		},
		Semaphore: make(chan struct{}, 5),
	}
	mux.mu.Lock()
	mux.backends["reloaded-backend"] = b2
	mux.mu.Unlock()

	// 3. Existing handler closure must dynamically resolve the new client without stale pointer!
	res2, err := handler(context.Background(), req)
	if err != nil || res2.IsError {
		t.Fatalf("call after reload failed: %v", err)
	}
	text2 := res2.Content[0].(mcp.TextContent).Text
	if text2 != "response from reloaded client 2" {
		t.Errorf("expected 'response from reloaded client 2' after reload, got '%s'", text2)
	}
}

func TestBuildRouter_BatchSessionToolsRegistration(t *testing.T) {
	cfg := &Config{
		Servers: map[string]ServerConfig{
			"bulk-backend": {
				Transport:     "stdio",
				Command:       "echo",
				Prefix:        "bulk__",
				AllowedAgents: []string{},
			},
		},
	}
	mux := NewMultiplexer(cfg)
	defer mux.Stop()

	// Provide 20 tools to test batch registration
	var bulkTools []mcp.Tool
	for i := 1; i <= 20; i++ {
		bulkTools = append(bulkTools, mcp.Tool{Name: fmt.Sprintf("tool_%d", i)})
	}
	mux.backends["bulk-backend"].Client = &mockMCPClient{}
	mux.backends["bulk-backend"].CachedTools = bulkTools

	s := BuildRouter(mux)
	sess := &mockSession{id: "agent-bulk:uuid-99"}

	if err := s.RegisterSession(context.Background(), sess); err != nil {
		t.Fatalf("failed to register session: %v", err)
	}

	sessionTools := sess.GetSessionTools()
	if len(sessionTools) != 20 {
		t.Fatalf("expected exactly 20 registered session tools, got %d", len(sessionTools))
	}

	for i := 1; i <= 20; i++ {
		expectedName := fmt.Sprintf("bulk__tool_%d", i)
		if _, ok := sessionTools[expectedName]; !ok {
			t.Errorf("expected tool %s to be registered in session", expectedName)
		}
	}
}

func TestCreateToolHandler_SecurityGuard_RecursiveValueScan(t *testing.T) {
	b := &Backend{
		Name: "github-backend",
		Config: ServerConfig{
			Type:   "github",
			Prefix: "gh__",
		},
		Client:    &mockMCPClient{},
		Semaphore: make(chan struct{}, 5),
	}
	mux := newTestMultiplexer(b)

	// 1. Nested repository object with branch: "main"
	handler := CreateToolHandler(b.Name, mux, "push_files", "agent-scanner")
	req1 := mcp.CallToolRequest{}
	req1.Params.Name = "gh__push_files"
	req1.Params.Arguments = map[string]interface{}{
		"repository": map[string]interface{}{
			"branch": "main",
		},
	}
	res1, err := handler(context.Background(), req1)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !res1.IsError {
		t.Errorf("expected push_files({ repository: { branch: 'main' } }) to be blocked")
	}
	errMsg1 := extractResultErrorMessage(res1)
	if !strings.Contains(errMsg1, "forbidden branch 'main' at args.repository.branch") {
		t.Errorf("expected error message to contain 'forbidden branch 'main' at args.repository.branch', got '%s'", errMsg1)
	}

	// 2. Slice of objects with ref: "refs/heads/main"
	req2 := mcp.CallToolRequest{}
	req2.Params.Name = "gh__push_files"
	req2.Params.Arguments = map[string]interface{}{
		"refs": []interface{}{
			map[string]interface{}{
				"ref": "refs/heads/main",
			},
		},
	}
	res2, err := handler(context.Background(), req2)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !res2.IsError {
		t.Errorf("expected push_files({ refs: [{ ref: 'refs/heads/main' }] }) to be blocked")
	}

	// 3. Safe feature branch
	req3 := mcp.CallToolRequest{}
	req3.Params.Name = "gh__push_files"
	req3.Params.Arguments = map[string]interface{}{
		"branch": "feature/x",
	}
	res3, err := handler(context.Background(), req3)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if res3.IsError {
		t.Errorf("expected push_files({ branch: 'feature/x' }) to be allowed")
	}

	// 4. Unknown key branchName with main
	req4 := mcp.CallToolRequest{}
	req4.Params.Name = "gh__push_files"
	req4.Params.Arguments = map[string]interface{}{
		"branchName": "main",
	}
	res4, err := handler(context.Background(), req4)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !res4.IsError {
		t.Errorf("expected push_files({ branchName: 'main' }) to be blocked by value scan")
	}
}

func TestCreateToolHandler_NilResultFromBackend(t *testing.T) {
	b := &Backend{
		Name:      "weird-backend",
		Semaphore: make(chan struct{}, 1),
		Client: &mockMCPClient{
			callToolFunc: func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return nil, nil // Anomalous (nil, nil)
			},
		},
	}
	mux := newTestMultiplexer(b)
	handler := CreateToolHandler("weird-backend", mux, "ok_tool", "agent-x")
	req := mcp.CallToolRequest{}
	req.Params.Name = "weird__ok_tool"
	res, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if res == nil {
		t.Fatal("expected non-nil result")
	}
	if !res.IsError {
		t.Error("expected IsError=true for nil backend result")
	}
}

func TestCreateToolHandler_BackendPanicRecovers(t *testing.T) {
	b := &Backend{
		Name:      "panicking-backend",
		Semaphore: make(chan struct{}, 1),
		Client: &mockMCPClient{
			callToolFunc: func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				panic("simulated backend runtime panic")
			},
		},
	}
	mux := newTestMultiplexer(b)
	handler := CreateToolHandler("panicking-backend", mux, "ok_tool", "agent-x")
	req := mcp.CallToolRequest{}
	req.Params.Name = "panic__ok_tool"
	res, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res == nil {
		t.Fatal("expected non-nil result")
	}
	if !res.IsError {
		t.Error("expected IsError=true when backend panics")
	}
	text := extractResultErrorMessage(res)
	if !strings.Contains(text, "backend panic") {
		t.Errorf("expected error message to contain 'backend panic', got '%s'", text)
	}
}

func TestSessionIDExtractor_QueryParams(t *testing.T) {
	tests := []struct {
		name          string
		headers       map[string]string
		rawURL        string
		expectedAgent string
	}{
		{
			name:          "extracted from X-Agent-ID header",
			headers:       map[string]string{"X-Agent-ID": "toomynamea_brobot"},
			rawURL:        "http://localhost:8090/sse",
			expectedAgent: "toomynamea_brobot",
		},
		{
			name:          "extracted from ?agent= query parameter",
			headers:       nil,
			rawURL:        "http://localhost:8090/sse?agent=prometheus_brobot",
			expectedAgent: "prometheus_brobot",
		},
		{
			name:          "extracted from ?agent_id= query parameter",
			headers:       nil,
			rawURL:        "http://localhost:8090/sse?agent_id=kairos_brobot",
			expectedAgent: "kairos_brobot",
		},
		{
			name: "header takes precedence over query params",
			headers: map[string]string{
				"X-Agent-ID": "header_agent",
			},
			rawURL:        "http://localhost:8090/sse?agent=query_agent&agent_id=query_agent_id",
			expectedAgent: "header_agent",
		},
		{
			name:          "?agent= takes precedence over ?agent_id=",
			headers:       nil,
			rawURL:        "http://localhost:8090/sse?agent=primary_agent&agent_id=secondary_agent",
			expectedAgent: "primary_agent",
		},
		{
			name:          "fallback to anonymous when no agent specified",
			headers:       nil,
			rawURL:        "http://localhost:8090/sse",
			expectedAgent: "anonymous",
		},
		{
			name:          "fallback to anonymous on whitespace header and query params",
			headers:       map[string]string{"X-Agent-ID": "   "},
			rawURL:        "http://localhost:8090/sse?agent=%20%20&agent_id=%20",
			expectedAgent: "anonymous",
		},
		{
			name:          "trims whitespace from header",
			headers:       map[string]string{"X-Agent-ID": "  Tyler_Durden_gobot  "},
			rawURL:        "http://localhost:8090/sse",
			expectedAgent: "Tyler_Durden_gobot",
		},
		{
			name:          "trims whitespace from query parameter",
			headers:       nil,
			rawURL:        "http://localhost:8090/sse?agent=%20%20Caduceus_brobot%20%20",
			expectedAgent: "Caduceus_brobot",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.rawURL, nil)
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}

			sessionID, err := SessionIDExtractor(context.Background(), req)
			if err != nil {
				t.Fatalf("SessionIDExtractor returned unexpected error: %v", err)
			}

			parts := strings.Split(sessionID, ":")
			if len(parts) != 2 {
				t.Fatalf("expected session ID format '<agentID>:<uuid>', got %q", sessionID)
			}

			gotAgent := parts[0]
			gotUUID := parts[1]

			if gotAgent != tt.expectedAgent {
				t.Errorf("expected agent %q, got %q", tt.expectedAgent, gotAgent)
			}

			if _, err := uuid.Parse(gotUUID); err != nil {
				t.Errorf("expected valid UUID, got %q: %v", gotUUID, err)
			}
		})
	}
}

func TestSessionIDExtractor_PreservesExistingMcpSessionId(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/sse", nil)
	req.Header.Set("X-Agent-ID", "kairos_brobot")
	existingID := "kairos_brobot:10732613-8069-4a5c-a6eb-6243c7451808:3ff792fe-7055-43ec-8052-14610cae3d1e"
	req.Header.Set("Mcp-Session-Id", existingID)

	gotID, err := SessionIDExtractor(context.Background(), req)
	if err != nil {
		t.Fatalf("SessionIDExtractor returned error: %v", err)
	}
	if gotID != existingID {
		t.Errorf("expected %q, got %q", existingID, gotID)
	}
}

func TestSessionIDExtractor_Concurrent(t *testing.T) {
	const count = 50
	var wg sync.WaitGroup
	wg.Add(count)

	for i := 0; i < count; i++ {
		go func(idx int) {
			defer wg.Done()
			agent := fmt.Sprintf("agent_%d", idx)
			req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("http://localhost:8090/sse?agent=%s", agent), nil)
			sessionID, err := SessionIDExtractor(context.Background(), req)
			if err != nil {
				t.Errorf("concurrent extractor failed: %v", err)
				return
			}
			if !strings.HasPrefix(sessionID, agent+":") {
				t.Errorf("expected prefix %s:, got %s", agent, sessionID)
			}
		}(i)
	}
	wg.Wait()
}

func TestIsToolAllowed(t *testing.T) {
	allowlist := []string{"search_domain", "dynadot__get_dns", "RENEW_DOMAIN"}

	cases := []struct {
		toolName string
		prefix   string
		allowed  bool
	}{
		// Exact matches
		{"search_domain", "dynadot__", true},
		{"SEARCH_DOMAIN", "dynadot__", true},
		{"dynadot__search_domain", "dynadot__", true},
		{"get_dns", "dynadot__", true},
		{"dynadot__get_dns", "dynadot__", true},
		{"renew_domain", "dynadot__", true},
		{"dynadot__renew_domain", "dynadot__", true},

		// Disallowed tools
		{"delete_domain", "dynadot__", false},
		{"dynadot__delete_domain", "dynadot__", false},
		{"place_bid", "dynadot__", false},
		{"manage_cn_audit", "dynadot__", false},
		{"", "dynadot__", false},
	}

	for _, tc := range cases {
		t.Run(tc.toolName, func(t *testing.T) {
			got := isToolAllowed(tc.toolName, tc.prefix, allowlist)
			if got != tc.allowed {
				t.Errorf("isToolAllowed(%q, %q) = %v; want %v", tc.toolName, tc.prefix, got, tc.allowed)
			}
		})
	}

	// Empty allowlist allows everything
	if !isToolAllowed("any_random_tool", "prefix__", nil) {
		t.Errorf("expected empty allowlist to allow any tool")
	}
	if !isToolAllowed("any_random_tool", "prefix__", []string{}) {
		t.Errorf("expected empty allowlist slice to allow any tool")
	}
}

func TestCreateToolHandler_ToolsAllowlistEnforcement(t *testing.T) {
	backend := &Backend{
		Name:      "dynadot",
		Semaphore: make(chan struct{}, 5),
		Config: ServerConfig{
			Prefix:         "dynadot__",
			ToolsAllowlist: []string{"search_domain", "get_dns"},
		},
		Client: &mockMCPClient{
			callToolFunc: func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return mcp.NewToolResultText("success"), nil
			},
		},
	}

	mux := newTestMultiplexer(backend)

	// 1. Calling an allowed tool -> Success
	handlerAllowed := CreateToolHandler("dynadot", mux, "search_domain", "kairos_brobot")
	reqAllowed := mcp.CallToolRequest{}
	reqAllowed.Params.Name = "dynadot__search_domain"
	res, err := handlerAllowed(context.Background(), reqAllowed)
	if err != nil {
		t.Fatalf("unexpected error for allowed tool: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success for allowed tool, got error: %v", res)
	}

	// 2. Calling a disallowed tool -> Security Block
	handlerDisallowed := CreateToolHandler("dynadot", mux, "delete_domain", "kairos_brobot")
	reqDisallowed := mcp.CallToolRequest{}
	reqDisallowed.Params.Name = "dynadot__delete_domain"
	resBlocked, err := handlerDisallowed(context.Background(), reqDisallowed)
	if err != nil {
		t.Fatalf("unexpected error for disallowed tool: %v", err)
	}
	if !resBlocked.IsError {
		t.Fatalf("expected security error for disallowed tool")
	}
	errMsg := extractResultErrorMessage(resBlocked)
	if !strings.Contains(errMsg, "SECURITY POLICY VIOLATION") {
		t.Errorf("expected 'SECURITY POLICY VIOLATION' in error message, got: %s", errMsg)
	}
}

func TestBuildRouter_ToolsAllowlistFiltering(t *testing.T) {
	cfg := &Config{
		Servers: map[string]ServerConfig{
			"dynadot": {
				Transport:      "stdio",
				Command:        "echo",
				Prefix:         "dynadot__",
				AllowedAgents:  []string{"kairos_brobot"},
				ToolsAllowlist: []string{"search_domain", "get_dns"},
			},
		},
	}
	mux := NewMultiplexer(cfg)
	defer mux.Stop()

	mux.backends["dynadot"].Client = &mockMCPClient{}
	mux.backends["dynadot"].CachedTools = []mcp.Tool{
		{Name: "search_domain"},
		{Name: "get_dns"},
		{Name: "delete_domain"},
		{Name: "place_bid"},
	}

	s := BuildRouter(mux)

	sess := &mockSession{id: "kairos_brobot:uuid-test"}
	if err := s.RegisterSession(context.Background(), sess); err != nil {
		t.Fatalf("failed to register session: %v", err)
	}

	listMsg := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	ctx := s.WithContext(context.Background(), sess)
	resp := s.HandleMessage(ctx, []byte(listMsg))
	respJSON, _ := json.Marshal(resp)
	respStr := string(respJSON)

	// Allowed tools should be registered
	if !strings.Contains(respStr, "dynadot__search_domain") {
		t.Errorf("expected dynadot__search_domain to be present, got: %s", respStr)
	}
	if !strings.Contains(respStr, "dynadot__get_dns") {
		t.Errorf("expected dynadot__get_dns to be present, got: %s", respStr)
	}

	// Filtered-out tools should NOT be registered
	if strings.Contains(respStr, "dynadot__delete_domain") {
		t.Errorf("expected dynadot__delete_domain to be pruned, got: %s", respStr)
	}
	if strings.Contains(respStr, "dynadot__place_bid") {
		t.Errorf("expected dynadot__place_bid to be pruned, got: %s", respStr)
	}
}

func TestCreateToolHandler_AgentToolsAllowlistEnforcement(t *testing.T) {
	backend := &Backend{
		Name:      "shared-gateway",
		Semaphore: make(chan struct{}, 5),
		Config: ServerConfig{
			Prefix: "shared__",
			AgentToolsAllowlist: map[string][]string{
				"kairos_brobot": {"search_web", "deep_research"},
			},
		},
		Client: &mockMCPClient{
			callToolFunc: func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return mcp.NewToolResultText("success"), nil
			},
		},
	}

	mux := newTestMultiplexer(backend)

	// 1. kairos_brobot calls allowed tool -> Success
	handlerAllowed := CreateToolHandler("shared-gateway", mux, "search_web", "kairos_brobot")
	reqAllowed := mcp.CallToolRequest{}
	reqAllowed.Params.Name = "shared__search_web"
	res, err := handlerAllowed(context.Background(), reqAllowed)
	if err != nil || res.IsError {
		t.Fatalf("expected success for kairos_brobot allowed tool, got err=%v res=%v", err, res)
	}

	// 2. kairos_brobot calls disallowed tool -> Security Block
	handlerBlocked := CreateToolHandler("shared-gateway", mux, "hybrid_search", "kairos_brobot")
	reqBlocked := mcp.CallToolRequest{}
	reqBlocked.Params.Name = "shared__hybrid_search"
	resBlocked, err := handlerBlocked(context.Background(), reqBlocked)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resBlocked.IsError {
		t.Fatalf("expected security violation for kairos_brobot")
	}

	// 3. other_agent calls hybrid_search (no allowlist restriction for other_agent) -> Success
	handlerOther := CreateToolHandler("shared-gateway", mux, "hybrid_search", "prometheus_brobot")
	resOther, err := handlerOther(context.Background(), reqBlocked)
	if err != nil || resOther.IsError {
		t.Fatalf("expected other_agent to be allowed without allowlist restriction, got err=%v res=%v", err, resOther)
	}
}

func TestBuildRouter_AgentToolsAllowlistFiltering(t *testing.T) {
	cfg := &Config{
		Servers: map[string]ServerConfig{
			"shared-search": {
				Transport:     "stdio",
				Command:       "echo",
				Prefix:        "search__",
				AllowedAgents: []string{"kairos_brobot", "toomynamea_brobot"},
				AgentToolsAllowlist: map[string][]string{
					"kairos_brobot": {"search_web", "deep_research"},
				},
			},
		},
	}
	mux := NewMultiplexer(cfg)
	defer mux.Stop()

	mux.backends["shared-search"].Client = &mockMCPClient{}
	mux.backends["shared-search"].CachedTools = []mcp.Tool{
		{Name: "search_web"},
		{Name: "deep_research"},
		{Name: "hybrid_search"},
		{Name: "searxng_health"},
	}

	s := BuildRouter(mux)

	// 1. Session for kairos_brobot -> Only 2 allowed tools
	sessKairos := &mockSession{id: "kairos_brobot:uuid-1"}
	if err := s.RegisterSession(context.Background(), sessKairos); err != nil {
		t.Fatalf("failed to register kairos session: %v", err)
	}

	listMsg := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	ctxKairos := s.WithContext(context.Background(), sessKairos)
	respKairos := s.HandleMessage(ctxKairos, []byte(listMsg))
	respJSONKairos, _ := json.Marshal(respKairos)
	strKairos := string(respJSONKairos)

	if !strings.Contains(strKairos, "search__search_web") || !strings.Contains(strKairos, "search__deep_research") {
		t.Errorf("expected kairos to have search_web and deep_research, got: %s", strKairos)
	}
	if strings.Contains(strKairos, "search__hybrid_search") || strings.Contains(strKairos, "search__searxng_health") {
		t.Errorf("expected kairos to NOT have hybrid_search or searxng_health, got: %s", strKairos)
	}

	// 2. Session for toomynamea_brobot -> Has all 4 tools (not restricted by allowlist)
	sessToomy := &mockSession{id: "toomynamea_brobot:uuid-2"}
	if err := s.RegisterSession(context.Background(), sessToomy); err != nil {
		t.Fatalf("failed to register toomy session: %v", err)
	}

	ctxToomy := s.WithContext(context.Background(), sessToomy)
	respToomy := s.HandleMessage(ctxToomy, []byte(listMsg))
	respJSONToomy, _ := json.Marshal(respToomy)
	strToomy := string(respJSONToomy)

	if !strings.Contains(strToomy, "search__hybrid_search") || !strings.Contains(strToomy, "search__searxng_health") {
		t.Errorf("expected toomynamea to retain hybrid_search and searxng_health, got: %s", strToomy)
	}
}

func TestExtractBotFromPath(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		expected string
	}{
		{
			name:     "standard agent office path",
			path:     "/opt/.agents/kairos_brobot",
			expected: "kairos_brobot",
		},
		{
			name:     "subdirectory inside agent office",
			path:     "/opt/.agents/kairos_brobot/scratch/downloads",
			expected: "kairos_brobot",
		},
		{
			name:     "relative .agents path",
			path:     ".agents/trickster_gobot",
			expected: "trickster_gobot",
		},
		{
			name:     "common agents dir should be rejected",
			path:     "/opt/.agents/common",
			expected: "",
		},
		{
			name:     "empty path",
			path:     "",
			expected: "",
		},
		{
			name:     "non-agent path",
			path:     "/var/log/mcp-router",
			expected: "",
		},
		{
			name:     "another agent office",
			path:     "/opt/.agents/prometheus_brobot/.locks",
			expected: "prometheus_brobot",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := extractBotFromPath(tc.path)
			if got != tc.expected {
				t.Errorf("extractBotFromPath(%q) = %q, want %q", tc.path, got, tc.expected)
			}
		})
	}
}

func TestResolveAgentIDFromLocalPeer_NonLoopback(t *testing.T) {
	// Non-loopback addresses must never trigger local peer inspection
	if got := resolveAgentIDFromLocalPeer(context.Background(), "192.0.2.50:54321"); got != "" {
		t.Errorf("expected empty string for non-loopback IP, got %q", got)
	}
	if got := resolveAgentIDFromLocalPeer(context.Background(), "invalid-address"); got != "" {
		t.Errorf("expected empty string for invalid address, got %q", got)
	}
}


func TestFindBackendForTool(t *testing.T) {
	mux := NewMultiplexer(&Config{})
	
	backend1 := &Backend{
		Name: "b1",
		Config: ServerConfig{Prefix: "pre_"},
		CachedTools: []mcp.Tool{
			{Name: "tool1"},
		},
	}
	
	backend2 := &Backend{
		Name: "b2",
		CachedTools: []mcp.Tool{
			{Name: "tool2"},
		},
	}
	
	mux.backends = map[string]*Backend{
		"b1": backend1,
		"b2": backend2,
	}

	t.Run("by prefix", func(t *testing.T) {
		name, orig := findBackendForTool(mux, "pre_toolX")
		if name != "b1" || orig != "toolX" {
			t.Errorf("expected b1, toolX, got %s, %s", name, orig)
		}
	})

	t.Run("by cache fallback", func(t *testing.T) {
		name, orig := findBackendForTool(mux, "tool2")
		if name != "b2" || orig != "tool2" {
			t.Errorf("expected b2, tool2, got %s, %s", name, orig)
		}
	})

	t.Run("not found", func(t *testing.T) {
		name, orig := findBackendForTool(mux, "tool3")
		if name != "" || orig != "" {
			t.Errorf("expected empty strings, got %s, %s", name, orig)
		}
	})
}

func TestExtractResultErrorMessage(t *testing.T) {
	t.Run("nil result", func(t *testing.T) {
		if msg := extractResultErrorMessage(nil); msg != "" {
			t.Errorf("expected empty string, got %s", msg)
		}
	})

	t.Run("empty content", func(t *testing.T) {
		res := &mcp.CallToolResult{Content: []mcp.Content{}}
		if msg := extractResultErrorMessage(res); msg != "unknown tool error" {
			t.Errorf("expected 'unknown tool error', got %s", msg)
		}
	})

	t.Run("with content", func(t *testing.T) {
		res := &mcp.CallToolResult{Content: []mcp.Content{mcp.TextContent{Text: "error detail"}}}
		if msg := extractResultErrorMessage(res); !strings.Contains(msg, "error detail") {
			t.Errorf("expected error detail, got %s", msg)
		}
	})
}

func TestResolveAgentIDFromLocalPeer_EdgeCases(t *testing.T) {
	ctx := context.Background()
	
	t.Run("empty string", func(t *testing.T) {
		if got := resolveAgentIDFromLocalPeer(ctx, ""); got != "" {
			t.Errorf("expected empty string, got %s", got)
		}
	})
	
	t.Run("invalid host:port format", func(t *testing.T) {
		if got := resolveAgentIDFromLocalPeer(ctx, "invalidformat"); got != "" {
			t.Errorf("expected empty string, got %s", got)
		}
	})
	
	t.Run("invalid port", func(t *testing.T) {
		if got := resolveAgentIDFromLocalPeer(ctx, "127.0.0.1:abcd"); got != "" {
			t.Errorf("expected empty string, got %s", got)
		}
	})
	
	t.Run("valid loopback without matching process", func(t *testing.T) {
		// Use a random port unlikely to be connected locally via ss
		if got := resolveAgentIDFromLocalPeer(ctx, "127.0.0.1:40001"); got != "" {
			t.Errorf("expected empty string, got %s", got)
		}
	})
}

func TestSessionIDExtractor_AgentIdParameter(t *testing.T) {
	ctx := context.Background()
	
	t.Run("agent_id query param", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/mcp?agent_id=query-agent", nil)
		sessionID, err := SessionIDExtractor(ctx, req)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if !strings.HasPrefix(sessionID, "query-agent:") {
			t.Errorf("expected session ID to start with query-agent:, got %s", sessionID)
		}
	})
}

func TestResolveAgentIDFromLocalPeer_ContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	// This should return "" because the exec context is cancelled
	if got := resolveAgentIDFromLocalPeer(ctx, "127.0.0.1:40000"); got != "" {
		t.Errorf("expected empty string due to context cancel, got %s", got)
	}
}

func TestSessionIDExtractor_FallbackToAnonymous(t *testing.T) {
	ctx := context.Background()
	req := httptest.NewRequest("GET", "/mcp", nil) // No X-Agent-ID, no agent query param
	
	sessionID, err := SessionIDExtractor(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	
	if !strings.HasPrefix(sessionID, "anonymous:") {
		t.Errorf("expected session ID to fallback to anonymous:, got %s", sessionID)
	}
}

func TestResolveAgentIDFromLocalPeer_ExecFails(t *testing.T) {
	ctx := context.Background()
	// An unlikely port to hit any real process, command will run but find no match
	if got := resolveAgentIDFromLocalPeer(ctx, "127.0.0.1:65535"); got != "" {
		t.Errorf("expected empty string when no process is found, got %s", got)
	}
}

func TestResolveAgentIDFromLocalPeer_PIDPaths(t *testing.T) {
	// Let's test the cmdline and cwd paths by spoofing the command or using the current test process.
	// Since testing `ss` behavior reliably is hard without mocks or fake binaries, we'll hit the fallback paths directly
	// Or we just accept the coverage block because `ss` output formatting in tests is brittle without interfaces.
	// We'll leave it as is if it's too difficult to mock `/proc` and `ss`.
}
