# 35 — VS Code extension + editor host

**Product version focus:** **0.35** ([m_0.35_vscode_extension.md](../../roadmaps/v1.0/m_0.35_vscode_extension.md))  
**Status:** planned

## Intent

Ship a first-party **VS Code extension** so the Anvil agent is usable inside the editor the developer already has open, at **Claude Code-class interaction**: streaming chat, per-call approvals, diff review before apply, `@` context, slash commands, plan mode, resumable sessions.

**This unblocks a long-standing non-goal.** [00-overview.md](00-overview.md) and the roadmap index list "First-party IDE plugin UI" as explicitly out of scope. Chapter 35 supersedes that exclusion for the **client** only.

## Independence (locked)

`anvil-host` is a **standalone capability**. It consumes **only the public `anvil` package** — `Create` / `Resume` / `Send` / `Stream` / `Wait` / `Cancel`, `Options`, `LocalOptions`, `anvil.Event`, `anvil.RunResult` — and nothing from the Anvil **automated coding tool-chain** (scheduler, `agentd` workers, clusters, anvil-web, the delivery pipeline). It must not import `internal/daemon`, `internal/scheduler`, `internal/web`, or any other `cmd/anvil-*` package, and must not read platform config files.

Consequences to hold during implementation:

- Its event payload is built from the SDK's **own public types**; it does not borrow `JobEventBody` / `JobDoneBody` from the daemon protocol.
- It defines its own session concept over `anvil.Agent`; it needs no repo allowlist, node id, scheduler URL, or cluster token.
- The tool-chain and the extension **evolve independently**: a scheduler/daemon protocol change can never break the extension, and vice versa.

Not the same thing as [32](32-sandbox-code-review.md), which *is* tool-chain: it hosts **code-server** on a node for sandbox review (Browser → anvil-web → nginx → ide-proxy, workspace = a sandbox checkout). Chapter 35 runs locally in the developer's real checkout, over the SDK alone. The two share no code path.

## Topology

```text
VS Code extension (TypeScript)        anvil-host (Go, stdio)              LLM
┌────────────────────────────┐        ┌──────────────────────────┐
│ Agent panel (webview)      │◄──────►│ in-process anvil.SDK     │──────► OpenAI-compatible
│ Approval prompt / diff view│ newline│ Options.Permission hook  │        or Anthropic
│ Settings + SecretStorage   │ JSON-  │ session registry         │
│ Workspace trust gate       │ RPC 2.0│ event → RPC notification │
└────────────────────────────┘        └──────────────────────────┘
```

One `anvil-host` child per **VS Code window**, spawned on activation, killed on window close. One agent session per chat session; the workspace folder is the agent `cwd`.

## Process boundary (locked)

The extension cannot import the Go library, so a sidecar is mandatory. Three candidates were considered; two are rejected:

| Option | Verdict |
|--------|---------|
| `cmd/agent-worker` (stdin job → stdout NDJSON) | **Rejected** — one-way by construction. No back-channel, so a mid-run approval prompt is impossible. |
| `cmd/agentd` control protocol | **Rejected** — it is tool-chain: a per-machine daemon with a hand-authored repo allowlist and token file, the wrong lifecycle for a window, and it would couple the extension to the platform. |
| **New `anvil-host`** | **Chosen** — stdio child whose lifetime is the window; zero config; bidirectional by nature; depends on the SDK alone. |

`agentd` and the scheduler keep their identity as the multi-job / cluster plane ([11](11-worker-daemon.md), [24](24-scheduler.md)) with **no relationship to this chapter**. `anvil-host` has no delegation path into that plane: its backend is the in-process SDK, full stop. See **Independence (locked)** above.

## Host protocol

Transport: **stdio**, one compact JSON object per line (`\n`), JSON-RPC 2.0. This is deliberately the same framing as the plugin exec client ([20](20-plugins.md), `internal/plugin/exec.go`) and its test fixture `internal/plugin/fakecmd/` — a proven shape in this repo.

Messages marked ← are server→client **notifications** (no `id`); → are client→server requests.

| Direction | Method | Payload |
|-----------|--------|---------|
| → | `initialize` | `cwd`, `settings{provider, model, base_url, max_steps, store}`, `client_info` |
| ← | `initialized` | `host_version`, `capabilities[]` |
| → | `session.new` \| `session.resume` \| `session.list` \| `session.close` | session == one agent id |
| → | `prompt` | `session_id`, `text`, `attachments[]`, `context{active_file, selection, diagnostics, mentions[]}` |
| ← | `event` | `run_id` + **`JobEventBody`** (below) |
| ← | **`approval.request`** | `request_id`, `tool`, `args`, `preview`, `options[]` |
| → | **`approval.response`** | `request_id`, `decision: allow\|deny\|allow_session`, `args?`, `message?` |
| → | `cancel` | `session_id` |
| → | `models.list` | → `ModelInfo[]` for the picker |

### Event payload comes from the public SDK types

The `event` notification carries the fields of the public **`anvil.Event`** (`Source/types.go`) — `type, text, tool_call_id, name, args, content, is_error, step, max_steps, code` — plus the `session_id` / `run_id` the host adds for correlation. The terminal `done` carries the public **`anvil.RunResult`**: `status, result, steps, usage, err, code`.

Deliberately **not** the daemon's `JobEventBody` / `JobDoneBody`: those belong to the tool-chain protocol, and depending on them is exactly the coupling this chapter forbids. The shapes look alike because both derive from the same runtime events — that resemblance is a consequence, not a contract.

Consequences the extension must respect:

- Assistant text arrives as **concatenatable chunks, not guaranteed per-token** — a provider that does not stream yields the whole content in one `assistant` event.
- `usage` is **terminal only** (on `anvil.RunResult`). A live token meter would need an SDK addition and is out of scope.
- `Run.Stream(ctx)` ignores its `ctx`; cancellation is `Run.Cancel`, driven by the `cancel` method.
- `Run.emit` drops events when its buffer fills. The host must drain the channel and forward without blocking on stdio writes (use an internal queue), or the UI silently loses output.

## Approval and permission modes

The SDK already exposes a **blocking** approval hook — `Options.Permission` (`Source/options.go`):

```go
type PermissionFunc func(ctx, PermissionRequest) (PermissionDecision, error)
type PermissionRequest  struct { ToolName string; Args json.RawMessage }
type PermissionDecision struct { Allow bool; Args json.RawMessage; Message string }  // Args rewrites the call
```

The host installs a callback that emits `approval.request` and blocks until `approval.response` arrives. No SDK change is needed for the round trip.

### Gap: the gate is too narrow

Today the gate fires only when `tools.IsMutating(name)` is true, and that map holds exactly four names: `write_file`, `edit_file`, `apply_patch`, `shell`. **`run_tests` and `build` execute arbitrary argv, `web_search`/`fetch_url` egress, and MCP / plugin / custom tools are all ungated.** For an editor that must ask before acting, this is the blocking gap.

0.35 closes it **additively**:

- Gate by **tool group** (the group table already exists in `Source/toolfilter.go`: `shell, read, write, edit, search, skills, web, lsp, git, verify, vision, mcp`).
- New option `Options.PermissionGroups []string`; **empty keeps today's behaviour exactly** (the four mutating builtins). The host opts in to the wider set: `write, edit, shell, verify, web, mcp` — and treats plugin/custom tools as gated too.
- `MCP`, plugin, and `Options.Tools` handlers currently bypass the gate entirely; they must route through it so the UI can ask.

### Modes (Claude Code-class)

| Mode | Implementation |
|------|----------------|
| `ask` (default) | every gated call round-trips to the UI |
| `acceptEdits` | in-workspace `write`/`edit` auto-allowed; `shell`/`verify` still ask |
| `plan` | `DisallowedTools: ["write","edit","shell","verify"]` + system-prompt addendum to produce a plan and stop. Pure SDK composition — no new code. |
| `bypass` | user opts in explicitly; UI shows a persistent warning |

- **Session allowlist**: the host remembers `(tool, scope)` approvals and short-circuits the callback. In-memory per window; not persisted.
- **Diff review before apply**: for `write_file` / `edit_file` / `apply_patch` the host has the args *and* the on-disk pre-image, so it can compute the resulting diff and put it in `preview`. `apply_patch` reuses the existing patch implementation in `Source/internal/tools/patch.go`. This is the review-changes flow.
- **Command preview**: `shell`, `run_tests`, `build` are argv arrays (never shell strings) — `preview` is that argv list.
- The callback must abort cleanly when the run is cancelled while an approval is pending.

## Extension surface (VS Code)

| Capability | Note |
|-----------|------|
| Chat panel (webview), streaming assistant + collapsible tool cards | Claude Code-class layout |
| Interrupt | `cancel` → `Run.Cancel`; also cancels a pending approval |
| Approve / deny / allow-for-session | from the approval prompt |
| Diff review | pre-apply preview; afterwards a changed-files list opening native diffs |
| `@` file / folder mentions | `context.mentions` |
| Selection + diagnostics injection | `context.selection`, `context.diagnostics` |
| Image paste | `attachments` → the 0.19 vision path ([19](19-vision.md)) |
| Slash commands | mapped onto existing **skills** ([14](14-hooks-skills.md)) plus built-ins `/plan`, `/model`, `/resume` |
| Session history + resume | `anvil.List` / `ListRuns`; click → `session.resume` |
| Rules / memory | host sets `LoadRules` + `LoadCursorRules`, so `AGENTS.md`, `.anvil/rules/*`, and `.cursor/rules/*` apply automatically |
| Usage display | terminal `JobDoneBody.usage` |
| Model picker | `models.list` → `anvil.ListModels` ([12](12-model-gateway.md)) |

## Placement

- Extension (TypeScript): repository root **`editors/vscode/`** — mirrors the `app/` precedent from [26](26-mobile-app.md) of keeping non-Go clients outside `Source/`.
- Host binary: **`Source/cmd/anvil-host/`**; protocol + session registry in **`Source/internal/editorhost/`** (`.go` files stay under `Source/` per `.cursor/rules/source-layout.mdc`).
- Working state stays in the workspace: `{cwd}/.anvil/` (already git-ignored).

## Security

- **Workspace trust**: refuse to run in an untrusted workspace.
- API key lives in VS Code **SecretStorage**, injected into the host child's env at spawn; never written to settings or logs.
- The sidecar holds the same cwd sandbox as any Anvil local agent ([08](08-security.md)); path escapes remain tool errors.
- Approvals default to deny; `bypass` is opt-in and visibly flagged.
- No new network listener — stdio only.

## Explicit non-goals (0.35)

- Bundling or shipping VS Code Server, or otherwise touching [32](32-sandbox-code-review.md) — that line is tool-chain, this one is not.
- Becoming a language server / DAP debugger; LSP stays **agent tools** ([16](16-lsp.md)).
- Any scheduler / `agentd` / cluster path from the extension. This protocol deliberately has none; adding one would be a new, separately-decided capability rather than an extension of it.
- Notebook cell editing, multi-root in one panel, sub-agent graph UI, checkpoints/rewind.
- Persisting per-hunk approval policy to disk, or a settings-sync story.
- Public marketplace publishing as a milestone acceptance item.

## Acceptance (milestone)

1. Installing the platform VSIX and opening a trusted workspace starts the host; the panel streams assistant text and tool cards.
2. Mutating tools prompt; deny returns a tool error to the model; allow-for-session stops re-prompting for that tool.
3. A `shell` command, a `run_tests`, and an MCP tool call each prompt (the gate gap is closed).
4. Diff review shows the result of an edit before it is applied.
5. `@`-mentioned file content and the current selection reach the model.
6. Plan mode performs no writes.
7. Session list shows past agents; resuming continues the conversation.
8. Untrusted workspace does not start the agent; the key never appears in settings, logs, or the VSIX.
9. `go test ./...` green in `Source/`; extension unit tests green; one integration test drives a stubbed host end to end.

## Roadmap mapping

[m_0.35_vscode_extension.md](../../roadmaps/v1.0/m_0.35_vscode_extension.md) phases `p_0.35.1` … `p_0.35.7`.
