# 17 — Git tools

**Product version focus:** **0.17** ([m_0.17_git_tools.md](../../roadmaps/v1.0/m_0.17_git_tools.md))  
**Status:** done

## Intent

Expose **structured git read tools** so the agent gets status/diff/log without brittle `shell` parsing — better truncation, stable JSON/text, and safer defaults (read-only first).

Mutating git (`commit`, `push`) stays **opt-in** and heavily gated (hooks / Permission); 0.17 core is **read path**.

## Builtin tools

| Tool | Args | Behavior |
|------|------|----------|
| `git_status` | (none or pathspec?) | Porcelain-like summary: branch, staged/unstaged/untracked (capped lists) |
| `git_diff` | staged?, paths?, context? | Unified diff; hard size cap + “truncated” marker |
| `git_log` | n?, path?, author? | Oneline or short subject+hash; default n small (e.g. 20) |

Optional later (same milestone if time, else note): `git_show` (rev, path).

### Tool group

`git` — include in allow/deny. Default **on** when cwd is a git work tree; if not git → clear tool error.

## Implementation

- Prefer **go-git** or invoking `git` binary with fixed args (no shell string concat). Design choice in p_0.17.1: **subprocess `git`** is fine for MVP (ubiquitous); library optional.
- Always `GIT_TERMINAL_PROMPT=0`; no interactive credential prompts.
- Detect `.git` via cwd walk; respect path sandbox (no diff outside cwd / worktree).

## Mutating ops (explicit non-core)

```text
git_commit / git_add  — deferred unless Options.AllowGitWrite
```

When added: require Permission + hooks `beforeShell`-style or dedicated `beforeGitWrite`; never force-push in builtin.

## Relation to shell

- `shell` remains for power users (`rebase`, `bisect`).
- Prompt/skills can prefer `git_*` for status/diff.

## Security

- Read-only tools must not modify index.
- Redact tokens in diff text if matched (align redact).
- Diff/log output truncation mandatory.

## Explicit non-goals

- Full magit / IDE SCM UI.
- Hosted GitHub API (use MCP).
- Auto-commit on every run.

## Roadmap mapping

[m_0.17_git_tools.md](../../roadmaps/v1.0/m_0.17_git_tools.md) phases `p_0.17.1` … `p_0.17.3`.
