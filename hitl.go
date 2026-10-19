package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

const (
	DefaultHITLTTL        = 15 * time.Minute
	DefaultMaxPendingHITL = 200
)

// HITLStagedAction represents a critical action (PR Merge or Service Restart/Deploy)
// staged in memory awaiting explicit Human-In-The-Loop approval from ZaVLab.
type HITLStagedAction struct {
	Token      string                 `json:"token"`
	AgentID    string                 `json:"agent_id"`
	Backend    string                 `json:"backend"`
	Tool       string                 `json:"tool"`
	ActionType string                 `json:"action_type"`
	Arguments  map[string]interface{} `json:"arguments"`
	CreatedAt  time.Time              `json:"created_at"`
	ExpiresAt  time.Time              `json:"expires_at"`
}

// HITLManager coordinates validation, staging, and single-use consumption of HITL tokens.
type HITLManager struct {
	mu         sync.Mutex
	pending    map[string]*HITLStagedAction
	ttl        time.Duration
	maxPending int
}

var defaultHITLManager = NewHITLManager(DefaultMaxPendingHITL, DefaultHITLTTL)

// NewHITLManager creates a new HITL Manager instance.
func NewHITLManager(maxPending int, ttl time.Duration) *HITLManager {
	if maxPending <= 0 {
		maxPending = DefaultMaxPendingHITL
	}
	if ttl <= 0 {
		ttl = DefaultHITLTTL
	}
	return &HITLManager{
		pending:    make(map[string]*HITLStagedAction),
		ttl:        ttl,
		maxPending: maxPending,
	}
}

func generateHITLToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return "hitl_" + hex.EncodeToString(b)
}

// Stage saves a mutating action, evicts expired/old actions, and returns a unique single-use token.
func (m *HITLManager) Stage(agentID, backend, tool, actionType string, args map[string]interface{}) string {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	// Clean expired tokens
	for tok, act := range m.pending {
		if now.After(act.ExpiresAt) {
			delete(m.pending, tok)
		}
	}

	// Evict oldest if capacity is reached
	if len(m.pending) >= m.maxPending {
		var oldestTok string
		var oldestTime time.Time
		first := true
		for tok, act := range m.pending {
			if first || act.CreatedAt.Before(oldestTime) {
				oldestTok = tok
				oldestTime = act.CreatedAt
				first = false
			}
		}
		if oldestTok != "" {
			delete(m.pending, oldestTok)
		}
	}

	token := generateHITLToken()
	m.pending[token] = &HITLStagedAction{
		Token:      token,
		AgentID:    agentID,
		Backend:    backend,
		Tool:       tool,
		ActionType: actionType,
		Arguments:  cloneArgsMap(args),
		CreatedAt:  now,
		ExpiresAt:  now.Add(m.ttl),
	}
	return token
}

// ValidateAndConsume checks whether a token is valid, unexpired, and matches the calling context.
// If valid, the token is atomically consumed and returned to prevent replay attacks.
func (m *HITLManager) ValidateAndConsume(token, agentID, tool string) (*HITLStagedAction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	act, ok := m.pending[token]
	if !ok {
		return nil, fmt.Errorf("invalid, expired, or already consumed HITL token '%s'", token)
	}

	if now.After(act.ExpiresAt) {
		delete(m.pending, token)
		return nil, fmt.Errorf("HITL token '%s' expired at %s", token, act.ExpiresAt.Format(time.RFC3339))
	}

	// Validate agent match if both are non-empty and non-anonymous
	if act.AgentID != "" && act.AgentID != "anonymous" && agentID != "" && agentID != "anonymous" && act.AgentID != agentID {
		return nil, fmt.Errorf("HITL token '%s' was staged by agent '%s', cannot be consumed by agent '%s'", token, act.AgentID, agentID)
	}

	if act.Tool != tool {
		return nil, fmt.Errorf("HITL token '%s' was staged for tool '%s', not '%s'", token, act.Tool, tool)
	}

	// Single-use: delete atomically
	delete(m.pending, token)
	return act, nil
}

// Len returns the number of currently active staged actions.
func (m *HITLManager) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.pending)
}

func cloneArgsMap(args map[string]interface{}) map[string]interface{} {
	if args == nil {
		return nil
	}
	cloned := make(map[string]interface{}, len(args))
	for k, v := range args {
		if k != "hitl_token" && k != "token" && k != "hitl_approved" {
			cloned[k] = v
		}
	}
	return cloned
}
