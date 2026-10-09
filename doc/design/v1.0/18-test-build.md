# 18 — Test & build tools

**Product version focus:** **0.18** ([m_0.18_test_build.md](../../roadmaps/v1.0/m_0.18_test_build.md))  
**Status:** planned

## Intent

Give the agent **structured test/build invocation** with exit code, failed case names, and truncated logs — instead of free-form `shell` that often overflows context or misses failures.

## Builtin tools

| Tool | Args (conceptual) | Behavior |
|------|-------------------|----------|
| `run_tests` | selector?, path?, extra_args? | Run project test command; return `{ ok, exitCode, summary, failed[], logTail }` |
| `build` | target?, extra_args? | Run build/compile; return `{ ok, exitCode, errors[], logTail }` |

### Discovery

Resolution order:

1. `Options.Test.Command` / `Options.Build.Command` (explicit).
2. `.anvil/project.json` or `package` hints: `go test`, `npm test`, `cargo test`, `pytest`, etc.
3. If unknown → tool error with “configure Test.Command”.

Do **not** invent unbounded `shell` under the hood with user-controlled full command strings in MVP without allowlist; prefer argv arrays in config.

### Tool group

`verify` (or `test` + `build` subgroups). Default on when commands configured; else off.

## Execution

- Working directory = agent cwd (or configured package dir under sandbox).
- Timeouts: `Test.Timeout` / `Build.Timeout` (defaults e.g. 10m / 5m); cancel on ctx.
- Capture stdout/stderr; cap total bytes; keep **tail** + parse fail lines when possible.
- Parsers (best-effort): go test `-json`, pytest, cargo, generic “FAILED”/error regex — unknown → logTail only.

## Relation to LSP / git

- After edits: agent may `diagnostics` ([16](16-lsp.md)) then `run_tests`.
- `git_diff` ([17](17-git-tools.md)) to see what under test.

## Security

- Same as shell: no secret logging; Permission hook may gate `run_tests`/`build` if desired.
- Command from **config only**, not from model-supplied arbitrary shell (model may pass **selector** / path args that are validated: no `;`, no `$(` ).

## Explicit non-goals

- Full CI matrix / remote runners.
- Flake retry product (optional simple retry count later).
- Replacing all shell usage.

## Roadmap mapping

[m_0.18_test_build.md](../../roadmaps/v1.0/m_0.18_test_build.md) phases `p_0.18.1` … `p_0.18.3`.
