# Security Policy

TheNovaNodes Collective treats autonomous AI agent infrastructure security and operational integrity with the highest priority. **MCP Router** serves as the single perimeter gateway between autonomous agents and critical infrastructure services.

---

## 🔒 Supported Versions

| Version | Supported          | Runtime    | Status             |
| ------- | ------------------ | ---------- | ------------------ |
| 1.0.x   | :white_check_mark: | Go 1.22-1.24   | Production Current |

---

## 🛡️ Security Architecture & Threat Model

### 1. Ingress Perimeter Security & Timing Attack Defense
- **Constant-Time Token Comparison:** Bearer token authentication uses `crypto/subtle.ConstantTimeCompare` to resist timing attacks.
- **Header-First Authentication:** Tokens should be passed via `Authorization: Bearer <token>` HTTP headers.
- **Protected Query Parameter Fallback:** Query parameter authentication (`?token=`) is **strictly disabled by default** to prevent token leakage in proxy logs and browser history. It is only permitted if `allow_query_token: true` is explicitly configured in `auth:` settings.
- **DoS & Slowloris Mitigation:** The HTTP server configures aggressive timeouts:
  - `ReadHeaderTimeout: 5s`
  - `IdleTimeout: 120s`

### 2. Autonomous Agent PR Merge Ban (HITL Two-Phase Commit)
- AI agents are strictly prohibited from invoking `merge_pull_request` on any GitHub backend.
- Any attempt by an agent to execute PR merges triggers an immediate `SECURITY_BLOCK` audit event and returns a policy violation error. All merges require explicit human review and authorization from **ЗавЛаб**.

### 3. Hardened Multi-Parameter Protected Branch Guard
- Direct branch mutations (`push_files`, `create_or_update_file`, `delete_file`, `create_branch`, `update_pull_request_branch`) targeting protected branches (`main`, `master`, `origin/main`, `refs/heads/master`, etc.) are intercepted at the gateway level.
- **Deep Recursive Argument Inspection:** The guard inspects the argument tree across all common branch parameters (`branch`, `ref`, `target_branch`, `base`, `head`, `dest`, `branch_name`, `target`, `base_branch`, `target_ref`), while intelligently distinguishing non-branch keys (such as `file_path`, `content`, `commit_message`) to avoid false positives.

### 4. Host Secret Isolation & Environment Sanitization
- Child subprocesses (`stdio` backends) do **not** inherit the parent router's full environment.
- `ExpandedEnv()` injects only a minimal safe baseline (`PATH`, `TMPDIR`, `TEMP`, `TMP`, `LANG`, `LC_ALL`, `TZ`) merged with declared server `env:`.
- `HOME` and `USER` are **deliberately excluded** from the default baseline to prevent child processes from inspecting sensitive host files (`~/.ssh`, `~/.netrc`, `~/.aws`). If required, host environment variables must be declared explicitly via `env_inherit:`.

### 5. Granular Agent Isolation & Access Control Lists (ACL)
The router enforces strict cluster isolation:
- **Cluster 1 (Base Stack):** Public to all collective agents (Search, Memory, Context7).
- **Cluster 2 (Personal Office):** Strictly confined to `kairos_brobot` (Dynadot DNS, Mail.ru, Nextcloud, Grizzly SMS).
- **Cluster 3 (R&D Control Plane):** Decommissioned (retired in favor of direct terminal operations).
- **Cluster 4 (GitHub Contours):** Segregated between `TheNovaNodes` (Core agents + OpenClaw) and `DoctorMes` (Medical agents).
- **Cluster 5 (Google Quarantine Sandbox):** Isolated to `Tyler_Durden_gobot` (Stitch, Jules).

### 6. Zero-Noise Structured Audit Logging & Redaction
- Routine read calls are filtered out to prevent disk exhaustion.
- Mutating actions (`STATE_CHANGE`), security violations (`SECURITY_BLOCK`), and runtime errors (`TOOL_ERROR`) are recorded in structured JSON lines (`/var/log/mcp-router/audit.jsonl`).
- **Data Redaction:** Sensitive keys (passwords, tokens, keys) and sensitive string patterns (GitHub PATs, JWTs, AWS keys, Bearer tokens) are automatically redacted to `[REDACTED]` or `[REDACTED-VALUE]`.
- **Failsafe Delivery:** If primary and fallback audit log files are unwriteable, events fail-safe to `stderr` rather than discarding telemetry.
- **Logrotate Integration:** On `SIGHUP`, the audit log file descriptor is atomically closed and reopened.

---

## 🚨 Reporting a Vulnerability

If you discover a security vulnerability in `mcp-router`:
1. **Do not** create a public GitHub issue.
2. Report the vulnerability privately to **ЗавЛаб** or through the secure Telegram operational contour.
3. Include reproduction steps, environment details, and affected backends.
