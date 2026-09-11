# Contributing to MCP Router

Thank you for contributing to **MCP Router**, the mission-critical unified gateway and multiplexer of the **TheNovaNodes** autonomous AI agent collective.

Both human engineers and autonomous AI agents are welcome to contribute under the strict collective foundation directives outlined below.

---

## 🩸 Core Foundation Directives ("Правила Крови")

1. **Strict Git Flow:**
   - **NEVER push directly to `main` or `master` branches.**
   - All bugfixes, refactorings, security patches, and features **MUST** go through dedicated feature branches (`feat/*`, `fix/*`, `chore/*`, `docs/*`) and Pull Requests (PR).
   - **NEVER merge PRs without explicit approval from ЗавЛаб.**

2. **Verification Without Absurdity:**
   - **Pre-commit / Pre-push (Native Minimum):** Verify changes locally before committing:
     ```bash
     go test -v ./...
     go test -race ./...
     ```
     Ensure all unit tests pass with zero regressions and no race conditions.
   - **Post-PR (Cloud CI via GitHub CLI):** After opening a PR, verify cloud CI status using the GitHub CLI:
     ```bash
     gh pr checks <PR_NUMBER>
     ```
     Never report task completion or request merge approval until the cloud CI checks are fully green.

3. **Zero-Deadlock & Network Timeout Directive:**
   - Every network socket, HTTP call, or subprocess interaction **MUST** enforce an explicit timeout (`context.WithTimeout`, `ReadHeaderTimeout`, `IdleTimeout`, or per-backend `timeout`).
   - Infinite blocking calls on sockets or streaming endpoints are strictly prohibited.

4. **Documentation Synchronization Invariant:**
   - Code changes, configuration schema updates, or security policy alterations **MUST** be accompanied by synchronized updates across `README.md`, `ARCHITECTURE.md`, `SECURITY.md`, and `CHANGELOG.md` in the same commit / PR.
   - Documentation must never drift from actual codebase mechanics.

---

## 🛠️ Development Setup & Workflow

### 1. Prerequisites
- **Go 1.25+**
- **Git**
- **GitHub CLI (`gh`)** authenticated with proper organization scopes

### 2. Build the Daemon
```bash
git clone https://github.com/TheNovaNodes/mcp-router.git
cd mcp-router
go build -v -o mcp-router .
```

### 3. Run Tests and Coverage
```bash
# Run all unit tests with race detection
go test -v -race ./...

# Run test coverage profiling
go test -coverprofile=coverage.out ./...
go tool cover -func=coverage.out
```

### 4. Running Locally
```bash
# Run daemon with default config (config.yaml) on :8090
./mcp-router

# Run with custom config and port
./mcp-router -config /path/to/custom-config.yaml -port :8095
```

---

## 🚀 Pull Request Checklist

Before requesting review from ЗавЛаб, verify:
- [ ] Changes are isolated in a dedicated branch branched off `master`.
- [ ] All unit tests pass locally (`go test -v -race ./...`).
- [ ] No secrets or sensitive configuration values are committed.
- [ ] Commit messages follow Conventional Commits format (`feat:`, `fix:`, `docs:`, `chore:`).
- [ ] Cloud CI pipeline is 100% green (`gh pr checks <PR>`).
- [ ] Associated documentation files are updated.
