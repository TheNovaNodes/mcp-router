package main

import (
	"crypto/subtle"
	"log"
	"net/http"
	"strings"
)

// AuthMiddleware enforces Bearer Token authentication if enabled
func AuthMiddleware(cfg AuthConfig, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !cfg.Enabled {
			next.ServeHTTP(w, r)
			return
		}

		authHeader := r.Header.Get("Authorization")
		token := ""
		tokenSource := "none"

		// RFC 6750: Case-insensitive "Bearer " prefix in Authorization header
		if len(authHeader) >= 7 && strings.EqualFold(authHeader[:7], "bearer ") {
			token = authHeader[7:]
			tokenSource = "header"
		} else if cfg.AllowQueryToken {
			// Query parameter ?token= is ONLY accepted if explicitly allowed in configuration
			queryToken := r.URL.Query().Get("token")
			if queryToken != "" {
				token = queryToken
				tokenSource = "query"
			}
		}

		token = strings.TrimSpace(token)
		expectedToken := strings.TrimSpace(cfg.BearerToken)

		if token == "" || subtle.ConstantTimeCompare([]byte(token), []byte(expectedToken)) != 1 {
			agentID := r.Header.Get("X-Agent-ID")
			if agentID == "" {
				agentID = "unknown"
			}
			log.Printf("🔒 [AUTH DENIED] Unauthorized access attempt by agent '%s' from IP %s (source: %s, path: %s)",
				agentID, r.RemoteAddr, tokenSource, r.URL.Path)

			// Record 401 unauthorized attempt in audit trail to prevent invisible brute-force attacks
			LogAuditEvent(AuditEvent{
				EventType: EventSecurityBlock,
				AgentID:   agentID,
				Backend:   "auth",
				Tool:      r.URL.Path,
				Reason:    "Unauthorized: Invalid or missing Bearer token",
				Arguments: map[string]interface{}{
					"path":        r.URL.Path,
					"source":      tokenSource,
					"remote_addr": r.RemoteAddr,
				},
			})

			// RFC 6750 §3 compliance: send WWW-Authenticate challenge header
			w.Header().Set("WWW-Authenticate", `Bearer realm="mcp-router", error="invalid_token"`)
			http.Error(w, "Unauthorized: Invalid or missing Bearer token", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}
