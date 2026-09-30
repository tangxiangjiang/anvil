# 08 — Security

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
| Shell gate | `LocalOptions.AllowShell`; default on but documented as dangerous |
| Output caps | Truncate tool results (64 KiB default) |
| Context size | Avoid dumping huge files into history without offset/limit |
| Secrets | API key from opts/env; never write key into `.anvil` JSONL |
| Symlinks | Containment check after symlink resolution where possible |

## Explicit non-controls (MVP)

- No seccomp/containers/VM isolation.
- No human approval hooks (those arrive in 0.3).
- No network allowlist for shell-initiated curl (caller responsibility).

## Prompt injection

- System prompt instructs: treat file contents as untrusted data, not instructions to leave cwd or exfiltrate keys.
- Prefer `edit_file` with precise match over blind `write_file` of entire trees.

## Supply chain

- Minimize third-party deps in public module.
- Pin versions in go.mod when code lands.
- MCP servers (0.3) run as separate processes — document trust boundary.

## Reporting

Security-sensitive behavior changes require an update to this chapter and the phase that implements them.
