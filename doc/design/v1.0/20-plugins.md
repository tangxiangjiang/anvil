# 20 — Agent plugins (worker-side)

**Product version focus:** **0.20** ([m_0.20_plugins.md](../../roadmaps/v1.0/m_0.20_plugins.md))  
**Status:** planned

## Intent

Add an **Anvil plugin** model so optional, heavy, or org-specific capabilities load on the **agent-worker** (or in-process host), not in the controller / cloud-agent Gateway / thin SDK core.

**Opinion locked in this chapter:** prefer **worker-side plugins**. The worker already owns `cwd`, secrets env, and the tool loop; language servers, indexers, and proprietary tools belong next to that process. Controllers only declare *which plugin set* a job may use.

## Why worker-side

| Concern | Worker plugin | Controller / Gateway |
|---------|---------------|----------------------|
| Path sandbox + repo files | native | remote, wrong place |
| gopls / index / GPU | local process affinity | high latency / impossible |
| Crash isolation | kill worker slot; agentd respawns | take down fleet control plane |
| Secret material | stays on node | must not pool centrally |
| Multi-tenant schedule | advertise plugin labels in heartbeat | only routes |

`agentd` remains lifecycle + routing ([11](11-worker-daemon.md)); it **loads config and passes plugin allowlists** into workers, but does not execute plugin tool code in the daemon process.

## Layering (do not collapse)

| Layer | Role | Example |
|-------|------|---------|
| **Core builtins** | Always-on minimal coding loop | read/write/edit/grep/glob/shell/patch |
| **Plugins** | Optional capability packs registering tools/hooks/providers | LSP pack, org Jira pack, local indexer |
| **MCP** | Out-of-process protocol tools | third-party servers |
| **Skills** | Prompt/playbook packs ([14](14-hooks-skills.md)) | commit-style instructions |

Plugins may **wrap** or **replace** a later builtin (e.g. ship `semantic_search` as plugin first); core stays bootable with zero plugins.

## What should be a plugin (guidance)

**Hard rule: general-purpose agent capabilities stay in the agent (core builtins / SDK).**  
Plugins are for **optional, heavy, or org-specific** packs loaded on the worker — not a dumping ground that hollows out the default agent.

**Keep in agent (core)**

- Universal coding loop: read / write / edit / patch / grep / glob / shell.
- Stable Create / Prompt / Resume / Send / Stream / Wait / Cancel / Close.
- Session store, redact, error codes, allow/deny, minimal hooks.
- **General** enhancements that every coding agent should have when enabled by options: e.g. git read tools, test/build runners, web_search, semantic_search, vision attachments — ship as **builtins** (feature-flagged / tool groups), not plugins.
- In-process SDK must remain useful **with zero plugins**.

**Prefer plugin**

- Depends on external binaries or long-lived sidecars (language servers, browsers).
- Large native deps, optional license, or rare install footprint.
- Org- / product-specific tools (internal APIs, proprietary workflows).
- Experimental packs that must not bloat the default `agent-worker` binary.
- **Development memory** (cross-run fact store) — first-party plugin, not core; see [21-dev-memory.md](21-dev-memory.md).

**Keep MCP** when the capability is already an MCP server you do not want to reimplement.

**Anti-pattern:** moving grep/shell/git_status into plugins “for modularity.” That forces every host to assemble a basic agent from packs — rejected.

## Plugin shape

```text
anvil-plugin-<id>/
  plugin.json          # id, version, anvil_api, capabilities[], permissions[]
  # delivery one of:
  main.go → shared library / Go plugin  OR
  exec host binary speaking Plugin RPC   OR
  in-tree module registered at worker link time (first MVP)
```

### MVP delivery (0.20)

**In-tree / link-time packs** + **exec RPC plugins**:

1. **In-tree** (`source/plugins/...` or build tags): compile into `agent-worker` / `anvil` CLI — simplest, typed `Plugin` interface.
2. **Exec**: separate binary, JSON-RPC or gRPC over stdio — crash isolation, any language; worker supervises like MCP stdio.

Go `plugin` .so is **non-goal** on Windows (fragile); do not depend on it.

### Go interface (sketch)

```go
type Plugin interface {
    ID() string
    Init(ctx context.Context, host Host) error
    Tools() []tools.Tool          // or register via host.RegisterTool
    Close(ctx context.Context) error
}

type Host interface {
    Cwd() string
    RegisterTool(tools.Tool) error
    RegisterHook(...) error       // optional
    Logger() ...
    // no raw unrestricted shell helper that bypasses sandbox
}
```

### plugin.json

```json
{
  "id": "lsp",
  "version": "0.1.0",
  "anvil_api": 1,
  "capabilities": ["tools.lsp"],
  "permissions": ["fs.read", "fs.write", "net.none", "process.spawn"],
  "command": "anvil-plugin-lsp",
  "args": []
}
```

Worker refuses plugins whose `permissions` exceed job policy / daemon allowlist.

## Load path

```text
job.assign { plugins: ["lsp", "acme.tools"] }
        │
        ▼
agent-worker
  1. resolve plugin ids from worker config + job allowlist ∩ daemon policy
  2. Init each plugin with Host bound to cwd
  3. anvil.Create(... Options.Tools from plugins ...)
  4. run job; on done Close plugins (or keep warm if worker reused)
```

- **Daemon config:** `plugins.available[]`, search paths, global deny.
- **Job:** optional subset; empty → default set from worker config.
- **In-process SDK:** `Options.Plugins` same ids for parity when not using agentd.

Heartbeat may advertise `capabilities: ["tools.lsp", …]` so schedulers place jobs on nodes that have the plugin.

## Security

- Plugins are **trusted like MCP stdio you chose to run** — not arbitrary download from the internet in 0.20 (no marketplace).
- Path sandbox enforced by Host wrappers; plugin file IO should go through Host when possible.
- `process.spawn` / `net.*` permissions explicit; default deny net for plugins.
- Sign/checksum optional later; 0.20: local path allowlist only.

## Relation to milestones 0.16–0.19

| Capability | Stay in agent (builtin) | Plugin candidate |
|------------|-------------------------|------------------|
| git / test-build / web / retrieval / vision | **Yes** — general agent ability | Only thin org wrappers |
| LSP (gopls, etc.) | Thin client + tools may be builtin | Full server supervision packs / extra languages as plugins OK |
| Skills / hooks files | Core | — |
| Acme internal tools | — | **Yes** |

Tool **names** stay stable whether a rare pack is later extracted; default agent never requires plugins for the general loop.

## Explicit non-goals

- Public plugin marketplace / auto-update CDN.
- Running plugin code inside `agentd` or Gateway.
- Cursor IDE extension API compatibility.
- Hot-reload every plugin mid-run without Reload semantics (use Agent.Reload / worker recycle).

## Roadmap mapping

[m_0.20_plugins.md](../../roadmaps/v1.0/m_0.20_plugins.md) phases `p_0.20.1` … `p_0.20.4`.
