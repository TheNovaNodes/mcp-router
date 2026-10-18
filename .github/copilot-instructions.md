# GitHub Copilot & Cursor Instructions for `mcp-router`

## Engineering Principles
- **Language & Runtime:** Go 1.22+ (Idiomatic, performant, standard library first).
- **Zero-Panic Policy:** `mcp-router` is a mission-critical infrastructure daemon. Tool dispatching, deserialization, and subprocess management must never panic. Always use deferred `recover()` at handler boundaries.
- **Concurrency & Thread Safety:** All shared resources (backend tables, sessions, caches) must be guarded with `sync.RWMutex`. Every test must pass with `-race`.
- **Context & Lifecycle:** Always propagate `context.Context` to network calls, process executions, and timeouts. Avoid orphaned goroutines.
- **Error Handling:** Use `fmt.Errorf("operation failed: %w", err)` for wrapping. Never discard errors with `_ = ...` unless explicitly documented why it is safe.
- **Security Guardrails:**
  - Enforce backend and agent ACL allowlists before invoking tools.
  - Never log bearer tokens, authorization headers, or sensitive payload data.
  - Do not introduce external dependencies without strong justification; prefer the standard library.
- **Verification Commands:**
  - `go vet ./...`
  - `go test -v -race ./...`
  - `go build -v .`
