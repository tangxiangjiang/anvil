# Anvil

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

Independent **local-only** Go Agent SDK. Capability-aligned with Cursor’s local agent API shape (`Create` / `Prompt` / `Send` / `Stream` / `Wait`), but runs your own tool loop against **OpenAI-compatible** or **Anthropic** HTTP APIs — not the Cursor runtime.

The repository also carries an **automated coding tool-chain** (scheduler, worker daemon, web console, mobile app) and, from 0.35, a **VS Code extension**. Those are separate surfaces that consume the SDK; they do not depend on each other.

## Layout

| Path | Purpose |
|------|---------|
| [`doc/design/`](doc/design/) | Versioned design (current: [`v0.x/`](doc/design/v0.x/)) |
| [`doc/roadmaps/`](doc/roadmaps/) | Versioned milestones (current: [`v0.x/`](doc/roadmaps/v0.x/)) |
| [`source/`](source/) | Go module (`github.com/anvil-dev/anvil`) |
| [`editors/vscode/`](editors/vscode/) | VS Code extension + `anvil-host` sidecar (0.35) |
| [`app/`](app/) | Flutter mobile client (login via web → scheduler jobs) |

## Quick start

```bash
cd source
go test ./...

export ANVIL_API_KEY=...
export ANVIL_BASE_URL=https://your-gateway.example.com/v1   # OpenAI-compatible
export ANVIL_MODEL=private/deepseek-v4-pro
export ANVIL_PROVIDER=openai   # or anthropic

# CLI (0.6) — preferred for smoke / operator use
go build -o ../bin/anvil.exe ./cmd/anvil
../bin/anvil.exe prompt /path/to/repo "Fix the failing test"
../bin/anvil.exe list /path/to/repo
../bin/anvil.exe chat --resume agent-<id> /path/to/repo

# Thin examples (still available)
go run ./examples/oneshot /path/to/repo "Fix the failing test"
go run ./examples/interactive /path/to/repo
```

```go
package main

import (
	"context"
	"github.com/anvil-dev/anvil"
)

func main() {
	result, err := anvil.Prompt(context.Background(), "Explain this repo", anvil.Options{
		Provider: anvil.ProviderOpenAI,
		APIKey:   "...",
		BaseURL:  "https://api.openai.com/v1",
		Model:    anvil.Model{ID: "gpt-4.1"},
		Local:    anvil.LocalOptions{CWD: "."},
	})
	_ = result
	_ = err
}
```

## Status

Product is at **0.46 delivered** — the editor line is finished for this design cutoff. 0.46 is a polish line that adds no engine: a configured language server and test/build commands are wired through, every message can rewind, a plan opens as a read-only document, the transcript has a focus view, formula rendering and `/export`, a full ask → approve → finish cycle works from the keyboard with live regions, permission rules gain a user scope and protected paths, each window names the one root it works in, and the VSIX, the host and the docs agree on 0.47.0. 0.35–0.38 are the editor line (VS Code extension, tool permissions, permission modes, session identity); **0.39** is the Delivery execute/test campaign loop, **0.40** is the editor alignment, **0.41** is auto mode and the permissions manager, **0.42** is subagents, background tasks and workflows, **0.43** is context management, **0.44** is MCP management, and **0.46** is editor polish. Full per-milestone index: [`doc/roadmaps/v0.x/README.md`](doc/roadmaps/v0.x/README.md).

| Era | Milestones | What |
|-----|-----------|------|
| Core SDK | 0.1 – 0.10 | Create / Prompt / Resume, streaming & stable error codes, security hardening, tools & sandbox, MCP, Cursor SDK **local** parity |
| Agent capabilities | 0.11 – 0.22 | worker daemon, model gateway, retrieval, hooks & skills, web search, LSP, git tools, test & build, vision, plugins, dev memory, project search |
| Automated coding tool-chain | 0.23 – 0.34 | YAML ops config, scheduler hub, web fleet console, mobile app, app fleets, clusters/workers, sandbox workers, loop guard, job event stream, sandbox code review, delivery UI, workspace domain |
| Editor client | 0.35 | VS Code extension + `anvil-host` sidecar, packaged as per-platform VSIX — [design 35](doc/design/v0.x/35-vscode-extension.vscode.md) |
| Tool permissions | 0.36 | `.anvil/permissions.json` rule set, a shell analyzer that proves read-only, the loading switch that makes `.anvil/hooks.json` real, and an approval card that remembers the pattern rather than the tool — [design 36](doc/design/v0.x/36-tool-permissions.vscode.md) |
| Permission modes | 0.37 | A complete mode ladder — `dont_ask` runs only what the rules allow and `bypass` no longer overrules a written `ask` rule — plus `"defaultMode"` in the same workspace file, resolved by the host and reported to the composer — [design 37](doc/design/v0.x/37-permission-modes.vscode.md) |
| Session identity & replay | 0.38 | A session named by its first message, a reviewable change on every tool card, and a transcript that survives a reload — [design 38](doc/design/v0.x/38-session-identity-and-replay.vscode.md) |
| Editor alignment | 0.40 | Parallel tabs, rename/delete/search, per-hunk acceptance, checkpoints, fork, MCP and web tools switched on, live usage, a task list — [design 40](doc/design/v0.x/40-editor-alignment.vscode.md) |
| Auto & rules manager | 0.41 | An `auto` rung that runs what the analyzer can prove and asks about the rest, and a structured manager for `.anvil/permissions.json` — [design 41](doc/design/v0.x/41-permission-auto-and-manager.vscode.md) |
| Subagents & workflows | 0.42 | `spawn_agent` creates a child agent with inherited permissions, background shell and subagents share one task table, `/tasks` draws it in the editor, and `run_workflow` evaluates a declarative DAG — [design 42](doc/design/v0.x/42-subagents-and-tasks.vscode.md) |
| Context management | 0.43 | A declared token budget with a safe cut, auto-compaction at run start and mid-run, `Agent.Compact` and the editor's `/compact`, cached-token accounting, and a meter that counts context left and the cache's remaining life — [design 43](doc/design/v0.x/43-context-management.vscode.md) |
| MCP management | 0.44 | A host-owned MCP runtime with per-server state and failure isolation, atomic edits to `.cursor/mcp.json` that preserve unknown fields, reconnect, Streamable HTTP, host-owned OAuth tokens, a manager panel and `/mcp` — [design 44](doc/design/v0.x/44-mcp-management.vscode.md) |
| Editor polish | 0.46 | LSP and test/build wiring, "rewind to here", a plan as a document, focus view with formulas and `/export`, keyboard and live-region accessibility, user-scope rules with protected paths, one root per window, and one version across the VSIX, the host and the docs — [design 46](doc/design/v0.x/46-editor-polish.vscode.md) |

### VS Code extension (0.35)

[`editors/vscode/`](editors/vscode/) is the editor surface: the conversation is
an **editor tab** that streams a turn, shows every tool call as a card, and stops
for approval before anything writes to the workspace, with a composer that
floats over the transcript; the **activity bar** lists this workspace's past
sessions, one click to continue. It holds no agent logic — it
spawns the **`anvil-host`** sidecar ([`source/cmd/anvil-host`](source/cmd/anvil-host/),
a stdio JSON-RPC program built from the public SDK) and renders what that process
reports. The Go side imports the public `anvil` package only, which is enforced
by `internal/editorhost/independence_test.go`.

```bash
cd editors/vscode
npm install
npm run package:vsix          # this platform; --target <id> / --all for the rest
code --install-extension anvil-vscode-0.47.0-win32-x64.vsix
```

A platform VSIX carries the matching `anvil-host`, cross-compiled with
`CGO_ENABLED=0` and verified against the digest recorded at build time before it
runs, so it installs on a machine with no Go toolchain. The API key never
enters the package: it lives in VS Code SecretStorage and reaches the child only
through its environment (`ANVIL_API_KEY`). See
[`editors/vscode/README.md`](editors/vscode/README.md) for the settings,
permission modes, and troubleshooting.

### Inspect agents (0.5)

```go
infos, _ := anvil.List(ctx, anvil.ListOptions{Local: anvil.LocalOptions{CWD: "."}})
info, _ := anvil.Get(ctx, "agent-…", anvil.ListOptions{Local: anvil.LocalOptions{CWD: "."}})
```

Offline eval: `cd source && go test ./eval`

### CLI notes (0.6 / 0.7)

- Streams `progress` / assistant / tool events; default MaxSteps is **128**.
- After `Send`, prints `agent=… run=…` (`FormatRunStart`); `RunResult.Code` distinguishes `max_steps` / `provider` / `cancelled` / `max_wall_time` / `context_deadline`.
- `--max-wall` / `ANVIL_MAX_WALL` sets `Options.MaxWallTime` (separate from the CLI’s 30m host context deadline).
- Exit codes: **1** startup/config, **2** run `Status=error`.
- `--store sqlite` uses `{cwd}/.anvil/agents.db` (default `jsonl`).
- Optional `{cwd}/.anvil/config` JSON: `provider`, `model`, `base_url`, `max_steps`, `store` (prefer `ANVIL_API_KEY` in env).

### Project rules (0.4)

```go
Local: anvil.LocalOptions{
  CWD:       ".",
  LoadRules: true, // AGENTS.md + .anvil/rules/*.md
}
```

### Tool permissions (0.36)

A workspace can write down what the agent may do, in `.anvil/permissions.json` — and the
rules hold whether or not a client is there to ask, `bypass` included. See
[design 36](doc/design/v0.x/36-tool-permissions.vscode.md).

```json
{
  "version": 1,
  "rules": [
    { "effect": "allow", "tool": "shell", "pattern": "readonly", "reason": "read-only commands" },
    { "effect": "allow", "tool": "shell", "pattern": "git status:*" },
    { "effect": "deny",  "tool": "shell", "pattern": "rm -rf:*", "reason": "destructive" },
    { "effect": "allow", "tool": "write_file", "pattern": "src/**/*.go" },
    { "effect": "ask",   "tool": "apply_patch", "pattern": "**/*.md" }
  ]
}
```

Across effects **`deny` > `ask` > `allow`** — one matching deny defeats every allow; within
one effect the first rule in the file wins. `pattern` is read per call, not per tool:
`readonly` means the analyzer proved the whole command line non-mutating, otherwise an exact
command or a `prefix:*` compared token by token (so `git status:*` does not admit
`git status && rm -rf /`), and on a call that touches files it is a path glob that has to
cover **every** path the call touches. A missing file means no rules; a malformed one fails
session creation, naming the line.

In the editor, the approval card offers the rule shapes that would decide **this** call — the
exact command, the leading tokens, anything read-only, the one file — and writes the choice
into the same file; the call then runs without a second prompt, because the host re-reads the
file and answers it. A tool card that ran unprompted says `allowed by rule 2: shell
"git status:*"`. **Anvil: Open Permission Rules** opens the manager — effects, tools, patterns,
reasons and the workspace's starting mode as a form — and **Anvil: Open Permission Rules File**
opens or creates the JSON itself; a file that will not compile is reported where it is edited —
with a diagnostic on the line — and the manager goes read-only rather than overwriting it.
`anvil.permissions` turns the whole thing off for a workspace.

The library and the CLI read the files only when asked to, and the editor host and CLI
default both switches on:

```go
Local: anvil.LocalOptions{
  CWD:             ".",
  LoadPermissions: true, // .anvil/permissions.json
  LoadHooks:       true, // .anvil/hooks.json — no client loaded this before 0.36
}
// Rules can also be installed from code; they come after the file's, so the
// numbers a rule error names still point at the file.
Options.PermissionRules: []anvil.PermissionRule{
  {Effect: "deny", Tool: "shell", Pattern: "rm -rf:*", Reason: "destructive"},
}
```

### Permission modes (0.37)

A mode decides only **whether to ask** — never what the rules mean. Five of them:
`ask`, `accept_edits`, `plan`, `dont_ask`, `bypass`. See
[design 37](doc/design/v0.x/37-permission-modes.vscode.md).

- **`dont_ask`** is for a run with nobody watching: every gated call the workspace's rules do
  not already allow is refused, with the reason, and no prompt is ever raised. An `allow` rule
  still runs the call, and a `deny` still refuses.
- **A written `ask` rule outranks `bypass`.** A blanket "do not ask" is not an answer about
  `git push:*` in particular, so the rule wins and the card says which rule it was.
- **A workspace can declare the mode its sessions start in**, in the same file:

```json
{ "version": 1, "defaultMode": "accept_edits", "rules": [] }
```

A client that sends no mode gets the declared one; one that sends a mode gets what it asked
for. `"defaultMode": "bypass"` is refused — a repository is not a person — so the session
starts in `ask` and both the session result and `permissions.get` carry a sentence saying so.
`load_permissions: false` ignores the declaration along with the rules. Writing a rule from
the approval card leaves the declaration alone.

### SDK local parity (0.10)

Surface alignment with Cursor SDK *local* knobs (not Cursor runtime quality). See [`doc/design/v0.x/10-sdk-parity.md`](doc/design/v0.x/10-sdk-parity.md).

```go
Local: anvil.LocalOptions{
  CWD:            ".",
  SettingSources: []anvil.SettingSource{anvil.SettingSourceProject}, // .cursor/mcp.json + hooks
}
// Options.SystemPrompt, BuiltinTools / DisallowedTools
// Send(ctx, msg, anvil.WithForce()); agent.Reload(ctx); anvil.ListRuns(...)
// anvil.Prewarm(ctx, cwd, sources)
```

### Model gateway (0.12)

OpenAI-compatible catalog + auth + proxy (`cmd/anvil-gateway`). Point Anvil at the gateway:

```bash
cd source
go build -o ../bin/anvil-gateway.exe ./cmd/anvil-gateway
# edit cmd/anvil-gateway/gateway.example.yaml (or .json) — upstreams + models
set ANVIL_GATEWAY_TOKEN=gw-token
set UPSTREAM_OPENAI_KEY=sk-...
../bin/anvil-gateway.exe --config cmd/anvil-gateway/gateway.example.yaml

set ANVIL_BASE_URL=http://127.0.0.1:8080/v1
set ANVIL_API_KEY=gw-token
set ANVIL_PROVIDER=openai
set ANVIL_MODEL=private/demo
../bin/anvil.exe models
../bin/anvil.exe prompt /path/to/repo "hello"
```

`anvil.ListModels` hits `GET {BaseURL}/models`. Config is JSON or YAML (0.23). See [`doc/design/v0.x/12-model-gateway.md`](doc/design/v0.x/12-model-gateway.md).

### Plugins (0.20)

Optional capability packs on the worker / in-process SDK (not inside `agentd`). General coding tools stay builtins; plugins are for optional/heavy/org-specific tools.

- In-tree sample: `Options.Plugins: []string{"sample"}` registers `plugin_ping`.
- Exec plugins: put `<id>/plugin.json` under `Options.PluginDirs` (or agentd `plugins.search_paths`) with a `command` speaking stdio JSON-RPC (`initialize` / `list_tools` / `call_tool` / `shutdown`).
- Daemon policy: `plugins.available` / `plugins.deny`; `job.assign.plugins` is intersected — denied ids never Init. `hello_ok` / cluster heartbeat advertise `plugin.<id>` capabilities.

```go
Plugins: []string{"sample"},
// PluginDirs: []string{"/path/to/plugin-roots"},
// PluginPolicy: &anvil.PluginPolicy{Deny: []string{"untrusted"}},
```

Crash of an exec plugin fails that tool call / job path; it does not take down agentd (worker/embedded runner reports the error). See [`doc/design/v0.x/20-plugins.md`](doc/design/v0.x/20-plugins.md).

### Development memory (0.21)

First-party plugin `memory` (not core builtins). Durable notes under `{cwd}/.anvil/memory/` that survive new agent ids on the same cwd — distinct from session Resume (transcript) and rules/skills (static instructions).

```go
Plugins: []string{"memory"},
// optional vector boost for memory_search when EmbedBaseURL + EmbedModel are set
```

Tools: `memory_write` / `memory_read` / `memory_search` / `memory_list` / `memory_delete`. Writes redact secret-like strings (reject private keys). Without the plugin, memory tools are absent; Resume still works. See [`doc/design/v0.x/21-dev-memory.md`](doc/design/v0.x/21-dev-memory.md).

### Vision / images (0.19)

Opt-in multimodal attachments (off by default in the SDK; the VS Code extension asks for it unless `anvil.vision` is false). Set `Options.Vision.Enabled` and pass images via `WithAttachments` / `Options.Attachments` (path under cwd or in-memory bytes). Optional builtin `read_image` (tool group `vision`) queues a sandbox file for the next Completer turn. Images are downscaled (default longest side 2048, ~4MiB encoded budget). Gateway catalog may advertise `anvil.vision: true` on models.

```go
Vision: anvil.VisionOptions{Enabled: true},
// Prompt(..., Options{..., Attachments: []anvil.ImageAttachment{{Path: "ui.png"}}})
// or agent.Send(ctx, "look", anvil.WithAttachments(anvil.ImageAttachment{Path: "ui.png"}))
// DisallowedTools: []string{"vision"},
```

See [`doc/design/v0.x/19-vision.md`](doc/design/v0.x/19-vision.md).

### Test & build (0.18)

`run_tests` / `build` register only when configured via `Options.Test` / `Options.Build` or `.anvil/project.json`. Tool group `verify`. Model may pass `selector` / `path` / `extra_args` tokens; shell metacharacters are rejected. Commands are argv arrays (no shell string).

```go
Test:  anvil.TestOptions{Command: []string{"go", "test", "./..."}},
Build: anvil.BuildOptions{Command: []string{"go", "build", "./..."}},
// example: source/examples/project.example.json → copy to .anvil/project.json
```

See [`doc/design/v0.x/18-test-build.md`](doc/design/v0.x/18-test-build.md).

### Git tools (0.17)

Read-only builtins `git_status`, `git_diff`, `git_log` (tool group `git`). Invokes the `git` binary with fixed argv (`GIT_TERMINAL_PROMPT=0`); non-git cwd returns a clear error. Diffs are size-capped and redacted.

```go
// DisallowedTools: []string{"git"},
```

See [`doc/design/v0.x/17-git-tools.md`](doc/design/v0.x/17-git-tools.md).

### LSP (0.16)

Language-server tools (`diagnostics`, `definition`, `references`, `hover`, `rename_symbol`) when servers are configured. Tool group `lsp`. Missing binaries → tool error (Create still ok unless `LSP.Required`).

```go
LSP: anvil.LSPOptions{
  Servers: []anvil.LSPServerConfig{{
    Language: "go", Pattern: "**/*.go", Command: "gopls",
  }},
},
// or project file .anvil/lsp.json with SettingSources: project
// example: source/examples/lsp.example.json
```

Writes via `write_file` / `edit_file` / `apply_patch` sync `didChange` to open LSP docs. See [`doc/design/v0.x/16-lsp.md`](doc/design/v0.x/16-lsp.md).

### Web search (0.15)

Opt-in egress tools (off by default). Configure `Options.WebSearch` with an HTTP JSON provider; tool group `web` covers `web_search` / `fetch_url`.

```go
WebSearch: anvil.WebSearchOptions{
  Enabled:       true,
  BaseURL:       "https://search.example.com/v1/web/search", // or gateway proxy
  APIKeyEnv:     "ANVIL_WEB_SEARCH_KEY", // Bearer; or set APIKey
  AllowFetchURL: true,  // optional fetch_url (SSRF-guarded; private IPs blocked by default)
},
// DisallowedTools: []string{"web"},
```

Request/response shape and MCP alternative: [`doc/design/v0.x/15-web-search.md`](doc/design/v0.x/15-web-search.md).

### Hooks & skills (0.14)

Extended `.anvil/hooks.json` / `.cursor/hooks.json`: `beforeMCP`, `beforeRead`, `afterTool`, `sessionStart` / `sessionEnd`. Optional `command` hooks require `Options.AllowHookCommands` (default off).

Skills live under `.anvil/skills/<id>/SKILL.md` (also `.cursor/skills/`, `Options.SkillDirs`, user `~/.anvil/skills` with `SettingSourceUser`). Builtins `list_skills` / `read_skill` (group `skills`). `Options.Skills` or `.anvil/skills.json` `always` inject into the system prompt; keyword router adds matched skills per `Send`.

```go
Local: anvil.LocalOptions{
  CWD:            ".",
  SettingSources: []anvil.SettingSource{anvil.SettingSourceProject},
},
Skills:             []string{"commit-style"},
AllowHookCommands:  false, // set true only if you trust hooks.json command entries
```

See [`doc/design/v0.x/14-hooks-skills.md`](doc/design/v0.x/14-hooks-skills.md).

### Project search engine (0.22) / semantic_search (0.13)

Local engine at `{cwd}/.anvil/index/search.db`: SQLite tables + **FTS5** lexical + optional **BLOB** embeddings (brute-force cosine). Builtin `semantic_search` (tool group `search`) uses hybrid when vectors exist, else FTS-only. `grep` remains independent.

```bash
cd source
go build -o ../bin/anvil.exe ./cmd/anvil
../bin/anvil.exe index build /path/to/repo
../bin/anvil.exe index status /path/to/repo
../bin/anvil.exe index clear /path/to/repo
# optional hybrid embeddings (OpenAI-compatible POST …/embeddings):
../bin/anvil.exe index build --embed-url http://127.0.0.1:8080/v1 --embed-model text-embedding-3-small /path/to/repo
```

Incremental builds skip unchanged file hashes; embed-model changes clear vectors and re-embed. `Local.EnsureIndex` builds a lexical index on Create when `search.db` is missing.

See [`doc/design/v0.x/22-project-search.md`](doc/design/v0.x/22-project-search.md) (engine) and [`doc/design/v0.x/13-retrieval.md`](doc/design/v0.x/13-retrieval.md) (tool).

### Worker daemon (0.11)

`agentd` accepts local controller connections (unix socket or `tcp://127.0.0.1:…`), allowlists repos, and runs jobs via an embedded Anvil worker. Ops config is JSON or YAML (0.23); put LLM/gateway defaults under `llm:` (injected into workers — agentd does not proxy chat). Env / `job.assign` may override.

```bash
cd source
go build -o ../bin/agentd.exe ./cmd/agentd
# edit cmd/agentd/agentd.example.yaml → absolute repos[].cwd + llm.base_url
set ANVIL_AGENTD_TOKEN=dev-token
set ANVIL_API_KEY=gw-token
../bin/agentd.exe --config cmd/agentd/agentd.example.yaml
```

Protocol: length-prefixed JSON (`hello`, `worker.list`, `job.assign` / `job.event` / `job.cancel` / `job.done`). See [`doc/design/v0.x/11-worker-daemon.md`](doc/design/v0.x/11-worker-daemon.md) and [`doc/design/v0.x/23-yaml-config.md`](doc/design/v0.x/23-yaml-config.md).

### Scheduler hub (0.24)

`anvil-scheduler` is the app-facing control plane: configure gateway credentials (`PUT /v1/llm`), list nodes/models, submit jobs. agentd joins `listen_cluster`; scheduler pushes `base_url` / `api_key` / `model` on `join_ok` and live `cluster.config`. Chat still goes worker → gateway (scheduler does not proxy).

App HTTP accepts the static `ANVIL_SCHEDULER_TOKEN` **or** a valid anvil-web session Bearer when `web_sessions_db` points at the same SQLite file as web `db_path`. Cluster join still requires the static token only.

```bash
cd source
go build -o ../bin/anvil-scheduler.exe ./cmd/anvil-scheduler
go build -o ../bin/agentd.exe ./cmd/agentd
# edit scheduler.example.yaml; set web_sessions_db to the same path as web db_path
# enable agentd cluster.url → listen_cluster
set ANVIL_SCHEDULER_TOKEN=sched-token
set ANVIL_GATEWAY_TOKEN=gw-token
../bin/anvil-scheduler.exe --config cmd/anvil-scheduler/scheduler.example.yaml
# agentd: cluster.enabled=true, token_env=ANVIL_SCHEDULER_TOKEN
../bin/agentd.exe --config cmd/agentd/agentd.example.yaml

curl -s -H "Authorization: Bearer sched-token" http://127.0.0.1:17800/v1/nodes
curl -s -H "Authorization: Bearer sched-token" -H "Content-Type: application/json" ^
  -X PUT http://127.0.0.1:17800/v1/llm -d "{\"base_url\":\"http://127.0.0.1:8080/v1\",\"api_key\":\"gw-token\",\"model\":\"private/demo\"}"
curl -s -H "Authorization: Bearer sched-token" http://127.0.0.1:17800/v1/models
curl -s -H "Authorization: Bearer sched-token" -H "Content-Type: application/json" ^
  -X POST http://127.0.0.1:17800/v1/jobs -d "{\"repo_id\":\"demo\",\"prompt\":\"hello\"}"
```

See [`doc/design/v0.x/24-scheduler.md`](doc/design/v0.x/24-scheduler.md).

### Web fleet console (0.25)

`anvil-web` is a separate ops UI: SQLite account auth (7-day Bearer session in the browser), BFF to the scheduler. Manage node capacity (+/− `max_workers`), drain/enable, and view token usage. Extensible shell (`static/js/nav.js` + `features/*`).

```bash
cd source
go build -o ../bin/anvil-web.exe ./cmd/anvil-web
set ANVIL_SCHEDULER_TOKEN=sched-token
../bin/anvil-web.exe --config cmd/anvil-web/web.example.yaml
# open http://127.0.0.1:17900 → install wizard (create root admin) → Clusters / Usage
# optional env bootstrap instead of wizard: ANVIL_WEB_ADMIN_USER / ANVIL_WEB_ADMIN_PASSWORD
```

Scheduler also exposes `GET /v1/usage` and `PATCH /v1/nodes/{id}` (`max_workers`, `accepting`). See [`doc/design/v0.x/25-web-admin.md`](doc/design/v0.x/25-web-admin.md).

**Adding a web feature:** register a nav item in `internal/web/static/js/nav.js`, add `static/js/features/<name>.js` with `mount`/`unmount`, and wire `/api/v1/...` on [`internal/web/server.go`](source/internal/web/server.go) (+ schedclient if needed).

### Flutter mobile app (0.26)

Phone client under [`app/`](app/): web URL is baked via `--dart-define=ANVIL_WEB_URL=...` (default `http://127.0.0.1:17900`). Sign in with username/password, pick a **fleet** from `GET /api/v1/fleets`, then call that scheduler for cluster (nodes scale/drain), jobs, and models.

**Joint debug (recommended):** one script builds binaries and starts **gateway + scheduler + agentd + web** with a shared sessions DB under `.dev/`. After start it `PUT /v1/llm` so workers call the local gateway.

```bash
# required for real LLM through gateway:
set UPSTREAM_OPENAI_KEY=sk-...
# optional OpenAI-compatible upstream (default https://api.openai.com/v1):
set UPSTREAM_OPENAI_URL=https://api.openai.com/v1
# or write both into .dev/upstream.env

python scripts/dev_stack.py
# python scripts/dev_stack.py --upstream-key sk-... --upstream-url https://...
# python scripts/dev_stack.py --repo E:/path/to/work/repo
# python scripts/dev_stack.py --wizard       # browser install wizard (resets web DB)
# python scripts/dev_stack.py --no-gateway   # skip LLM path
# Install wizard is only allowed from localhost or 192.168.0.0/16.

# another terminal
cd app
flutter pub get
flutter run
# login: http://127.0.0.1:17900 + http://127.0.0.1:17800
# user/pass default: admin / adminadmin
```

Logs: `.dev/*.log`. Ctrl+C in the script terminal stops the stack.

See [`doc/design/v0.x/26-mobile-app.md`](doc/design/v0.x/26-mobile-app.md) and [`app/README.md`](app/README.md).

Still deferred: Cursor Bridge, cloud agents, full hooks catalog, OIDC/RBAC, workflow/milestone DAG on mobile, and marketplace publication of the VS Code extension (0.35 ships a VSIX you install yourself — [design 35](doc/design/v0.x/35-vscode-extension.vscode.md)).

## License

Licensed under the MIT License. See [LICENSE](LICENSE).