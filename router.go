package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// ExtractAgentID extracts the agent identifier from a composite session ID formatted as "<agentID>:<uuid>".
// If no colon separator is found, the entire session ID is returned as the agent ID.
func ExtractAgentID(sessionID string) string {
	if agentID, _, found := strings.Cut(sessionID, ":"); found && agentID != "" {
		return agentID
	}
	return sessionID
}

func findBackendForTool(mux *Multiplexer, toolName string) (string, string) {
	for _, b := range mux.GetBackends() {
		if b.Config.Prefix != "" && strings.HasPrefix(toolName, b.Config.Prefix) {
			return b.Name, strings.TrimPrefix(toolName, b.Config.Prefix)
		}
	}
	for _, b := range mux.GetBackends() {
		for _, t := range b.CachedTools {
			if t.Name == toolName {
				return b.Name, t.Name
			}
		}
	}
	return "", ""
}

func BuildRouter(mux *Multiplexer) *server.MCPServer {
	var s *server.MCPServer

	// Hook into session registration to populate tools based on ACL
	hooks := &server.Hooks{
		OnRegisterSession: []server.OnRegisterSessionHookFunc{
			func(ctx context.Context, session server.ClientSession) {
				sessionID := session.SessionID()
				agentID := ExtractAgentID(sessionID)
				log.Printf("Agent %s (session: %s) connected. Populating tools...", agentID, sessionID)

				grantedCount := 0
				allowedBackends := 0
				var sessionTools []server.ServerTool

				for _, b := range mux.GetBackends() {
					if mux.IsAgentAllowed(b.Name, agentID) {
						allowedBackends++
						allowlist := b.Config.GetToolsAllowlistForAgent(agentID)
						// Use cached tools instead of querying synchronously
						for _, tool := range b.CachedTools {
							if len(allowlist) > 0 && !isToolAllowed(tool.Name, b.Config.Prefix, allowlist) {
								continue
							}
							// Prefix the tool name
							prefixedTool := tool
							originalName := tool.Name
							prefixedTool.Name = b.Config.Prefix + originalName

							// Create handler closure dynamically looking up backend by name via mux
							handler := CreateToolHandler(b.Name, mux, originalName, agentID)

							sessionTools = append(sessionTools, server.ServerTool{
								Tool:    prefixedTool,
								Handler: handler,
							})
						}
					}
				}

				// Batch register all session tools in a single operation (O(1) map copy)
				if len(sessionTools) > 0 {
					if err := s.AddSessionTools(sessionID, sessionTools...); err != nil {
						log.Printf("Failed to register %d session tools for session %s: %v", len(sessionTools), sessionID, err)
					} else {
						grantedCount = len(sessionTools)
					}
				}

				log.Printf("Session %s registered for agent %s: granted %d tools across %d backends", sessionID, agentID, grantedCount, allowedBackends)
			},
		},
	}

	toolFilter := func(ctx context.Context, tools []mcp.Tool) []mcp.Tool {
		var agentID string
		if session := server.ClientSessionFromContext(ctx); session != nil {
			agentID = ExtractAgentID(session.SessionID())
		}
		if agentID == "" {
			agentID = "anonymous"
		}

		var allowed []mcp.Tool
		for _, tool := range tools {
			backendName, origName := findBackendForTool(mux, tool.Name)
			if backendName == "" {
				continue
			}
			if !mux.IsAgentAllowed(backendName, agentID) {
				continue
			}
			backend, ok := mux.GetBackend(backendName)
			if !ok || backend == nil {
				continue
			}
			allowlist := backend.Config.GetToolsAllowlistForAgent(agentID)
			if len(allowlist) > 0 && !isToolAllowed(origName, backend.Config.Prefix, allowlist) {
				continue
			}
			allowed = append(allowed, tool)
		}
		return allowed
	}

	s = server.NewMCPServer("mcp-router", "1.0.0",
		server.WithToolCapabilities(true),
		server.WithHooks(hooks),
		server.WithToolFilter(toolFilter),
	)

	// Register all cached backend tools globally as fallback across dual transports
	for _, b := range mux.GetBackends() {
		for _, tool := range b.CachedTools {
			prefixedTool := tool
			originalName := tool.Name
			prefixedTool.Name = b.Config.Prefix + originalName
			handler := CreateToolHandler(b.Name, mux, originalName, "")
			s.AddTool(prefixedTool, handler)
		}
	}

	return s
}

// CreateToolHandler builds a ToolHandlerFunc enforcing dynamic backend resolution (anti-stale pointer on reload),
// concurrency limiting, overload timeout, security guard policy (branch protection & PR merge ban),
// backend availability, and execution timeouts.
func CreateToolHandler(backendName string, mux *Multiplexer, origName string, agentID string) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		startTime := time.Now()
		targetAgentID := agentID
		if targetAgentID == "" || targetAgentID == "anonymous" {
			if session := server.ClientSessionFromContext(ctx); session != nil {
				if ext := ExtractAgentID(session.SessionID()); ext != "" && ext != "anonymous" {
					targetAgentID = ext
				}
			}
		}
		if targetAgentID == "" {
			targetAgentID = "anonymous"
		}
		log.Printf("Agent %s called tool %s (backend: %s)", targetAgentID, request.Params.Name, backendName)

		// Extract args map if available
		var argsMap map[string]interface{}
		if am, ok := request.Params.Arguments.(map[string]interface{}); ok {
			argsMap = am
		}

		backend, ok := mux.GetBackend(backendName)
		if !ok || backend == nil {
			reason := fmt.Sprintf("backend '%s' is unavailable (not found in multiplexer)", backendName)
			LogAuditEvent(AuditEvent{
				EventType: EventToolError,
				AgentID:   targetAgentID,
				Backend:   backendName,
				Tool:      request.Params.Name,
				Error:     reason,
				Arguments: argsMap,
			})
			return mcp.NewToolResultError(reason), nil
		}

		// Concurrency limiting & Overload Guard (Issue #6 & Issue #10): Bounded worker pool with queue timeout
		if backend.Semaphore != nil {
			queueTimeout := 15 * time.Second
			acquireTimer := time.NewTimer(queueTimeout)
			defer acquireTimer.Stop()

			select {
			case backend.Semaphore <- struct{}{}:
				defer func() { <-backend.Semaphore }()
			case <-acquireTimer.C:
				reason := fmt.Sprintf("backend '%s' is overloaded: all %d worker slots busy (queue timeout 15s)", backend.Name, cap(backend.Semaphore))
				log.Printf("OVERLOAD GUARD BLOCKED: Agent %s queued on %s exceeded timeout", targetAgentID, backend.Name)
				LogAuditEvent(AuditEvent{
					EventType: EventToolError,
					AgentID:   targetAgentID,
					Backend:   backend.Name,
					Tool:      request.Params.Name,
					Error:     reason,
					Arguments: argsMap,
				})
				return mcp.NewToolResultError(reason), nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		// SECURITY GUARD: Enforce Tools Allowlist if configured on backend
		allowlist := backend.Config.GetToolsAllowlistForAgent(targetAgentID)
		if len(allowlist) > 0 && !isToolAllowed(origName, backend.Config.Prefix, allowlist) {
			reason := fmt.Sprintf("SECURITY POLICY VIOLATION: Tool '%s' is not permitted by tools_allowlist on backend '%s'", origName, backend.Name)
			log.Printf("SECURITY GUARD BLOCKED: Agent %s attempted to call unallowed tool %s on %s", targetAgentID, request.Params.Name, backend.Name)
			LogAuditEvent(AuditEvent{
				EventType: EventSecurityBlock,
				AgentID:   targetAgentID,
				Backend:   backend.Name,
				Tool:      request.Params.Name,
				Reason:    reason,
				Arguments: argsMap,
			})
			return mcp.NewToolResultError(reason), nil
		}

		// SECURITY GUARD (Issue #8): Enforce Branch Protection & PR Merge Ban for GitHub MCP backends
		if backend.GetType() == "github" {
			// 1. Block merging Pull Requests
			if origName == "merge_pull_request" {
				reason := "SECURITY POLICY VIOLATION: Agents are strictly prohibited from merging Pull Requests! Request human review from ZaVLab."
				log.Printf("SECURITY GUARD BLOCKED: Agent %s attempted to merge PR via %s", targetAgentID, request.Params.Name)
				LogAuditEvent(AuditEvent{
					EventType: EventSecurityBlock,
					AgentID:   targetAgentID,
					Backend:   backend.Name,
					Tool:      request.Params.Name,
					Reason:    reason,
					Arguments: argsMap,
				})
				return mcp.NewToolResultError(reason), nil
			}

			// 2. Block direct pushes / modifications to protected branches (main, master) across all branch/ref argument keys and recursive value tree
			if argsMap != nil {
				dangerousTools := map[string]bool{
					"push_files":                 true,
					"create_or_update_file":      true,
					"delete_file":                true,
					"create_branch":              true,
					"update_pull_request_branch": true,
				}

				if dangerousTools[origName] {
					if matchedVal, matchedPath, found := findForbiddenBranch(argsMap, "args"); found {
						reason := fmt.Sprintf("SECURITY POLICY VIOLATION: forbidden branch '%s' at %s. Direct push/modification to protected branches is forbidden for Agents. Create a feature branch and submit a Pull Request!", matchedVal, matchedPath)
						log.Printf("SECURITY GUARD BLOCKED: Agent %s attempted direct modification on protected branch '%s' at %s via tool %s", targetAgentID, matchedVal, matchedPath, request.Params.Name)
						LogAuditEvent(AuditEvent{
							EventType: EventSecurityBlock,
							AgentID:   targetAgentID,
							Backend:   backend.Name,
							Tool:      request.Params.Name,
							Reason:    reason,
							Arguments: argsMap,
						})
						return mcp.NewToolResultError(reason), nil
					}
				}
			}
		}

		// Resilience Guard: Check if backend client is connected and healthy
		if backend.Client == nil {
			reason := fmt.Sprintf("backend '%s' is unavailable (not connected or degraded)", backend.Name)
			LogAuditEvent(AuditEvent{
				EventType: EventToolError,
				AgentID:   targetAgentID,
				Backend:   backend.Name,
				Tool:      request.Params.Name,
				Error:     reason,
				Arguments: argsMap,
			})
			return mcp.NewToolResultError(reason), nil
		}

		// Execution Timeout Guard: enforce bounded execution time per backend
		callTimeout := backend.Config.GetCallTimeout()
		callCtx, callCancel := context.WithTimeout(ctx, callTimeout)
		defer callCancel()

		// Reconstruct the request for backend
		backendReq := mcp.CallToolRequest{}
		backendReq.Params.Name = origName
		backendReq.Params.Arguments = request.Params.Arguments
		res, err := safeCallTool(backend.Client, callCtx, backendReq)

		duration := time.Since(startTime).String()

		if errors.Is(callCtx.Err(), context.DeadlineExceeded) {
			reason := fmt.Sprintf("tool execution timed out after %v", callTimeout)
			log.Printf("TIMEOUT GUARD: Tool %s on backend %s timed out after %v", request.Params.Name, backend.Name, callTimeout)
			LogAuditEvent(AuditEvent{
				EventType: EventToolError,
				AgentID:   targetAgentID,
				Backend:   backend.Name,
				Tool:      request.Params.Name,
				Duration:  duration,
				Error:     reason,
				Arguments: argsMap,
			})
			return mcp.NewToolResultError(reason), nil
		}

		if err != nil {
			LogAuditEvent(AuditEvent{
				EventType: EventToolError,
				AgentID:   targetAgentID,
				Backend:   backend.Name,
				Tool:      request.Params.Name,
				Duration:  duration,
				Error:     err.Error(),
				Arguments: argsMap,
			})
			return res, err
		}

		if res == nil {
			reason := fmt.Sprintf("backend '%s' returned nil result without error", backend.Name)
			LogAuditEvent(AuditEvent{
				EventType: EventToolError,
				AgentID:   targetAgentID,
				Backend:   backend.Name,
				Tool:      request.Params.Name,
				Duration:  duration,
				Error:     reason,
				Arguments: argsMap,
			})
			return mcp.NewToolResultError(reason), nil
		}

		// Zero-Noise Event Logging: Only record State Changes or Execution Errors
		if res.IsError {
			errMsg := extractResultErrorMessage(res)
			LogAuditEvent(AuditEvent{
				EventType: EventToolError,
				AgentID:   targetAgentID,
				Backend:   backend.Name,
				Tool:      request.Params.Name,
				Duration:  duration,
				Error:     errMsg,
				Arguments: argsMap,
			})
		} else if IsStateChangingTool(origName) {
			LogAuditEvent(AuditEvent{
				EventType: EventStateChange,
				AgentID:   targetAgentID,
				Backend:   backend.Name,
				Tool:      request.Params.Name,
				Duration:  duration,
				Arguments: argsMap,
			})
		}
		// Pure read tools with 200 OK are silently filtered out (Zero Noise)

		return res, nil
	}
}

var nonBranchContentKeys = map[string]bool{
	"path":           true,
	"file_path":      true,
	"filename":       true,
	"content":        true,
	"file_content":   true,
	"body":           true,
	"message":        true,
	"commit_message": true,
	"patch":          true,
	"diff":           true,
	"title":          true,
	"description":    true,
}

func isForbiddenBranchValue(val string) bool {
	clean := strings.ToLower(strings.TrimSpace(val))
	return clean == "main" || clean == "master" ||
		strings.HasSuffix(clean, "/main") || strings.HasSuffix(clean, "/master")
}

func findForbiddenBranch(v interface{}, path string) (string, string, bool) {
	switch val := v.(type) {
	case string:
		if isForbiddenBranchValue(val) {
			return val, path, true
		}
	case map[string]interface{}:
		for k, item := range val {
			lowerK := strings.ToLower(k)
			if nonBranchContentKeys[lowerK] {
				continue
			}
			keyPath := path + "." + k
			if matchedVal, matchedPath, found := findForbiddenBranch(item, keyPath); found {
				return matchedVal, matchedPath, true
			}
		}
	case []interface{}:
		for i, item := range val {
			elemPath := fmt.Sprintf("%s[%d]", path, i)
			if matchedVal, matchedPath, found := findForbiddenBranch(item, elemPath); found {
				return matchedVal, matchedPath, true
			}
		}
	}
	return "", "", false
}

func safeCallTool(mcpClient client.MCPClient, ctx context.Context, req mcp.CallToolRequest) (res *mcp.CallToolResult, err error) {
	defer func() {
		if r := recover(); r != nil {
			res = mcp.NewToolResultError(fmt.Sprintf("backend panic: %v", r))
			err = nil
		}
	}()
	return mcpClient.CallTool(ctx, req)
}

func extractResultErrorMessage(res *mcp.CallToolResult) string {
	if res == nil {
		return ""
	}
	if len(res.Content) > 0 {
		return fmt.Sprintf("%v", res.Content[0])
	}
	return "unknown tool error"
}

var ssPIDRegex = regexp.MustCompile(`pid=(\d+)`)

// extractBotFromPath extracts the bot identifier from an agent directory path.
// It handles paths like "~/.agents/kairos_brobot", "/home/user/.agents/kairos_brobot/scratch",
// or relative ".agents/kairos_brobot".
func extractBotFromPath(p string) string {
	clean := filepath.Clean(strings.TrimSpace(p))
	if clean == "" || clean == "." {
		return ""
	}

	agentsMarker := string(filepath.Separator) + ".agents" + string(filepath.Separator)
	idx := strings.Index(clean, agentsMarker)
	if idx == -1 {
		if strings.HasPrefix(clean, ".agents"+string(filepath.Separator)) {
			idx = 0
			agentsMarker = ".agents" + string(filepath.Separator)
		} else {
			return ""
		}
	}

	rest := clean[idx+len(agentsMarker):]
	parts := strings.Split(rest, string(filepath.Separator))
	if len(parts) > 0 {
		bot := strings.TrimSpace(parts[0])
		if bot != "" && bot != "common" && bot != "." && bot != ".." {
			return bot
		}
	}
	return ""
}

// resolveAgentIDFromLocalPeer inspects incoming loopback connections (127.0.0.1 / ::1)
// to determine the caller agent ID via peer socket port -> PID -> CWD/cmdline inspection.
func resolveAgentIDFromLocalPeer(ctx context.Context, remoteAddr string) string {
	remoteAddr = strings.TrimSpace(remoteAddr)
	if remoteAddr == "" {
		return ""
	}

	host, portStr, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return ""
	}

	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return ""
	}

	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 {
		return ""
	}

	// Fast lookup using ss command with bounded timeout
	execCtx, cancel := context.WithTimeout(ctx, 800*time.Millisecond)
	defer cancel()

	cmd := exec.CommandContext(execCtx, "ss", "-ntpH", fmt.Sprintf("sport = :%d", port))
	out, err := cmd.Output()
	if err != nil || len(out) == 0 {
		return ""
	}

	matches := ssPIDRegex.FindSubmatch(out)
	if len(matches) < 2 {
		return ""
	}

	pid, err := strconv.Atoi(string(matches[1]))
	if err != nil || pid <= 0 {
		return ""
	}

	// 1. Check CWD symlink
	cwdLink, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", pid))
	if err == nil {
		if bot := extractBotFromPath(cwdLink); bot != "" {
			log.Printf("🔍 [PEER RESOLVE] Identified agent '%s' from PID %d CWD (%s)", bot, pid, cwdLink)
			return bot
		}
	}

	// 2. Check cmdline arguments
	cmdlineBytes, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err == nil {
		args := strings.Split(string(cmdlineBytes), "\x00")
		for i, arg := range args {
			if (arg == "--add-dir" || arg == "--workspace") && i+1 < len(args) {
				if bot := extractBotFromPath(args[i+1]); bot != "" {
					log.Printf("🔍 [PEER RESOLVE] Identified agent '%s' from PID %d cmdline arg %s (%s)", bot, pid, arg, args[i+1])
					return bot
				}
			}
			if arg == "--agent" && i+1 < len(args) {
				candidate := strings.TrimSpace(args[i+1])
				if candidate != "" && candidate != "common" {
					log.Printf("🔍 [PEER RESOLVE] Identified agent '%s' from PID %d --agent flag", candidate, pid)
					return candidate
				}
			}
		}
	}

	return ""
}

// SessionIDExtractor extracts agent ID from request headers (X-Agent-ID) or query parameters (?agent=, ?agent_id=)
// and generates a unique session ID formatted as "<agentID>:<uuid>" to guarantee session isolation across concurrent connections.
// If not explicitly provided, it resolves the caller agent identity for local loopback connections via peer socket inspection.
func SessionIDExtractor(ctx context.Context, r *http.Request) (string, error) {
	agentID := strings.TrimSpace(r.Header.Get("X-Agent-ID"))
	if agentID == "" {
		agentID = strings.TrimSpace(r.URL.Query().Get("agent"))
	}
	if agentID == "" {
		agentID = strings.TrimSpace(r.URL.Query().Get("agent_id"))
	}
	if agentID == "" || agentID == "anonymous" {
		if loopbackAgent := resolveAgentIDFromLocalPeer(ctx, r.RemoteAddr); loopbackAgent != "" {
			agentID = loopbackAgent
		}
	}
	if agentID == "" {
		agentID = "anonymous"
	}

	// Preserve existing session ID if client provides one matching agent prefix
	if mcpSessionID := strings.TrimSpace(r.Header.Get("Mcp-Session-Id")); mcpSessionID != "" {
		if strings.HasPrefix(mcpSessionID, agentID+":") {
			return mcpSessionID, nil
		}
	}

	sessionUUID := uuid.New().String()
	return fmt.Sprintf("%s:%s", agentID, sessionUUID), nil
}

// isToolAllowed checks whether toolName is permitted under allowlist, taking prefix and case into account.
func isToolAllowed(toolName string, prefix string, allowlist []string) bool {
	if len(allowlist) == 0 {
		return true
	}
	cleanName := strings.ToLower(strings.TrimSpace(toolName))
	cleanPrefix := strings.ToLower(strings.TrimSpace(prefix))
	strippedName := cleanName
	if cleanPrefix != "" && strings.HasPrefix(cleanName, cleanPrefix) {
		strippedName = strings.TrimPrefix(cleanName, cleanPrefix)
	}

	for _, allowed := range allowlist {
		cleanAllowed := strings.ToLower(strings.TrimSpace(allowed))
		if cleanAllowed == cleanName || cleanAllowed == strippedName {
			return true
		}
		if cleanPrefix != "" && cleanAllowed == cleanPrefix+strippedName {
			return true
		}
	}
	return false
}

