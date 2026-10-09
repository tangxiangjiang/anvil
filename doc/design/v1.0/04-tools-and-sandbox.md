# 04 — Tools and sandbox

## Built-in tools (MVP)

| Name | Args (conceptual) | Behavior |
|------|-------------------|----------|
| `read_file` | path, offset?, limit? | Read file contents; optional line window |
| `write_file` | path, content | Create or overwrite entire file |
| `edit_file` | path, old_string, new_string | Exact string replace; fail if not unique / missing |
| `grep` | pattern, path?, glob? | Recursive search (Go regexp or ripgrep if present) |
| `glob` | pattern | List matching paths under cwd |
| `shell` | command | Run in `cwd`; capture stdout/stderr/exit |

## Registry

- Tools register name, JSON schema, and `Execute(ctx, args) (string, error)`.
- Runtime passes only schemas for enabled tools.
- `AllowShell=false` removes `shell` from the registry for that agent.

## Path sandbox

1. Resolve `cwd` to absolute path at agent create.
2. For every file path argument: join with cwd (if relative), `Clean`, then require `strings.HasPrefix(abs, cwd+sep)` (or equal).
3. Reject symlink escapes: resolve symlinks when checking containment (best effort on all platforms).
4. On violation: return tool error string to the model (do not panic); do not touch the filesystem.

## Output truncation

- Default max tool result size: **64 KiB** (configurable later).
- Truncate with a clear suffix marker so the model knows data was cut.

## Shell specifics

- Working directory = agent `cwd`.
- Inherit a minimal env (or caller-provided env map later); never log secrets.
- Kill process on context cancel (0.2 Cancel / ctx cancel).
- Do not implement a full OS sandbox (no containers in MVP); path + timeout are the controls.

## Testing requirements

- Unit tests for path escape (`../`, absolute outside cwd).
- Unit tests for `edit_file` uniqueness failure.
- Shell test with short echo command (or skip if env forbids).

## Later tools (not 0.1)

- MCP-backed tools (0.3).
- Caller-registered custom tools (0.3).
- Git-specific helpers (optional; shell can cover git for MVP).
