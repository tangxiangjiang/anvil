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
| → | `session.new` \| `session.resume` \| `session.list` \| `session.close` | session == one agent id. `session.resume` takes every `session.new` param plus `agent_id`, so the same conversation re-opens under new options. `session.list` answers `SessionInfo[]` with `runs`, `last_status` and the cumulative `usage` of the stored runs. |
| → | `prompt` | `session_id`, `message`, `attachments[]{path\|data, mime}`, `context{mentions[]{path}, selection{path, range, text}, diagnostics[]{path, line, severity, message}}` |
| ← | `event` | `run_id` + **`JobEventBody`** (below) |
| ← | **`approval.request`** | `request_id`, `session_id`, `run_id`, `tool`, `args`, `preview{kind, path, diff, before?, after?, command[], summary, truncated?}`, `decisions[]`, `timeout_ms` |
| → | **`approval.response`** | `request_id`, `decision: allow\|deny\|allow_session`, `scope?`, `args?`, `message?` |
| → | `cancel` | `session_id` |
| → | `models.list` | → `ModelInfo[]` for the picker |
| → | **`skills.list`** | `scan_user?`, `skill_dirs?` → `skills[]{id, name, description, path, always}` |
| → | **`rules.list`** | `load_project?`, `load_cursor_rules?` → `files[]{path, bytes, included, truncated}` |

### Event payload comes from the public SDK types

The `event` notification carries the fields of the public **`anvil.Event`** (`source/types.go`) — `type, text, tool_call_id, name, args, content, is_error, step, max_steps, code` — plus the `session_id` / `run_id` the host adds for correlation. The terminal `done` carries the public **`anvil.RunResult`**: `status, result, steps, usage, err, code`.

Deliberately **not** the daemon's `JobEventBody` / `JobDoneBody`: those belong to the tool-chain protocol, and depending on them is exactly the coupling this chapter forbids. The shapes look alike because both derive from the same runtime events — that resemblance is a consequence, not a contract.

Consequences the extension must respect:

- Assistant text arrives as **concatenatable chunks, not guaranteed per-token** — a provider that does not stream yields the whole content in one `assistant` event.
- `usage` is **terminal only** (on `anvil.RunResult`). A live token meter would need an SDK addition and is out of scope; `session.list` is the only other place it appears, as the stored runs' cumulative totals.
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

### Sessions, modes and commands as built (p_0.35.6)

- **One mechanism carries every option change: close, then resume.** A mode, a model, and a skill list are all fixed when the SDK builds its options, so changing one means opening another session — and `ChatSession` therefore keeps the agent id in a field of its own, outliving the live session. `reopen(changes)` closes and resumes under the same agent; `continueWith(id)` adopts a stored one; `reset()` forgets it so the next prompt starts fresh. There is one code path behind the mode select, `/plan`, `/model`, `/resume`, and a skill command, which is what makes "the conversation survives" a property of the design rather than four promises.
- **A change that is refused has to be refused everywhere.** `HostManager.applyOptions` records the choice as an override on top of the settings (so it survives a host restart) and rolls that override back when the re-open refuses it; the panel checks the running state *before* asking, so the refusal is reported as a refusal instead of leaving a mode that quietly takes effect on the next session. The mode lives in `workspaceState`, not in settings: `extension.ts` restarts the host on any `anvil.*` change, and a mode stored as a setting would take the conversation with it.
- **Skills and rules needed public SDK APIs.** The host imports the public `anvil` package only, so `internal/skills` and `internal/rules` are unreachable from it. `anvil.ListSkills` and `anvil.ListRuleFiles` are additive public wrappers over the same discovery the prompt path uses — and `internal/rules` was refactored onto one `discover()` pass that yields both the prompt text and the per-file report, so what the panel lists and what the model reads cannot drift. The report says more than a list: a file whose body the budget never reached is `included: false`, and the file the budget cut is `truncated: true`. "Why is the model ignoring my AGENTS.md" is a question about those two flags.
- **A rule file that was skipped is shown unbidden; a rule file that loaded is not.** The panel keeps a `Rules (n)` button for the list, and speaks up on its own only when something went wrong — a truncated or skipped rule file is a silent failure of something the user wrote, which is the one case they cannot discover for themselves. Showing the list after every turn would be noise about the ordinary case.
- **Slash commands are decided in a pure module and executed where a window exists.** `commands.ts` holds the catalogue, the parser, and `route()`, which turns a line plus the catalogue into an action (`mode`, `model`, `resume`, `clear`, `help`, `skill`, or a plain prompt). Everything that needs a quick pick or a session — `chatPanel.ts` — consumes that decision. Built-ins are matched case-insensitively and are never shadowed by a skill with the same name; a slash that names nothing stays a prompt verbatim, so `/usr/bin/env is missing` is a sentence and not an error.
- **The `/` palette is an affordance, not a dispatcher.** It lists what can be typed and inserts a name; Enter still goes through the same submit path as anything else. Keeping the webview that dumb is what lets route resolution stay on the extension side, next to the catalogue it needs.
- **A resumed conversation cannot be replayed.** There is no public transcript reader in the SDK, so the panel marks the resume and moves on rather than inventing a history it does not have; the prior context is real — it is the agent's — but the *rendering* of it is not available to a client. This is a limit of the current SDK surface, documented rather than worked around.
- **The mode select is driven by the extension, never by the click.** It is disabled while a turn runs and updated from the `mode` message, so a refused change corrects the control instead of sticking in it. `bypass` is the one mode that changes the panel's appearance, because it is the one mode that removes a safeguard.

### Packaging as built (p_0.35.7)

- **A platform VSIX carries exactly one sidecar, at `bin/anvil-host[.exe]`.** The target is a parameter of the build rather than a property of the extension, because an installed extension cannot know which machine it will run on: VS Code's platform is a fact about the *host*, and `ExtensionContext.extensionUri` says nothing about it. `scripts/package-vsix.mjs --all` therefore emits one VSIX per target — `win32-x64`, `win32-arm64`, `linux-x64`, `linux-arm64`, `darwin-x64`, `darwin-arm64` — each built by `--target <id>`, cross-compiled with `CGO_ENABLED=0` so a Linux package does not depend on the glibc vintage of the machine that opens it. The target list is written once, in `src/hostBundle.ts`, and both scripts read their behaviour from it (the build script keeps a copy, and a test reads the manifest it produced and fails if a name is one the extension would not look for).
- **The developer's binary and the shipped binary are the same file.** `npm run build:host` writes to `bin/`, which is where F5 resolves the host from and what `vsce package` picks up; there is no "build directory" and "package directory" to drift apart. The `vscode`-free `hostBundle.ts` holds the whole resolution order — setting, then bundle, then `PATH` — which is why the packaged case, the F5 case, and the "I built my own" case are one code path and one set of tests.
- **What is bundled is checked before it is run.** `build-host.mjs` writes `bin/hosts.json` beside the binary: file name, sha256, size, Go toolchain, protocol revision, build time. At spawn the extension hashes the bundled binary and refuses to run a mismatch, or a bundled binary the manifest does not name at all. The check applies **only** to the bundled host: a configured path or a `PATH` lookup is the user's own build, and the extension has nothing truthful to compare it against — refusing those would break the developer loop while proving nothing. This is an integrity check against a broken package (a truncated download, a half-replaced `bin/`, a stale file next to fresh JavaScript), not a signature: anyone who can rewrite the binary can rewrite the manifest. It is not claimed to be more than that.
- **Two version checks, and only one of them is fatal.** `initialize` asserts the protocol revision, as it always did, and now fails with which binary answered, both revisions, and the two ways out — a mismatch must not look like a hang. On top of that the host's banner is compared against the extension's version: a different *minor* is reported as a warning, because shipping a stale sidecar for one platform is the failure this milestone makes easy, but a patch or a newer build is not an error and refusing it would strand anyone running one. The compile-time half is cheaper still: `build-host.mjs` refuses to build when `proto.go` and `protocol.ts` disagree, so two halves of one wire format cannot be packaged apart.
- **A package contains the product and nothing else.** `.vscodeignore` drops `src/`, the tests, the scripts, the source maps, and the workspace's own `.anvil/` session state — the last of these being the one that matters: it is somebody's conversations, and an extension has no business carrying them. `scripts/smoke-packaged.mjs` extracts a built VSIX to a temporary directory and asserts both the required files and the forbidden ones, verifies the binary against the manifest in the package, and then runs *that* binary and completes the handshake. A package that only works from the tree it was built in fails it.
- **The extension version tracks the product line.** `package.json` moves with the milestone (0.35.7 here), the marketplace publishing story stays a non-goal, and the VSIX is unsigned: it is installed with `code --install-extension` for a local trial, which is what this phase was for.

### The panel's layout as built (0.35.7, after the first install)

The first install of a packaged VSIX found a defect no headless check could have seen: the chat panel had no scrollbar, and a long transcript was drawn as evenly spaced slivers. Both halves are properties of `media/chat.css`, and both are now written down as invariants.

- **The transcript is the panel's only scroll container.** `html` and `body` are 100% tall, `body` is a column flex container, and the transcript is the flexible child (`flex: 1; min-height: 0; overflow-y: auto`) with the banner, status line, mode toolbar and composer taking their natural height around it. `min-height: 0` is the load-bearing declaration: without it a flex item refuses to be smaller than its content, so the transcript would grow and the panel — not the transcript — would be the thing that overflows, taking the composer off the bottom edge with it.
- **Children of a scroll container must be forbidden from shrinking.** Flex items shrink by default, and an item's automatic minimum size is its content height only while its `overflow` is `visible`. A tool card sets `overflow: hidden` (so its rounded corners clip its own output pane), which makes that minimum 0 — so a transcript of forty tool calls compressed them to nothing instead of overflowing, and because the flex `gap` between items is not compressible the result was a column of evenly spaced rules. One rule fixes it and states the invariant: `.transcript > *, .palette > * { flex: 0 0 auto; }`. The pairing of `overflow: hidden` with a flex column is the trap, not the `hidden` itself.
- **It is guarded by reading the stylesheet, not by rendering it.** There is no browser in the extension's test suite and no lint that knows flexbox, so `test/unit/chatLayout.test.ts` parses `media/chat.css` and asserts the structure the two points above describe: every column flex container that declares a scrolling overflow has a rule stopping its children from shrinking, and the height chain from `html` down to the transcript is unbroken. It is a weaker check than a screenshot and it would have caught this — the same failure mode it exists for is the one that got shipped.

## Placement

- Extension (TypeScript): repository root **`editors/vscode/`** — mirrors the `app/` precedent from [26](26-mobile-app.md) of keeping non-Go clients outside `source/`. `bin/` inside it holds the built sidecar; it is git-ignored, and the packaged extension carries it.
- Build and packaging scripts: **`editors/vscode/scripts/`** — `build-host.mjs` (cross-compile + manifest), `package-vsix.mjs` (compile, host, `vsce`), `smoke-packaged.mjs` (open a VSIX and run what is inside). `@vscode/vsce` is a dev dependency, run through `node` rather than its `.cmd` shim, since a Windows shim needs a shell and a shell needs quoting rules for a command that has none.
- Host binary: **`source/cmd/anvil-host/`**; protocol + session registry in **`source/internal/editorhost/`** (`.go` files stay under `source/` per `.cursor/rules/source-layout.mdc`).
- Working state stays in the workspace: `{cwd}/.anvil/` (already git-ignored).

## Security

- **Workspace trust**: refused twice over — declared in `capabilities.untrustedWorkspaces` so the extension does not activate, and asserted at the spawn path so no future caller can start the agent in a folder the user has not trusted.
- API key lives in VS Code **SecretStorage**, injected into the host child's env at spawn; never written to settings or logs.
- The sidecar holds the same cwd sandbox as any Anvil local agent ([08](08-security.md)); path escapes remain tool errors.
- Approvals default to deny; `bypass` is opt-in and visibly flagged.
- No new network listener — stdio only.
- **The bundled sidecar is checked against the digest the build recorded** before it is run, and only for the bundled case; the check is an integrity check on the package, not a signature, and is documented as such. A package for another platform is never run by mistake: the host file name is looked up per target.
- **A package carries no workspace state.** `.anvil/` (agents, runs, rules the user wrote for their own workspace) is excluded from the VSIX and its absence is asserted by the packaging smoke test.

## Explicit non-goals (0.35)

- Bundling or shipping VS Code Server, or otherwise touching [32](32-sandbox-code-review.md) — that line is tool-chain, this one is not.
- Becoming a language server / DAP debugger; LSP stays **agent tools** ([16](16-lsp.md)).
- Any scheduler / `agentd` / cluster path from the extension. This protocol deliberately has none; adding one would be a new, separately-decided capability rather than an extension of it.
- Notebook cell editing, multi-root in one panel, sub-agent graph UI, checkpoints/rewind.
- Persisting per-hunk approval policy to disk, or a settings-sync story.
- Public marketplace publishing as a milestone acceptance item.

## Acceptance (milestone)

1. Installing the platform VSIX and opening a trusted workspace starts the host; the panel streams assistant text and tool cards. (The extension half is p_0.35.4, the package is p_0.35.7; what a script can check — the tree, the provenance, and the handshake of the binary inside the package — is `npm run smoke:vsix`, and the window-level pass is installing it.)
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
