package main

import (
	"encoding/json"
	"net/http"
	"time"
)

type DetailedHealthStatus struct {
	Status      string            `json:"status"`
	Timestamp   string            `json:"timestamp"`
	AuditLogger string            `json:"audit_logger"`
	Backends    map[string]string `json:"backends"`
}

func HealthHandler(mux *Multiplexer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		
		status := "healthy"
		backendStatuses := make(map[string]string)
		
		backends := mux.GetBackends()
		if len(backends) == 0 {
			status = "degraded"
		}

		// Check backend status:
		// READY: connected and has cached tools
		// UP: connected with 0 cached tools (alive)
		// DEGRADED: not connected / nil client
		for _, b := range backends {
			if !b.IsConnected() {
				backendStatuses[b.Name] = "DEGRADED"
				status = "degraded"
			} else if b.HasTools() {
				backendStatuses[b.Name] = "READY"
			} else {
				backendStatuses[b.Name] = "UP"
			}
		}

		resp := DetailedHealthStatus{
			Status:      status,
			Timestamp:   time.Now().UTC().Format(time.RFC3339),
			AuditLogger: GetAuditLoggerStatus(),
			Backends:    backendStatuses,
		}

		if status == "healthy" {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusMultiStatus)
		}

		json.NewEncoder(w).Encode(resp)
	}
}
