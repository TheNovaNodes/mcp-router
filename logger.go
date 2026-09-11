package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

type EventType string

const (
	EventSecurityBlock EventType = "SECURITY_BLOCK"
	EventStateChange   EventType = "STATE_CHANGE"
	EventToolError     EventType = "TOOL_ERROR"
)

type AuditEvent struct {
	Timestamp string                 `json:"timestamp"`
	EventType EventType              `json:"event_type"`
	AgentID   string                 `json:"agent_id"`
	Backend   string                 `json:"backend"`
	Tool      string                 `json:"tool"`
	Reason    string                 `json:"reason,omitempty"`
	Arguments map[string]interface{} `json:"arguments,omitempty"`
	Duration  string                 `json:"duration,omitempty"`
	Error     string                 `json:"error,omitempty"`
}

type AuditLogger struct {
	mu         sync.Mutex
	logPath    string
	logFile    *os.File
	isFallback bool
}

var globalAuditLogger *AuditLogger

func InitAuditLogger(logPath string) (*AuditLogger, error) {
	dir := filepath.Dir(logPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create log dir %s: %w", dir, err)
	}

	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open audit log file %s: %w", logPath, err)
	}

	al := &AuditLogger{logPath: logPath, logFile: f}
	globalAuditLogger = al
	log.Printf("[AUDIT] Zero-Noise Audit Logger initialized at %s", logPath)
	return al, nil
}

// InitAuditLoggerWithFallback attempts to open primaryPath, falling back to fallbackPath if primary fails.
// If both paths fail, returns an error. LogAuditEvent guarantees failsafe output to stderr so events are never lost.
func InitAuditLoggerWithFallback(primaryPath, fallbackPath string) (*AuditLogger, error) {
	al, err := InitAuditLogger(primaryPath)
	if err == nil {
		return al, nil
	}

	log.Printf("[AUDIT WARNING] Primary audit log path '%s' failed: %v. Attempting fallback '%s'", primaryPath, err, fallbackPath)
	cleanFallback := strings.TrimSpace(fallbackPath)
	if cleanFallback != "" && cleanFallback != primaryPath {
		alFall, fallErr := InitAuditLogger(cleanFallback)
		if fallErr == nil {
			alFall.isFallback = true
			log.Printf("[AUDIT WARNING] Audit Logger initialized in FALLBACK mode at %s", cleanFallback)
			return alFall, nil
		}
		log.Printf("[AUDIT WARNING] Fallback audit log path '%s' also failed: %v", cleanFallback, fallErr)
	}

	return nil, fmt.Errorf("failed to initialize primary audit log '%s': %w", primaryPath, err)
}

func (al *AuditLogger) Status() string {
	if al == nil || al.logFile == nil {
		return "DOWN"
	}
	if al.isFallback {
		return "FALLBACK"
	}
	return "UP"
}

func (al *AuditLogger) IsFallback() bool {
	return al != nil && al.isFallback
}

func GetAuditLoggerStatus() string {
	if globalAuditLogger == nil {
		return "DOWN"
	}
	return globalAuditLogger.Status()
}

// Reopen flushes and reopens the log file to support external log rotation (e.g. logrotate)
func (al *AuditLogger) Reopen() error {
	if al == nil {
		return nil
	}
	al.mu.Lock()
	defer al.mu.Unlock()

	if al.logFile != nil {
		_ = al.logFile.Close()
	}

	f, err := os.OpenFile(al.logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to reopen audit log file %s: %w", al.logPath, err)
	}
	al.logFile = f
	return nil
}

// Close flushes and closes the audit log file
func (al *AuditLogger) Close() error {
	if al == nil {
		return nil
	}
	al.mu.Lock()
	defer al.mu.Unlock()
	if al.logFile != nil {
		err := al.logFile.Close()
		al.logFile = nil
		return err
	}
	return nil
}

var (
	reGitHubPAT     = regexp.MustCompile(`^ghp_[A-Za-z0-9]{30,}`)
	reFineGrainedPAT = regexp.MustCompile(`^github_pat_[A-Za-z0-9_]{40,}`)
	reJWT           = regexp.MustCompile(`^eyJ[A-Za-z0-9_-]+\.eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`)
	reAWSAccessKey  = regexp.MustCompile(`^AKIA[0-9A-Z]{16}$`)
)

func isSensitiveValue(v string) bool {
	clean := strings.TrimSpace(v)
	if reGitHubPAT.MatchString(clean) ||
		reFineGrainedPAT.MatchString(clean) ||
		reJWT.MatchString(clean) ||
		reAWSAccessKey.MatchString(clean) {
		return true
	}
	if strings.HasPrefix(strings.ToLower(clean), "bearer ") && len(clean) > 15 {
		return true
	}
	return false
}

// SanitizeArguments returns a copy of arguments with sensitive keys and values redacted
func SanitizeArguments(args map[string]interface{}) map[string]interface{} {
	if args == nil {
		return nil
	}
	clean := make(map[string]interface{}, len(args))
	for k, v := range args {
		lowerK := strings.ToLower(k)
		if isSensitiveKey(lowerK) {
			clean[k] = "[REDACTED]"
		} else if strVal, ok := v.(string); ok && isSensitiveValue(strVal) {
			clean[k] = "[REDACTED-VALUE]"
		} else if nestedMap, ok := v.(map[string]interface{}); ok {
			clean[k] = SanitizeArguments(nestedMap)
		} else if sliceVal, ok := v.([]interface{}); ok {
			clean[k] = sanitizeSlice(sliceVal)
		} else {
			clean[k] = v
		}
	}
	return clean
}

func sanitizeSlice(s []interface{}) []interface{} {
	clean := make([]interface{}, len(s))
	for i, item := range s {
		if m, ok := item.(map[string]interface{}); ok {
			clean[i] = SanitizeArguments(m)
		} else if strVal, ok := item.(string); ok && isSensitiveValue(strVal) {
			clean[i] = "[REDACTED-VALUE]"
		} else if subSlice, ok := item.([]interface{}); ok {
			clean[i] = sanitizeSlice(subSlice)
		} else {
			clean[i] = item
		}
	}
	return clean
}

func isSensitiveKey(k string) bool {
	lower := strings.ToLower(k)
	sensitiveKeywords := []string{
		"password", "passwd", "token", "secret", "api_key",
		"apikey", "auth", "credential", "private_key",
		"bearer", "cookie", "session", "jwt",
		"signature", "cert", "passphrase", "client_key",
		"webhook", "stripe", "x_hub",
	}
	for _, kw := range sensitiveKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	// "pat" is matched boundary-aware so it doesn't match innocent words like "path"
	if lower == "pat" || strings.HasPrefix(lower, "pat_") || strings.HasSuffix(lower, "pat") ||
		strings.Contains(lower, "_pat_") || strings.Contains(lower, "github_pat") {
		return true
	}
	return false
}

func LogAuditEvent(event AuditEvent) {
	if event.Timestamp == "" {
		event.Timestamp = time.Now().UTC().Format(time.RFC3339)
	}

	if event.Arguments != nil {
		event.Arguments = SanitizeArguments(event.Arguments)
	}

	data, err := json.Marshal(event)
	if err != nil {
		log.Printf("[AUDIT ERROR] Failed to marshal audit event: %v", err)
		return
	}

	if globalAuditLogger == nil || globalAuditLogger.logFile == nil {
		// FAIL-SAFE: If audit logger is uninitialized or file handle is missing,
		// NEVER drop audit events silently (especially SECURITY_BLOCK). Output directly to stderr!
		log.Printf("[AUDIT FAILSAFE] %s", string(data))
		return
	}

	globalAuditLogger.mu.Lock()
	defer globalAuditLogger.mu.Unlock()

	if _, writeErr := globalAuditLogger.logFile.Write(append(data, '\n')); writeErr != nil {
		log.Printf("[AUDIT WRITE ERROR] %v | EVENT: %s", writeErr, string(data))
	}

	if event.EventType == EventSecurityBlock {
		log.Printf("🚨 [SECURITY ALERT] Agent '%s' BLOCKED on tool '%s'! Reason: %s", event.AgentID, event.Tool, event.Reason)
	} else if event.EventType == EventStateChange {
		log.Printf("⚡ [STATE CHANGE] Agent '%s' executed '%s' on '%s'", event.AgentID, event.Tool, event.Backend)
	}
}

var stateChangingVerbs = map[string]bool{
	// write / create / mutate
	"create": true, "update": true, "delete": true, "remove": true,
	"add": true, "put": true, "patch": true, "set": true, "write": true,
	"upload": true, "insert": true,
	// state transitions / lifecycle
	"merge": true, "close": true, "reopen": true, "lock": true, "unlock": true,
	"approve": true, "reject": true, "submit": true, "cancel": true,
	// publishes / sends
	"publish": true, "send": true, "dispatch": true,
	"push": true, "deploy": true, "release": true,
	// access / assignment changes
	"assign": true, "unassign": true, "grant": true, "revoke": true,
	"subscribe": true, "unsubscribe": true,
	"enable": true, "disable": true,
	"archive": true, "restore": true,
}

func IsStateChangingTool(toolName string) bool {
	tool := strings.ToLower(strings.TrimSpace(toolName))
	// Strip router prefix if present (e.g. "github__create_issue" -> "create_issue")
	if idx := strings.Index(tool, "__"); idx >= 0 {
		tool = tool[idx+2:]
	}
	segs := strings.Split(tool, "_")
	if len(segs) == 0 || segs[0] == "" {
		return false
	}

	verb := segs[0]
	lastSeg := segs[len(segs)-1]

	// Handle 'post' and 'trigger' with contextual read exclusions
	if verb == "post" {
		return lastSeg != "metadata" && lastSeg != "info" && lastSeg != "status"
	}
	if verb == "trigger" {
		return lastSeg != "search" && lastSeg != "status" && lastSeg != "history"
	}

	return stateChangingVerbs[verb]
}
