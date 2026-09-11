package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuditLogger_InitAndLogEvents(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "audit.jsonl")

	al, err := InitAuditLogger(logPath)
	if err != nil {
		t.Fatalf("InitAuditLogger failed: %v", err)
	}
	if al == nil {
		t.Fatalf("expected non-nil AuditLogger")
	}

	// Test logging StateChange
	LogAuditEvent(AuditEvent{
		EventType: EventStateChange,
		AgentID:   "agent-1",
		Backend:   "github",
		Tool:      "update_issue",
	})

	// Test logging SecurityBlock
	LogAuditEvent(AuditEvent{
		EventType: EventSecurityBlock,
		AgentID:   "agent-2",
		Backend:   "github",
		Tool:      "merge_pull_request",
		Reason:    "Branch protection violation",
	})

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("failed to read log file: %v", err)
	}

	if len(data) == 0 {
		t.Errorf("expected non-empty audit log content")
	}
}

func TestInitAuditLogger_InvalidPath(t *testing.T) {
	// Trying to create directory where a file already exists
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "file_blocking")
	os.WriteFile(filePath, []byte("data"), 0644)

	_, err := InitAuditLogger(filepath.Join(filePath, "audit.log"))
	if err == nil {
		t.Errorf("expected error when directory creation fails")
	}
}

func TestLogAuditEvent_NilLogger(t *testing.T) {
	oldLogger := globalAuditLogger
	globalAuditLogger = nil
	defer func() { globalAuditLogger = oldLogger }()

	// Should not panic or crash
	LogAuditEvent(AuditEvent{
		EventType: EventStateChange,
		AgentID:   "agent-nil",
		Tool:      "some_tool",
	})
}

func TestIsStateChangingTool_Comprehensive(t *testing.T) {
	tests := []struct {
		toolName string
		expected bool
	}{
		{"create_issue", true},
		{"delete_repo", true},
		{"update_user", true},
		{"push_code", true},
		{"post_comment", true},
		{"put_file", true},
		{"patch_record", true},
		{"write_file", true},
		{"upload_asset", true},
		{"remove_tag", true},
		{"merge_pull_request", true},
		{"send_email", true},
		{"trigger_build", true},
		{"get_issue", false},
		{"list_repos", false},
		{"read_file", false},
		{"search_code", false},
	}

	for _, tt := range tests {
		result := IsStateChangingTool(tt.toolName)
		if result != tt.expected {
			t.Errorf("IsStateChangingTool(%s) = %v, expected %v", tt.toolName, result, tt.expected)
		}
	}
}

func TestSanitizeArguments(t *testing.T) {
	t.Run("nil arguments", func(t *testing.T) {
		if res := SanitizeArguments(nil); res != nil {
			t.Errorf("expected nil for nil input, got %v", res)
		}
	})

	t.Run("masks sensitive keys and preserves safe keys", func(t *testing.T) {
		input := map[string]interface{}{
			"username":      "admin",
			"password":      "plaintext123",
			"db_passwd":     "supersecret",
			"access_token":  "ghp_xxxxxx",
			"api_key":       "key-12345",
			"apikey":        "key-67890",
			"client_secret": "secret-xyz",
			"auth_header":   "Bearer secret",
			"credentials":   "sensitive-creds",
			"private_key":   "-----BEGIN RSA PRIVATE KEY-----",
			"query":         "how to code",
			"count":         42,
			"nested": map[string]interface{}{
				"safe_field":     "safe_value",
				"nested_token":   "secret_nested_token",
				"admin_password": "nested_password",
			},
		}

		sanitized := SanitizeArguments(input)

		// Check safe fields
		if sanitized["username"] != "admin" {
			t.Errorf("expected safe field 'username' to be 'admin', got %v", sanitized["username"])
		}
		if sanitized["query"] != "how to code" {
			t.Errorf("expected safe field 'query' to be 'how to code', got %v", sanitized["query"])
		}
		if sanitized["count"] != 42 {
			t.Errorf("expected safe field 'count' to be 42, got %v", sanitized["count"])
		}

		// Check redacted fields
		sensitiveKeys := []string{
			"password", "db_passwd", "access_token", "api_key",
			"apikey", "client_secret", "auth_header", "credentials", "private_key",
		}
		for _, k := range sensitiveKeys {
			if sanitized[k] != "[REDACTED]" {
				t.Errorf("expected key '%s' to be '[REDACTED]', got %v", k, sanitized[k])
			}
		}

		// Check nested fields
		nested, ok := sanitized["nested"].(map[string]interface{})
		if !ok {
			t.Fatalf("expected nested map to be map[string]interface{}")
		}
		if nested["safe_field"] != "safe_value" {
			t.Errorf("expected nested.safe_field to be 'safe_value', got %v", nested["safe_field"])
		}
		if nested["nested_token"] != "[REDACTED]" {
			t.Errorf("expected nested.nested_token to be '[REDACTED]', got %v", nested["nested_token"])
		}
		if nested["admin_password"] != "[REDACTED]" {
			t.Errorf("expected nested.admin_password to be '[REDACTED]', got %v", nested["admin_password"])
		}
	})
}

func TestAuditLogger_Close(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "audit.jsonl")

	al, err := InitAuditLogger(logPath)
	if err != nil {
		t.Fatalf("InitAuditLogger failed: %v", err)
	}

	if err := al.Close(); err != nil {
		t.Errorf("expected clean close, got: %v", err)
	}

	// Idempotent close
	if err := al.Close(); err != nil {
		t.Errorf("expected second close to be nil, got: %v", err)
	}

	// Nil receiver close
	var nilLogger *AuditLogger
	if err := nilLogger.Close(); err != nil {
		t.Errorf("expected nil logger close to return nil, got: %v", err)
	}
}

func TestAuditLogger_Reopen(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "audit.jsonl")

	al, err := InitAuditLogger(logPath)
	if err != nil {
		t.Fatalf("InitAuditLogger failed: %v", err)
	}
	defer al.Close()

	// 1. Log an event to original file
	LogAuditEvent(AuditEvent{
		EventType: EventStateChange,
		AgentID:   "agent-1",
		Tool:      "create_issue",
	})

	// 2. Simulate logrotate: rename the original file
	rotatedPath := filepath.Join(tmpDir, "audit.jsonl.1")
	if err := os.Rename(logPath, rotatedPath); err != nil {
		t.Fatalf("failed to rename log file: %v", err)
	}

	// 3. Call Reopen() to re-create/reopen logPath
	if err := al.Reopen(); err != nil {
		t.Fatalf("Reopen failed: %v", err)
	}

	// 4. Log another event after reopen
	LogAuditEvent(AuditEvent{
		EventType: EventStateChange,
		AgentID:   "agent-2",
		Tool:      "update_issue",
	})

	// 5. Verify new file was created and contains agent-2 event
	newData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("failed to read reopened log file: %v", err)
	}
	if !strings.Contains(string(newData), "agent-2") {
		t.Errorf("expected reopened log file to contain 'agent-2', got: %s", string(newData))
	}
	if strings.Contains(string(newData), "agent-1") {
		t.Errorf("expected reopened log file NOT to contain 'agent-1'")
	}

	// 6. Verify rotated file contains agent-1 event
	oldData, err := os.ReadFile(rotatedPath)
	if err != nil {
		t.Fatalf("failed to read rotated log file: %v", err)
	}
	if !strings.Contains(string(oldData), "agent-1") {
		t.Errorf("expected rotated log file to contain 'agent-1'")
	}

	// 7. Nil receiver test
	var nilLogger *AuditLogger
	if err := nilLogger.Reopen(); err != nil {
		t.Errorf("expected nil logger Reopen() to return nil, got: %v", err)
	}
}

func TestLogAuditEvent_EdgeCases(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "audit_edges.jsonl")

	al, err := InitAuditLogger(logPath)
	if err != nil {
		t.Fatalf("InitAuditLogger failed: %v", err)
	}
	defer al.Close()

	// 1. Pre-set timestamp & EventToolError
	LogAuditEvent(AuditEvent{
		Timestamp: "2026-09-04T12:00:00Z",
		EventType: EventToolError,
		AgentID:   "agent-tester",
		Backend:   "test-backend",
		Tool:      "broken_tool",
		Error:     "mock failure",
	})

	// 2. Unmarshalable argument value (e.g. channel) triggering json.Marshal error branch
	ch := make(chan int)
	LogAuditEvent(AuditEvent{
		EventType: EventStateChange,
		AgentID:   "agent-tester",
		Backend:   "test-backend",
		Tool:      "create_item",
		Arguments: map[string]interface{}{"unmarshalable": ch},
	})

	// 3. Global logger with nil file
	oldLogger := globalAuditLogger
	globalAuditLogger = &AuditLogger{logFile: nil}
	LogAuditEvent(AuditEvent{
		EventType: EventSecurityBlock,
		AgentID:   "agent-test",
		Tool:      "some_tool",
	})
	globalAuditLogger = oldLogger

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("failed to read log: %v", err)
	}
	if len(data) == 0 {
		t.Errorf("expected audit events to be written")
	}
}

func TestInitAuditLoggerWithFallback(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Primary works
	primPath := filepath.Join(tmpDir, "primary.jsonl")
	al1, err := InitAuditLoggerWithFallback(primPath, filepath.Join(tmpDir, "fallback.jsonl"))
	if err != nil {
		t.Fatalf("expected success on primary path: %v", err)
	}
	if al1.IsFallback() {
		t.Errorf("expected primary not to be in fallback mode")
	}
	if al1.Status() != "UP" {
		t.Errorf("expected status 'UP', got '%s'", al1.Status())
	}
	_ = al1.Close()

	// 2. Primary fails (directory blocked by file), fallback works
	blockedDir := filepath.Join(tmpDir, "blocked_dir")
	_ = os.WriteFile(blockedDir, []byte("blocker"), 0644)
	invalidPrimary := filepath.Join(blockedDir, "cannot_create.jsonl")
	validFallback := filepath.Join(tmpDir, "fallback.jsonl")

	al2, err := InitAuditLoggerWithFallback(invalidPrimary, validFallback)
	if err != nil {
		t.Fatalf("expected fallback to succeed: %v", err)
	}
	if !al2.IsFallback() {
		t.Errorf("expected fallback logger to have isFallback=true")
	}
	if al2.Status() != "FALLBACK" {
		t.Errorf("expected status 'FALLBACK', got '%s'", al2.Status())
	}
	_ = al2.Close()

	// 3. Both fail
	invalidFallback := filepath.Join(blockedDir, "cannot_create_fallback.jsonl")
	al3, err := InitAuditLoggerWithFallback(invalidPrimary, invalidFallback)
	if err == nil {
		t.Errorf("expected error when both primary and fallback fail")
	}
	if al3 != nil {
		t.Errorf("expected nil logger when both fail")
	}

	// 4. Fail-safe logging to stderr when globalAuditLogger is nil
	oldLogger := globalAuditLogger
	globalAuditLogger = nil
	// Must not panic, must fail-safe output to stderr
	LogAuditEvent(AuditEvent{
		EventType: EventSecurityBlock,
		AgentID:   "unauthorized-attacker",
		Tool:      "forbidden_action",
		Reason:    "Test fail-safe stderr output",
	})
	globalAuditLogger = oldLogger
}

func TestAuditLogger_Status(t *testing.T) {
	var nilLogger *AuditLogger
	if nilLogger.Status() != "DOWN" {
		t.Errorf("expected DOWN for nil logger, got %s", nilLogger.Status())
	}

	deadLogger := &AuditLogger{logFile: nil}
	if deadLogger.Status() != "DOWN" {
		t.Errorf("expected DOWN for dead logger, got %s", deadLogger.Status())
	}

	old := globalAuditLogger
	globalAuditLogger = nil
	if GetAuditLoggerStatus() != "DOWN" {
		t.Errorf("expected GetAuditLoggerStatus() to return DOWN when globalAuditLogger is nil")
	}
	globalAuditLogger = old
}

func TestAuditLogger_Reopen_NilAndFail(t *testing.T) {
	var nilLogger *AuditLogger
	if err := nilLogger.Reopen(); err != nil {
		t.Errorf("expected nil error on nil logger reopen")
	}

	tmpDir := t.TempDir()
	blockingDir := filepath.Join(tmpDir, "blocker_dir")
	_ = os.WriteFile(blockingDir, []byte("data"), 0644)

	al := &AuditLogger{logPath: filepath.Join(blockingDir, "cannot_open.jsonl")}
	if err := al.Reopen(); err == nil {
		t.Errorf("expected error when reopening unwritable path")
	}
}

func TestIsSensitiveKey_Coverage(t *testing.T) {
	positiveCases := []string{
		"bearer", "bearer_token", "bearerToken",
		"session_id", "sessionid", "cookie", "set_cookie",
		"jwt", "id_token", "refresh_token", "access_token",
		"pat", "github_pat", "fine_grained_token", "client_key",
		"webhook_secret", "x_hub_signature", "stripe_signature",
		"authorization", "Authorization",
	}

	for _, k := range positiveCases {
		if !isSensitiveKey(k) {
			t.Errorf("expected isSensitiveKey('%s') = true, got false", k)
		}
	}

	negativeCases := []string{
		"username", "email", "owner", "repo",
		"branch", "path", "query", "count",
		"page", "limit", "since", "until",
		"message", "body", "title",
	}

	for _, k := range negativeCases {
		if isSensitiveKey(k) {
			t.Errorf("expected isSensitiveKey('%s') = false, got true", k)
		}
	}
}

func TestSanitizeArguments_ValueShapeDetection(t *testing.T) {
	args := map[string]interface{}{
		"header":            "Authorization",
		"value":             "ghp_abc123def456ghi789jkl012mno345pqr678",
		"random_field":      "this is just a regular string",
		"raw_fine_grained":  "github_pat_11ABCDEFG_1234567890abcdefghijklmnopqrstuvwxyz1234567890",
		"aws_id":            "AKIAIOSFODNN7EXAMPLE",
		"encoded_payload":   "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIn0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c",
		"http_header_value": "Bearer sensitive_token_1234567890",
		"nested_slice": []interface{}{
			"normal string",
			"ghp_second123456789012345678901234567890",
			map[string]interface{}{
				"deep_key": "ghp_deep123456789012345678901234567890",
			},
		},
	}

	sanitized := SanitizeArguments(args)

	if sanitized["value"] != "[REDACTED-VALUE]" {
		t.Errorf("expected value to be [REDACTED-VALUE], got %v", sanitized["value"])
	}
	if sanitized["random_field"] != "this is just a regular string" {
		t.Errorf("expected random_field to be preserved, got %v", sanitized["random_field"])
	}
	if sanitized["raw_fine_grained"] != "[REDACTED-VALUE]" {
		t.Errorf("expected raw_fine_grained to be [REDACTED-VALUE], got %v", sanitized["raw_fine_grained"])
	}
	if sanitized["aws_id"] != "[REDACTED-VALUE]" {
		t.Errorf("expected aws_id to be [REDACTED-VALUE], got %v", sanitized["aws_id"])
	}
	if sanitized["encoded_payload"] != "[REDACTED-VALUE]" {
		t.Errorf("expected encoded_payload to be [REDACTED-VALUE], got %v", sanitized["encoded_payload"])
	}
	if sanitized["http_header_value"] != "[REDACTED-VALUE]" {
		t.Errorf("expected http_header_value to be [REDACTED-VALUE], got %v", sanitized["http_header_value"])
	}

	slice, ok := sanitized["nested_slice"].([]interface{})
	if !ok || len(slice) != 3 {
		t.Fatalf("expected slice of length 3, got %v", sanitized["nested_slice"])
	}
	if slice[0] != "normal string" {
		t.Errorf("expected slice[0] to be 'normal string', got %v", slice[0])
	}
	if slice[1] != "[REDACTED-VALUE]" {
		t.Errorf("expected slice[1] to be [REDACTED-VALUE], got %v", slice[1])
	}
	deepMap, ok := slice[2].(map[string]interface{})
	if !ok || deepMap["deep_key"] != "[REDACTED-VALUE]" {
		t.Errorf("expected deep_key in slice to be [REDACTED-VALUE], got %v", slice[2])
	}
}

func TestIsStateChangingTool_ContextualExclusions(t *testing.T) {
	cases := []struct {
		tool     string
		expected bool
	}{
		{"creator_info", false},
		{"post_metadata", false},
		{"post_info", false},
		{"post_status", false},
		{"trigger_status", false},
		{"trigger_history", false},
		{"trigger_search", false},
		{"post_message", true},
		{"trigger_job", true},
		{"gh__add_comment", true},
		{"gh__close_issue", true},
		{"gh__create_pull_request", true},
		{"gh__delete_branch", true},
	}

	for _, tc := range cases {
		actual := IsStateChangingTool(tc.tool)
		if actual != tc.expected {
			t.Errorf("IsStateChangingTool('%s') = %v, expected %v", tc.tool, actual, tc.expected)
		}
	}
}

