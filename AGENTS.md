# TheNovaNodes AGENTS.md Manifest

## Part 1: TheNovaNodes Core Invariants (Universal Standard)
1. **Strict Git Flow (ПРАВИЛА КРОВИ):**
   - NEVER push directly to main or master branches.
   - All changes must go through dedicated branches (feat/..., fix/..., docs/...) and Pull Requests.
   - NEVER merge PRs without explicit approval from ЗавЛаб.
   - No force-push on upstream branches.
2. **Security & Credential Hygiene:**
   - NEVER hardcode or log passwords, tokens, or credentials (especially MCP_ROUTER_BEARER_TOKEN, backend API keys).
   - All credentials must be loaded dynamically from environment variables or protected vault.
3. **Deadlock & Timeout Guardrails:**
   - All network calls and subprocess dispatches must have explicit timeouts (configured via config.Timeout).
   - Auxiliary commands must use hard timeouts.
   - Never loop endlessly without bounds.
4. **Continuous Verification:**
   - Never report a task complete without running local verification commands.
   - Use native project tools directly.

## Part 2: Repository Profile & Specific Directives (mcp-router)
1. **Project Overview & Tech Stack:**
   - Go (1.22+ / 1.25 compatible).
   - Key dependencies: gopkg.in/yaml.v3, standard library (net/http, sync, os/exec, context).
   - Role: Central nervous system and tool multiplexer for NovaNodes agents.
   - Core components: main.go (daemon entry point), router.go (tool dispatcher & allowlist), multiplexer.go (backend lifecycle), auth.go (bearer token auth), health.go (health probes), config.go (YAML schema).
2. **The Golden Loop (Mandatory Verification Commands):**
   - `go vet ./...`
   - `go test -v -race ./...`
   - `go build -o mcp-router ./...`
3. **Architectural Invariants & Taboos:**
   - **Zero-Panic Policy:** Router is the core daemon. Any panic during tool dispatching or backend execution MUST be recovered via `recover()`. Crashing the router process is strictly forbidden.
   - **Concurrency Safety:** All routing tables, backend pools, and session states MUST be protected by `sync.RWMutex`. Zero data races tolerated.
   - **Agent Branch Protection Guard:** The router's built-in security guard actively blocks agents from modifying protected branches (main/master). Agents working on mcp-router must strictly preserve this guard.
   - **Credential Masking:** Never echo tokens or secrets in error responses or access logs.
4. **PR & Commit Conventions:**
   - Conventional Commits (e.g. `docs(agents): ...`).
