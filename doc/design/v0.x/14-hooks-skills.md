# 14 — Hooks & skills (deepen)

**Product version focus:** **0.14** ([m_0.14_hooks_skills.md](../../roadmaps/v1.0/m_0.14_hooks_skills.md))  
**Status:** done

## Intent

Deepen **policy** and **workflow context** beyond 0.10’s minimal hooks (`beforeShell` / `beforeWrite` / `beforeEdit`):

- **Hooks** — more lifecycle gates and optional command hooks for operators.
- **Skills** — load packaged instruction/tool playbooks (Agent Skill style) into the agent context when relevant.

This is **not** full Cursor hooks catalog parity or the plugins marketplace. Stay file-based, local, and additive to [09](09-extensibility.md) / [10](10-sdk-parity.md).

## Hooks (deepen)

### Current (0.10)

```json
{
  "version": 1,
  "beforeShell": [{"denyContains": "rm -rf", "message": "blocked"}],
  "beforeWrite": [{"denyContains": "/etc/", "message": "blocked"}],
  "beforeEdit": []
}
```

Deny → tool error string to the model.

### 0.14 additions

| Hook | When | Effect |
|------|------|--------|
| `beforeMCP` | Before MCP tool invoke | deny / allow |
| `beforeRead` | Before `read_file` (optional, default off for noise) | deny paths |
| `afterTool` | After any tool (observability) | annotate log / optional rewrite result (strictly bounded) |
| `sessionStart` | After Create / Resume build | inject extra system notes (size-capped) |
| `sessionEnd` | On Close / successful Wait terminal | fire-and-forget shell or no-op |

**Matcher fields** (reuse 0.10 style): `denyContains`, `denyPattern`, optional `denyTool`, `message`.

**Optional command hooks** (subset):

```json
{
  "beforeShell": [
    {
      "command": ".anvil/hooks/check-shell.sh",
      "timeoutMs": 3000,
      "message": "hook rejected"
    }
  ]
}
```

- Exit 0 → allow; non-zero → deny with `message` or stderr snippet (redacted, truncated).
- No network assumed; cwd = agent cwd; env scrubbed.
- Disable command hooks unless `Options.AllowHookCommands` or settingSources policy says on (default **off** for safety).

### Loading

- Paths: `.anvil/hooks.json`, `.cursor/hooks.json` (existing precedence).
- `Agent.Reload` continues to refresh hooks (0.10).
- Invalid JSON → create/reload error (fail closed for that file).

### Non-goals (hooks)

- Full Cursor event names parity (`beforeSubmitPrompt`, MDM team hooks, …) unless listed above.
- Interactive UI approval prompts.
- Remote hook bundles.

## Skills

### Model

A **skill** is a directory:

```text
.anvil/skills/<skill-id>/
  SKILL.md          # required: YAML front matter + markdown body
  scripts/          # optional helper scripts (not auto-executed)
  references/       # optional docs for the model to read via tools
```

Also discover (when enabled): `.cursor/skills/`, project path from config, user `~/.anvil/skills/`.

### Front matter (minimal)

```yaml
---
name: commit-style
description: Conventional commits and repo changelog rules
# optional:
# globs: ["**/*.go", "CHANGELOG*"]
# disable-model-invocation: false
---
```

- `description` is what the **router** uses to decide inclusion.
- Body is instruction text appended or exposed via a `skill` read tool.

### Activation

| Mode | Behavior |
|------|----------|
| **Always** (opt-in list) | `Options.Skills` or `skills.json` `always: [id]` |
| **Router** (default for discovered) | Before run (or when user prompt matches), select top-k skills by description keyword / light scoring; inject capped markdown into system or first user turn |
| **On-demand tool** | Builtin `read_skill` / `list_skills` so the model pulls body when needed |

0.14 ships **list + read_skill tools** and **always** injection; router heuristic can be simple keyword overlap (no extra LLM call required in MVP of 0.14).

### settingSources

- Extend sources or document: with `project` / `user`, also scan skills directories (alongside mcp/rules).
- Inline `Options.SkillDirs []string` always available.

### Security

- Skills are **untrusted content** like rules: size caps, no automatic execution of `scripts/` unless user runs via shell (still gated by hooks).
- Path sandbox for `references/` reads.

## Relation to Permission API

- Existing `PermissionFunc` (0.3) remains the programmatic gate.
- File hooks run **before** or **as part of** permission checks. Document order since 0.36: hooks deny → rule deny → rule allow → session allowlist → Permission → execute ([36](36-tool-permissions.md)).
- Since 0.36 the shell / write / edit denies are evaluated in `preExec`, not inside the composed permission callback, so a rule `allow` (which never reaches the callback) cannot skip them. Deny semantics are unchanged; the read / MCP denies were already at that stage.

## Explicit non-goals

- Cursor plugins marketplace / team MDM hook distribution.
- Nested subagent skills that spawn agents (use [11](11-worker-daemon.md) for process workers).
- Guaranteed identical behavior to Cursor Skills UI.

## Roadmap mapping

[m_0.14_hooks_skills.md](../../roadmaps/v1.0/m_0.14_hooks_skills.md) phases `p_0.14.1` … `p_0.14.4`.
