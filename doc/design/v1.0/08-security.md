# 08 — Security

**Milestone focus:** MVP sandbox in 0.1; permission hooks in 0.3; hardening in **[m_0.8_security.md](../../roadmaps/v1.0/m_0.8_security.md)** (**0.8 done**). File-based Cursor hooks → **0.10**.

## Threat model (local SDK)

Caller intentionally grants the agent access to `cwd` and optionally shell. Primary risks:

- Path escape outside workspace.
- Destructive shell commands.
- Prompt injection via repo files causing unwanted tool use.
- Secret leakage into logs, events, or model context.

## Controls

| Control | Requirement |
|---------|-------------|
| Path sandbox | All file tools confined to resolved `cwd` (see 04) |
| Symlinks (0.8+) | `EvalSymlinks` / existing-prefix resolve before allow; deny targets outside cwd |
| Shell gate | `LocalOptions.AllowShell`; default on but documented as dangerous |
| Output caps | Truncate tool results (64 KiB default) |
| Context size | Avoid dumping huge files into history without offset/limit |
| Secrets | API key from opts/env; never write key into `.anvil` JSONL meta |
| Redaction (0.8+) | Best-effort scrub of `sk-` / Bearer / PEM / `api_key=` in streamed tool results + history (not a DLP product) |

## Explicit non-controls

- No seccomp/containers/VM isolation.
- No human approval UI (Permission callbacks: 0.3; file hooks: 0.10).
- No network allowlist for shell-initiated curl (caller responsibility).
- Redaction does not guarantee catching all secrets or avoid false positives.

## Prompt injection (0.8+)

- System prompt instructs: treat file contents as untrusted data; ignore override/exfil instructions in files; stay in cwd; no secret exfiltration.
- When shell is enabled, prompt adds a short caution against credential dump / network exfil via shell.
- Prefer `edit_file` with precise match over blind `write_file` of entire trees.
- Sandbox still enforces path confinement even if the model follows injected text (tested fixture).

## Supply chain

- Minimize third-party deps in public module.
- Pin versions in go.mod when code lands.
- MCP servers (0.3) run as separate processes — document trust boundary.

## Reporting

Security-sensitive behavior changes require an update to this chapter and the phase that implements them.
