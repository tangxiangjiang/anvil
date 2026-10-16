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
| → | `initialize` | `protocol`, `cwd`, `client{name, version}` — the workspace root, fixed for the process. Everything else a session needs goes on `session.new`. |
| ← | `initialized` | `host_version`, `capabilities[]` |
| → | `session.new` \| `session.resume` \| `session.list` \| `session.close` | session == one agent id |
| → | `prompt` | `session_id`, `message`, `attachments[]{path\|data, mime}`, `context{mentions[]{path}, selection{path, range, text}, diagnostics[]{path, line, severity, message}}` |
| ← | `event` | `run_id` + **`JobEventBody`** (below) |
| ← | **`approval.request`** | `request_id`, `session_id`, `run_id`, `tool`, `args`, `preview{kind, path, diff, before?, after?, command[], summary, truncated?}`, `decisions[]`, `timeout_ms` |
| → | **`approval.response`** | `request_id`, `decision: allow\|deny\|allow_session`, `scope?`, `args?`, `message?` |
| → | `cancel` | `session_id` |
| → | `models.list` | → `ModelInfo[]` for the picker |

### Event payload comes from the public SDK types

The `event` notification carries the fields of the public **`anvil.Event`** (`source/types.go`) — `type, text, tool_call_id, name, args, content, is_error, step, max_steps, code` — plus the `session_id` / `run_id` the host adds for correlation. The terminal `done` carries the public **`anvil.RunResult`**: `status, result, steps, usage, err, code`.

Deliberately **not** the daemon's `JobEventBody` / `JobDoneBody`: those belong to the tool-chain protocol, and depending on them is exactly the coupling this chapter forbids. The shapes look alike because both derive from the same runtime events — that resemblance is a consequence, not a contract.

Consequences the extension must respect:

- Assistant text arrives as **concatenatable chunks, not guaranteed per-token** — a provider that does not stream yields the whole content in one `assistant` event.
- `usage` is **terminal only** (on `anvil.RunResult`). A live token meter would need an SDK addition and is out of scope.
- `Run.Stream(ctx)` ignores its `ctx`; cancellation is `Run.Cancel`, driven by the `cancel` method.
- `Run.emit` drops events when its buffer fills. The host must drain the channel and forward without blocking on stdio writes (use an internal queue), or the UI silently loses output.

## Context injection

What the editor knows, the agent should know — but the client decides *what*, and the host decides *how much*. The extension sends a `context` object on `prompt`; the host composes it into the turn.

- **The host composes text, not structure.** There is no multi-part text message in the public SDK — `Agent.Send` takes a `string` — so mentions, selection, and diagnostics are folded into the message the same way plan mode's directive is (p_0.35.3). A structured-content SDK API would be a new capability; this phase does not need one.
- **Order: directive prefix, context block, user message.** The model reads the request last, immediately after the material it is about.
- **Mentions are read by the host, under the workspace root.** A file mention carries its contents; a folder mention carries a listing, not a dump. Caps: **32 mentions**, **64 KiB per file**, **256 KiB total**, **200 listing entries**. A capped file is included up to the cap with an explicit truncation note — silently sending half a file would be worse than saying so.
- **Diagnostics are the client's.** The editor already has them from the language server, so the host neither computes nor filters them; it renders a compact block. The cap (100 entries) exists to keep a broken file from filling the context.
- **Attachments ride the vision path.** `attachments[]` becomes `anvil.ImageAttachment` and goes out on `anvil.WithAttachments`; in-memory bytes for a pasted image, a workspace-relative path for one picked from disk. Vision is **off by default** and a session must opt in (`vision: true` on `session.new`), because `Options.Vision.Enabled` is what makes the SDK accept an attachment at all — a client that sends one to a non-vision session gets `-32602` naming the setting, not a run that mysteriously ignores the image.
- **The host reads nothing outside the workspace.** A mention path is resolved against the session cwd, cleaned, and re-checked after symlinks; an absolute path outside the root, a `..` escape, and a symlink pointing out are all rejected as invalid params. The SDK's own sandbox still governs what *tools* may touch; this check governs what the host will read into a prompt on a client's word, which is a different and narrower promise.

## Approval and permission modes

The SDK already exposes a **blocking** approval hook — `Options.Permission` (`source/options.go`):

```go
type PermissionFunc func(ctx, PermissionRequest) (PermissionDecision, error)
type PermissionRequest  struct { ToolName string; Args json.RawMessage; Preview ToolPreview }
type PermissionDecision struct { Allow bool; Args json.RawMessage; Message string }  // Args rewrites the call
```

The host installs a callback that emits `approval.request` and blocks until `approval.response` arrives. The round trip needs no SDK change; the **preview** does — see below.

### Gap: the gate is too narrow

The pre-0.35 gate fired only when `tools.IsMutating(name)` was true, and that map held exactly four names: `write_file`, `edit_file`, `apply_patch`, `shell`. **`run_tests` and `build` execute arbitrary argv, `web_search`/`fetch_url` egress, and MCP / plugin / custom tools were all ungated.** For an editor that must ask before acting, this is the blocking gap.

It was closed **additively** (shipped in p_0.35.3):

- Gating is by **tool group**, and the table now lives in `source/internal/tools/groups.go` as the single source of truth — `source/toolfilter.go` delegates to it, so the SDK gate and the tool filter cannot drift apart.
- New option `Options.PermissionGroups []string`; **empty keeps the old behaviour exactly** (the four mutating builtins). The host opts in to the wider set: `write, edit, shell, verify, web, mcp`.
- `MCP`, plugin, and `Options.Tools` handlers were routed through the gate. They belong to no group — recognised by type, since every external tool is a `*tools.FuncTool` — so they are gated **unconditionally once `PermissionGroups` is non-empty**, and stay ungated when it is empty.
- `Options.PermissionGroups` is the only change that alters what the SDK does. `PermissionRequest.Preview` is additive: every existing caller compiles and behaves identically.

### Previews are computed in the SDK

The preview cannot be computed in the host. The host imports the **public `anvil` package only** (see the package doc in `source/internal/editorhost/proto.go`), so it may not reach into `source/internal/tools/patch.go` for the `apply_patch` dry run. The SDK therefore builds the preview at gate time and hands it over on `PermissionRequest.Preview`; the host projects it field for field and stays import-clean.

The host does not have to be trusted with the pre-image either: a preview is best-effort by contract, and an empty `Kind` means "nothing could be shown", never "this call is safe".

For **diff review**, the preview carries the pre-image and the projected post-image (`before`, `after`) alongside the unified diff. The alternative — a client applying the diff itself to reconstruct the result — puts a second implementation of the patch algorithm in the editor, and the thing it would get wrong is the thing the user is approving. The bodies are what the SDK already had in hand when it built the diff, so they cost nothing to include.

Both bodies share one cap; past it, **both are dropped together** and `truncated` is set, because a diff editor showing a clipped post-image as if it were the whole file is worse than showing the diff text. `apply_patch` is the exception: it may span several files, so it sends the concatenated diff and no bodies, and the client renders that diff rather than opening a diff editor. When the answer is `allow` with rewritten `args`, the review has to be re-opened against the edited call — the preview describes the call that was asked for, not the one the user changed it into.

### Modes (Claude Code-class)

| Mode | Implementation |
|------|----------------|
| `ask` (default) | every gated call round-trips to the UI |
| `accept_edits` | the `write` / `edit` selectors are dropped from the gated set, so in-workspace edits apply without asking; `shell` / `verify` still do |
| `plan` | `DisallowedTools: ["write","edit","shell","verify"]` + a directive to produce a plan and stop. Pure SDK composition — no new code. |
| `bypass` | user opts in explicitly; UI shows a persistent warning |

Each mode is a **composition of public SDK options**, not a special case inside the callback: `accept_edits` works by narrowing the group set it passes as `Options.PermissionGroups`, and `plan` by adding `Options.DisallowedTools`. The sandbox already confines path-taking tools to the workspace, so "in workspace" needs no extra check.

The client sets these per session, on `session.new` / `session.resume`:

| Param | Meaning |
|-------|---------|
| `permission_mode` | `ask` (default) / `accept_edits` / `plan` / `bypass` |
| `permission_groups` | overrides the default gated set (`write, edit, shell, verify, web, mcp`); ignored in `bypass` |
| `approval_timeout_ms` | per-session wait for an answer; `0` = default 5m, negative = until the run ends |
| `test_command` / `build_command` | argv for `run_tests` / `build`. Without them the host registers neither tool, so those calls cannot reach an approval at all |
| `vision` | opt in to image attachments; off by default, and a prompt carrying one without it is refused |

- **Session allowlist**: the host remembers `(session, tool, scope)` approvals and short-circuits the callback. In-memory per session; not persisted. `scope` is opaque to the host — it compares it verbatim and takes it from `preview.path`, so a client can offer "always allow shell" (no scope) and "always allow edits to this file" (that path) through one field.
- **Diff review before apply**: for `write_file` / `edit_file` / `apply_patch` the SDK computes the resulting diff at gate time and puts it on `preview`. `apply_patch` is dry-run through the existing patch implementation, so the files it reports are the files it would touch — and the dry run must not mutate anything. This is the review-changes flow.
- **Command preview**: for `run_tests` and `build` — argv arrays fixed by `Options`, never shell strings — `preview` is that argv. `shell` is the exception: it runs one command string, so `preview.command` carries that string as its single element.
- **Plan mode's directive is a prompt prefix, not a system-prompt addition.** `Options.SystemPrompt` *replaces* Anvil's default system text, and the host cannot read text it did not write, so it cannot append to it. The host prefixes the plan directive to the user's message instead.
- **Nothing may hang.** A pending approval is released by the answer, by the run ending, by `cancel`, by `session.close`, by host shutdown, and by a timeout that defaults to deny. Whatever the path, the request is deregistered, so a late `approval.response` is refused with `-32004` instead of silently vanishing — the client learns its answer did not land. A *malformed* answer (an unknown `decision`) is `-32602`, because that is the client's bug and "too late" would send it looking in the wrong place.
- Aborting mid-approval must leave the client with a reason: the model sees a tool error carrying it, and the run still reaches a terminal `done`.

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

### The extension tree as built (p_0.35.4)

`editors/vscode/src/`: `protocol.ts` (the contract, mirroring `proto.go`), `hostClient.ts` (spawn, framing, correlation, teardown), `session.ts` (the conversation state machine), `errors.ts` (code → presentation), `settings.ts`, `hostManager.ts` (lifecycle), `chatPanel.ts` (the view), `extension.ts` (wiring). `media/` holds the webview's `chat.js`, `chat.css` and the activity-bar icon.

- **The `vscode` boundary is the test boundary.** Everything above the last three imports no `vscode`, so it runs under a plain Node test driving a real child process. Anything needing `vscode` is kept thin and delegates downward; the alternative — mocking the editor API — would have tested the mock.
- **One host per window, spawned lazily.** The first prompt starts it, not activation. It is stopped on `deactivate`, on a workspace-folder change (the child's cwd *is* the workspace root), and on a configuration change (the host reads its config at spawn). A crash is reported once and the next prompt restarts it.
- **The webview is a rendering surface only.** Sessions, approvals and error classification live on the extension side; the HTML holds no state the host needs, and everything it renders from the host goes through `textContent`, never `innerHTML` — tool output is arbitrary file content and command output, and it must not be able to add markup or script to the panel. The document is served under a CSP with a per-render nonce and no network sources.
- **Trust is declared, not just checked.** `capabilities.untrustedWorkspaces.supported: false` keeps VS Code from activating the extension in a folder the user has not trusted; the spawn path asserts it as well, because a UI check is a message, not an enforcement point.
- **Testing two ways.** `test/fixtures/fake-host.mjs` speaks the real protocol from a script, which is what makes the state machine testable without a key or a network — but a stub only pins the extension's *assumptions* about the Go side. Two integration tests therefore drive the real `anvil-host` binary with the model call aimed at `127.0.0.1:9`, which ends the run offline and deterministically and pins the wire against the actual Go writer.

### Context and diff review as built (p_0.35.5)

- **Five new modules, split along the existing line.** `mentions.ts` (a picked path, and the paths a tool call's arguments name) and `attachments.ts` (a data URL into `{data, mime}`, with its refusals) import no `vscode` and are unit-tested. `editorContext.ts` (selection, diagnostics, the quick pick), `review.ts` (the content provider and the diff editor), and `changes.ts` (the changed-files tracker) do import it and stay thin — the same boundary p_0.35.4 drew.
- **Mentions are picked, then sent as paths.** The composer opens a quick pick over `workspace.findFiles` on `@`, and the chip it leaves behind holds a workspace-relative path — never file contents. What gets read, how much of it, and whether it is even inside the workspace is the host's decision, made once, for every client.
- **Selection and diagnostics come from the editor, on send**, not when the panel was opened: the selection the prompt should carry is the one that exists when the user presses Send. Diagnostics are read from the language server already running, which is why the extension computes nothing.
- **A pasted image travels as bytes.** The webview reads the pasted `File` with a `FileReader` and posts the `data:` URL it produces — the one thing a webview can do with a `File` — and the extension turns that into `{data, mime}`. The conversion is where the refusals live (not an image, not base64, empty, larger than the composer's per-paste bound), it has no `vscode` import, and it is unit-tested: a payload that reaches the wire with the wrong MIME type is a model request made from something the user did not send. The bound is a transport sanity check, not a copy of the SDK's budget — the vision pipeline downscales to a 2048-longest side and applies its own 4 MiB encoded cap afterwards. Bytes, not a path, so no temporary file is written anywhere; a vision-off session is warned in the composer *and* refused by the host.
- **Diff review is a real diff editor.** The extension serves the preview's `before` / `after` through a read-only `TextDocumentContentProvider` and opens `vscode.diff` against them, so the review is the editor's own diff UI — folding, inline changes, and the theme included — rather than a second renderer inside a webview. Accepting closes the review and answers `allow`; dismissing answers `deny`. The review is a tab about a *proposed* change, so it is closed when the question it belongs to is answered, withdrawn, or the run ends: a window full of diffs of changes that were never made is worse than no review at all. When the bodies are absent — dropped by the cap, or an `apply_patch` that spans files — the card says so and the diff text is the review, rather than opening an empty diff editor.
- **The changed-files list needs snapshots, not reconstruction.** Before allowing a call that would write, the extension reads the target's current content into memory, keyed by path; it does the same at the `tool_call` event, which is what covers the calls that were never gated (accept-edits, bypass). After the turn, each touched file gets an entry whose diff opens against that snapshot. Reading the pre-image *before* the write is the only moment it is available — the host's preview has it, but the client that applied the edit does not keep it, and re-deriving it from git would make the feature depend on the workspace being a clean git checkout. First touch wins within a turn, so a file written twice is diffed against the state the turn started from. The list covers the tools that name what they write — `write_file`, `edit_file`, and the file headers of an `apply_patch` — and not a `shell` command that redirects into a file: rescanning the workspace after every turn would close that gap and cost far more than the gap does.

## Placement

- Extension (TypeScript): repository root **`editors/vscode/`** — mirrors the `app/` precedent from [26](26-mobile-app.md) of keeping non-Go clients outside `source/`.
- Host binary: **`source/cmd/anvil-host/`**; protocol + session registry in **`source/internal/editorhost/`** (`.go` files stay under `source/` per `.cursor/rules/source-layout.mdc`).
- Working state stays in the workspace: `{cwd}/.anvil/` (already git-ignored).

## Security

- **Workspace trust**: refused twice over — declared in `capabilities.untrustedWorkspaces` so the extension does not activate, and asserted at the spawn path so no future caller can start the agent in a folder the user has not trusted.
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

1. Installing the platform VSIX and opening a trusted workspace starts the host; the panel streams assistant text and tool cards. (The extension half of this is p_0.35.4 — the VSIX is p_0.35.7.)
2. Mutating tools prompt; deny returns a tool error to the model; allow-for-session stops re-prompting for that tool.
3. A `shell` command, a `run_tests`, and an MCP tool call each prompt (the gate gap is closed). The gate is closed as of p_0.35.3; the last two need the host to be able to *configure* those tools first, which the settings work brings.
4. Diff review shows the result of an edit before it is applied.
5. `@`-mentioned file content and the current selection reach the model.
6. Plan mode performs no writes.
7. Session list shows past agents; resuming continues the conversation.
8. Untrusted workspace does not start the agent; the key never appears in settings, logs, or the VSIX.
9. `go test ./...` green in `source/`; extension unit tests green; one integration test drives a stubbed host end to end.

## Roadmap mapping

[m_0.35_vscode_extension.md](../../roadmaps/v1.0/m_0.35_vscode_extension.md) phases `p_0.35.1` … `p_0.35.7`.
