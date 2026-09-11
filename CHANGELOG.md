# Changelog

All notable changes to **MCP Router** will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## [1.0.0] - 2026-09-11

### Added
- **Unified Multi-Transport MCP Gateway:** Multiplexes `stdio`, `streamable_http`, and `sse` backends behind a single high-performance SSE/HTTP endpoint.
- **12 Production Ecosystem Backends:** Multiplexed and partitioned across 4 active security clusters (Base Stack, Personal Office, GitHub Contours, Google Sandbox).
- **Containerization & Developer Experience (DX):** Multi-stage `Dockerfile` (< 20 MB Alpine image), `docker-compose.yml`, and 60-second Quick Start documentation for Claude Desktop, Cursor, and agent networks.
- **Zero-Downtime Configuration Reload (SIGHUP):** Shadow-init atomic pointer swap ensuring zero dropped requests during live configuration updates.
- **Zero-Noise Structured Audit Logging (`logger.go`):** Emits JSON-lines for `SECURITY_BLOCK`, `STATE_CHANGE`, and `TOOL_ERROR` events to `/var/log/mcp-router/audit.jsonl` while filtering routine reads. Automatic fallback and logrotate reopen support.
- **Data Redaction Engine:** Automatic redaction of sensitive credentials (GitHub PATs, JWTs, AWS keys, bearer tokens, passwords) in audit logs.
- **Security Policy Guard (`router.go`):**
  - PR Merge Ban: AI agents are strictly prevented from merging pull requests.
  - Multi-Parameter Protected Branch Guard: Blocks direct mutations targeting `main` or `master` across all git ref formats and argument parameters.
- **Perimeter & Ingress Security (`auth.go`):** Constant-time Bearer token authentication resisting timing attacks, with protected query token (`allow_query_token: true`) guard.
- **Session Isolation & Batch Registration:** Compound session keys `<agentID>:<uuid>` and $O(1)$ batch tool registration via `s.AddSessionTools()`.
- **Worker Pools & Overload Guard:** Bounded concurrency semaphores per backend (`max_concurrent`) with 15-second queue acquisition timeout.
- **Execution Timeout Guard:** Configurable per-backend timeout (default 60s) to fail-fast on hung subprocesses.
- **Environment Sanitization (`config.go`):** Minimal safe system baseline (`PATH`, `TMPDIR`, etc.) with deliberate exclusion of `HOME` and `USER` to prevent credential leakage; explicit `env_inherit:` support.
- **Automated Cross-Platform Releases:** GitHub Actions matrix workflow compiling Linux, macOS, and Windows static binaries with SHA256 checksums on tag push.
- **Active Health Endpoint (`/health`):** Real-time monitoring returning `READY`, `UP`, and `DEGRADED` backend statuses.
- **Prometheus Metrics (`/metrics`):** Export of runtime telemetry and HTTP request counters.

### Changed
- **Decoupled Portable Configuration:** Untracked production `config.yaml`, standardized on generic `config.example.yaml` with standard FHS binary paths.
- **FinOps CI Optimization (#80, #81):** Added build concurrency cancellation (`cancel-in-progress: true`), job timeout limits, shallow checkout (`fetch-depth: 1`), and documentation paths filtering.
- **Test Fixture Sanitization (#84):** Normalized peer inspection test IP to RFC 5737 documentation block (`192.0.2.50`) and sanitized agent office directory paths.
- **Repository Hygiene (#83):** Added `.gitattributes` enforcing LF endings across all operating systems.

### Fixed
- **SSE Client Context Cancellation (#82):** Bound SSE client stream context to multiplexer lifetime (`m.ctx`), eliminating premature connection teardown on tool calls.
- **Semantic Memory Failure (#60, #61):** Corrected `MG_ALM_BASE` to `http://127.0.0.1:3002/api/v1` for `nova-anythingllm-mcp`, resolving agent memory amnesia across 17,000+ vector embeddings.
- **Google Stitch Restoration:** Upgraded Google Stitch to native `streamable_http` POST transport targeting `https://stitch.googleapis.com/mcp`.
