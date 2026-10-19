package main

import (
	"testing"
	"time"
)

func TestHITLManager_StageAndConsume(t *testing.T) {
	mgr := NewHITLManager(10, 5*time.Minute)

	args := map[string]interface{}{
		"pull_number": 42,
		"commit_title": "feat: cool feature",
	}

	token := mgr.Stage("LeonardoAuditor_bot", "github-novanodes", "merge_pull_request", "pr_merge", args)
	if token == "" {
		t.Fatal("expected non-empty token")
	}

	if mgr.Len() != 1 {
		t.Errorf("expected 1 pending action, got %d", mgr.Len())
	}

	// 1. Wrong tool
	_, err := mgr.ValidateAndConsume(token, "LeonardoAuditor_bot", "devops_manage_service")
	if err == nil {
		t.Error("expected error for mismatched tool, got nil")
	}

	// 2. Wrong agent
	_, err = mgr.ValidateAndConsume(token, "trickster_gobot", "merge_pull_request")
	if err == nil {
		t.Error("expected error for mismatched agent, got nil")
	}

	// 3. Valid consumption
	act, err := mgr.ValidateAndConsume(token, "LeonardoAuditor_bot", "merge_pull_request")
	if err != nil {
		t.Fatalf("expected successful consumption, got error: %v", err)
	}
	if act.Tool != "merge_pull_request" || act.AgentID != "LeonardoAuditor_bot" {
		t.Errorf("unexpected action retrieved: %+v", act)
	}

	// 4. Token replay attempt should fail
	_, err = mgr.ValidateAndConsume(token, "LeonardoAuditor_bot", "merge_pull_request")
	if err == nil {
		t.Error("expected replay attack to fail, got nil error")
	}
	if mgr.Len() != 0 {
		t.Errorf("expected 0 pending actions after consumption, got %d", mgr.Len())
	}
}

func TestHITLManager_Expiration(t *testing.T) {
	mgr := NewHITLManager(10, 10*time.Millisecond)

	token := mgr.Stage("agent1", "backend1", "tool1", "action1", nil)
	time.Sleep(20 * time.Millisecond)

	_, err := mgr.ValidateAndConsume(token, "agent1", "tool1")
	if err == nil {
		t.Error("expected expired token to fail, got nil error")
	}
}

func TestHITLManager_CapacityEviction(t *testing.T) {
	mgr := NewHITLManager(2, 1*time.Hour)

	tok1 := mgr.Stage("agent1", "backend1", "tool1", "action1", nil)
	time.Sleep(2 * time.Millisecond)
	tok2 := mgr.Stage("agent1", "backend1", "tool1", "action1", nil)
	time.Sleep(2 * time.Millisecond)
	tok3 := mgr.Stage("agent1", "backend1", "tool1", "action1", nil)

	// tok1 should be evicted because capacity is 2
	_, err := mgr.ValidateAndConsume(tok1, "agent1", "tool1")
	if err == nil {
		t.Error("expected oldest token tok1 to be evicted, got nil error")
	}

	// tok2 and tok3 should still be valid
	if _, err := mgr.ValidateAndConsume(tok2, "agent1", "tool1"); err != nil {
		t.Errorf("expected tok2 to be valid, got: %v", err)
	}
	if _, err := mgr.ValidateAndConsume(tok3, "agent1", "tool1"); err != nil {
		t.Errorf("expected tok3 to be valid, got: %v", err)
	}
}
