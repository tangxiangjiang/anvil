# 45 — anvil-web polish, the web assistant, and the editor-host smoke tests

**Milestone focus:** 0.45. Statuses live in
[m_0.45_protocol_smoke.md](../../roadmaps/v0.x/m_0.45_protocol_smoke.md).

Milestone 0.45 carries three things that were once separate lines. This chapter records each one
so the design and the roadmap read the same way. The smoke tests come first because they are the
smallest; the console polish and the assistant are the bulk.

## 1. The editor-host smoke tests

0.35–0.44 grew the editor-host wire and the VS Code client, and the coverage stayed fragmented:
unit tests, fake-host integration, a thin real-host handshake, and VSIX packaging smoke. This part
adds two acceptance paths and no product behaviour:

- an **offline tour** (`TestProtocolTour`) that drives initialize, the catalogues, MCP listing from
  the file only, session administration, and read / write / shell through the stub LLM — free and
  deterministic, run on every push;
- a **live tour** (`TestLiveProtocolSmoke`, behind the `livesmoke` build tag) that runs against a real
  provider for long, manual or nightly runs, and never blocks an ordinary push.

Gateway, agentd and scheduler tours are out of scope. Default MCP connect timeouts and the default
`anvil.mcp.project` value do not change.

## 2. The console polish

The Delivery console (chapter [33](33-delivery-ui.md)) made the planning surfaces real. It was not yet
trustworthy to operate. An operator could destroy a planning record with one mis-click, 24 native
dialogs were reachable, a slow response could repaint a page the person had already left, and the
four sections opened differently. The polish pass fixes that without adding capability and without
touching any scheduler or daemon semantics.

Two properties of the current shape drive the work. Every mutation re-fetched the whole snapshot and
re-rendered the whole subtree, and rendering was `innerHTML` string concatenation with `esc()` called
by hand — correct today, one omission away from stored XSS, and no test would catch it.

The design rules are:

1. **A destructive action costs more than a click.** Delete milestone, delete design, delete version
   and seal a major each state what they remove and require an explicit confirmation.
2. **No native dialogs.** `alert`, `confirm` and `prompt` give way to the in-app notice and modal.
3. **Every async action is single-flight and says it is running.** A second click on a
   job-enqueuing button is a no-op, not a second job.
4. **A response never paints a route it did not start in.** Render is guarded by a token.
5. **The four sections share one information architecture:** one generation scope, breadcrumbs on
   every detail view, and designed loading and empty states.
6. **The front end can absorb the fixes:** module boundaries, one definition per helper, and tests
   over the pure functions.
7. **Every mutation is attributable.** Who changed a requirement, who approved a design, who sealed a
   generation, and when — recorded with the job's events and searchable.
8. **First run is observable, not instructional theatre.** The checklist reads authenticated server
   state for admin creation, Fleet configuration, Project binding and the first Requirement Version.
   Demo rows do not satisfy real Project steps. Only its closed/open preference is browser-local;
   it stores no ids, paths, Worker metadata or credentials.
9. **One noun names one domain object.** User-facing UI and help use Fleet, Node, Worker, Project,
   Generation, Milestone, Version, Phase, Job and Session consistently. This does not rename JSON,
   HTTP, database or scheduler protocol identifiers.

List operation follows one interaction contract across Roadmap milestones, test cases, and Design
Versions: identity-backed selection survives rendering, range selection follows the filtered visible
order, stale ids are pruned after refresh, and the keyboard mapping is arrows, Space / `x`,
Shift+Space, Enter, Escape, Cmd/Ctrl-A, and `/`. Milestone batch transitions call the existing
single-item endpoint in stable data order under one list-wide single-flight claim. Successful ids are
removed from selection; failed ids remain selected and are reported with their server reason.
Paste-cases is one bounded, transactional request, preserving the same server authentication and
validation path as any other Delivery mutation.

The mock assistants of the Delivery surfaces are part of this polish. Three surfaces used to save on
Send; they now draft only, and the operator applies a draft through an explicit edit-and-save. The
Gate case was the dangerous one: every checkbox in a design version gate blocks real phase jobs, so a
reply must never add an unchecked box on its own.

Structural gaps the polish found are closed in the same part: job events were not persisted, so a
failed job could not be diagnosed from the console; a transition could be forced through an
incomplete gate; and the console assumed a wide, stable desktop. The fixes are a persisted event
history, a pre-flight check that refuses a structurally incomplete transition and explains why before
the click, and a layout that lets an operator see fleet state and stop a runaway job on a phone and
survive a dropped connection.

Slow-network snapshots follow one bounded stale-while-revalidate contract on Delivery, Ops and
Workbench. The cache is volatile, cleared on authentication changes, LRU/age bounded, and keyed by
the complete surface scope. A cached paint always says it is stale, gives the last-success age and
refreshes in the background. Only the newest request for that exact key may atomically publish a
replacement. Failure preserves the prior snapshot and shows disconnected + Retry. Ops and Workbench
cache projections deliberately omit prompts, event content, credentials and provider/Worker
configuration; this is route resilience, not offline data entry.

The responsive contract is browser-measured, not inferred from CSS: Chrome at 375px must expose Ops
state and Stop with a 44px target and no page overflow, 600/601px must switch overlay/rail, and
900/901px must switch the roadmap canvas and ADR editor between their explanatory wide-only note and
content. A local fake SSE stream supplies deterministic drop, backoff, replay overlap, seq dedupe and
resume evidence without contacting a Fleet or provider.

The pre-flight also has an independent `#/delivery/readiness` operator view. Project, Generation and
Milestone filters are enforced by the server; each non-terminal record shows its next transition,
the authoritative blocker codes/messages and repair links, plus explicit refresh state. The same
view runs a deterministic, read-only whole-record consistency scan for orphaned planning records,
Milestones without Design Versions, missing/cyclic Version dependencies, unfinished Milestones in
sealed Generations and Gates with zero checkboxes. Findings are classified as blocking error,
warning or informational, capped, stable-coded, and evaluated without modifying planning data.

Planning-record search is SQLite FTS5, not an in-process scan of the Delivery snapshot. A versioned
backfill builds `delivery_search` for existing databases; triggers then keep requirement/version,
design, milestone, case, bug, job-event and audit rows synchronized through edits, retention deletes,
trash and restore. `unicode61` tokenization supports Unicode text. The query boundary accepts bounded
plain text only, converts it to quoted tokens, binds MATCH/scope/limit parameters, caps results, and
uses title-weighted BM25 plus deterministic tie-breaks. Snippets mark matches with text delimiters
that the UI escapes. Job arguments, audit attribution/request ids, Worker metadata and internal scope
keys are neither indexed nor returned; an explicit workspace/project scope is an exact filter.

## 3. The web drafting assistant

[The distinction](#the-distinction) below is the boundary this part of the chapter is built on. The
Delivery console had no assistant; the surfaces that looked like one answered with string templates,
and one of them wrote the operator's own words into a persisted requirement. Making the assistant
real means anvil-web calling a model provider, and the rule that says "no LLM HTTP" in chapter
[23](23-yaml-config.md) has to be either respected or explicitly amended. Amending it silently would
be worse than either.

### The distinction

| | Worker execution (0.11–0.39) | Web drafting (this part) |
|---|---|---|
| Who calls the model | the worker, inside a job | anvil-web, inside a request |
| What it may do | run tools, edit files, run commands | emit text for one field |
| Lifetime | a conversation with history | one request, no state |
| Credential | gateway config + job env | anvil-web's own `assist` block |
| Failure blast radius | the job | nothing: a proposal is refused or discarded |

A drafting call has no tools, no filesystem, no conversation, and no ability to write. It returns text
that a person accepts or discards.

### The contract

`POST /api/v1/delivery/assist`

```json
{ "surface": "requirement_version",
  "field": "success",
  "instruction": "make done testable",
  "target": { "type": "requirement_version", "id": "eng_1",
              "major": 0, "minor": 45, "patch": 1 },
  "cites": [{ "type": "requirement_version", "id": "eng_1",
              "major": 0, "minor": 45, "patch": 1, "field": "goals" }] }
```

→

```json
{ "available": true,
  "model": "…",
  "proposals": [
    { "field": "success", "label": "Completion criteria",
      "before": "old text", "value": "- [ ] …", "rationale": "…" } ] }
```

Three properties are enforced **server-side**:

1. **Structured patches, not prose.** A response that is not `{proposals:[…]}` is a `502`, not a
   fallback. Accepting prose is how the old mock wrote a sentence into an ADR.
2. **A per-surface field allowlist.** A proposal naming a field the surface does not own is refused
   before it reaches the client. The client cannot be trusted to limit where a patch lands.
3. **Nothing is applied here.** The endpoint proposes; the operator accepts; the surface saves through
   its own `PATCH`. A suggestion becomes a record only through an explicit act.

4. **Citations are references, never client-composed context.** The server resolves stable record and
   field references from the Delivery store and also obtains `before` from `target`. The old
   `cites:[]string` and client `current` shapes are rejected. Citeable fields are an explicit,
   non-sensitive allowlist: requirement prose, ADR sections, design-version Gate/architecture/class,
   and generation title/summary. Worker metadata, project/workspace identifiers, relative paths,
   statuses, audit data and files are never prompt context.
5. **Scope and budget are server-side.** A cite must share the target's project/engineering
   requirement and generation (a generation target accepts only that generation). Resolution
   preserves request order and allows at most 8 cites, 4 KiB each and 16 KiB total. A missing/deleted
   reference is `404`, a malformed/unknown field is `400`, a cross-project/generation reference is
   `403`, and an excessive request is `413`. Path-shaped ids are invalid; the resolver never opens a
   path.

### Off is stated, not faked

With no `assist` block configured, the endpoint answers `available:false, reason:"AI assist is not
configured"`. It never produces a canned reply. Seven surfaces that answered with a template is the
reason this rule exists.

### Configuration

```yaml
assist:
  base_url: http://127.0.0.1:8080/v1   # OpenAI-compatible root
  model: private/draft
  api_key_env: ANVIL_ASSIST_API_KEY   # name, never the key
  timeout_sec: 60
```

The key is read from the environment. It never appears in the config file, on the wire, or in a log.

## 4. Workbench approval channel

Workbench uses the existing scheduler job stream; it does not introduce a second agent transport.
Only a job carrying `approval_session_id` is interactive. agentd installs the SDK permission callback
for that job and emits `approval_request` as a `job.event`, with a stable request id, tool/arguments,
preview, accepted decisions, and absolute expiry. The matching `approval_result` closes the card.

The response path is `anvil-web → POST /v1/jobs/{job}/approval → scheduler → owning agentd`.
Scheduler routes from its live job ownership map; agentd settles only an exact
`job_id + approval_session_id + request_id` match and acknowledges the result. Unknown, duplicate,
expired, cross-session, and post-cancel responses fail closed. Stop releases pending callbacks as
denials. `allow_session` is scoped to the Workbench session plus tool and optional preview path.
Edited arguments are accepted only as bounded valid JSON and replace the SDK call arguments.

Phase/campaign jobs do not opt into the browser gate. They load project permission rules, hooks and
MCP configuration at the acquired Worker's checkout. An explicit allow rule can authorize a gated
call; anything still undecided is denied immediately. An unattended run therefore never silently
bypasses a prompt and never waits forever for a browser that is not attached. They appear in
Workbench read-only, using the same renderer: the live job fan while running and persisted events
after completion.

## 5. Production wiring and restart contract

p_0.45.15 corrects the earlier assumption that the existence of scheduler, daemon and Workbench
pieces meant the production path was connected.

The production chain is:

`anvil-web → schedclient → scheduler owner map → cluster.assign → acquired agentd Worker
→ EmbeddedRunner.RunOn(LLMFor(workerID)) → SDK → configured provider/tools`.

Start, stream, terminal result, approval and cancel all use that authenticated chain. Cancel is
remote-first: web changes local context/business projection only after the owning scheduler accepted
the cancel. Retries after a terminal state are idempotent. A Workbench session is owned by its web
user; resume accepts a prior Workbench session id, not an arbitrary SDK AgentID, and atomically claims
the durable mapping for the same user and Worker.

Project runtime configuration is read from the selected checkout only:
`.anvil/permissions.json`, `.anvil/hooks.json`, and project MCP configuration. User-level settings
are intentionally not loaded by agentd. Config parse failure fails job creation; hook command
execution remains disabled unless daemon policy explicitly enables it. Persisted Job events are
bounded and secret-shaped argument fields are redacted. Provider keys remain environment/config
secrets and are not written to Worker or Job state.

Scheduler node connections, job waiters, approval callbacks and cancel routing are leases, not
durable continuations. After scheduler restart nodes reconnect automatically, but pre-restart jobs
and approvals are reported unavailable and must not be represented as live. Workspace join tokens
must be registered again by their owning web control plane. Dynamic LLM state returns to the
configured bootstrap source; API key values are never persisted by the scheduler. Worker desired
state and non-secret per-Worker LLM fields persist under the daemon sandbox; a daemon restart reloads
them, while jobs that were in progress terminate and their web-side stream records an error.

### Non-goals (assistant)

- Streaming the draft. A patch is small; a spinner is enough.
- Tool use, multi-turn refinement, or planning. One request, one set of proposals.
- Letting the model choose its own model or prompt. Both are server-side, so a client cannot ask for
  something the deployment did not configure.
- Autonomy. No path here writes without an acceptance.

## Non-goals (chapter)

- A framework rewrite of the console. The current shape is fine once it is modular and tested.
- Light theme, theming system, i18n.
- Real-time multi-user collaboration or presence.
- Replacing the canvas drag editor.
- Scheduler/agentd changes beyond the minimum Workbench approval response channel; no Delivery
  schema change.
- An IDE in the browser: no diff review, hunk acceptance, checkpoints or notebook editing. Those belong
  to the VS Code client.

## Acceptance

1. `grep -rn "alert(\|confirm(\|prompt(" source/internal/web/static/js` returns nothing.
2. Every destructive action names what it removes and requires explicit confirmation before the
   request is sent.
3. A navigation performed while a mutation is in flight produces no paint into the new route.
4. Every job-enqueuing button is single-flight: a double click enqueues exactly one job.
5. With `assist` configured, a request for tighter completion criteria returns proposals which, when
   applied, change exactly that field and nothing else.
6. With no configuration, every assistant surface says it is not configured and writes nothing. No
   `(mock)` string is reachable.
7. A proposal for a field outside the surface's allowlist is refused by the server with a message
   naming the refusal.
8. The store is byte-identical before and after an assist call (`TestAssistNeverWritesToTheStore`).
9. A provider that returns prose produces an error, not an empty proposal list: "no suggestions" and
   "the model ignored the contract" are different statements.
10. A job that ran last week can be opened and read: its events, its error and its duration.
11. At 375 px an operator can see fleet state and stop a running job, and a dropped connection is
    visible and resumes without losing or duplicating an event.
12. `TestProtocolTour` runs on every push; `TestLiveProtocolSmoke` runs only under the `livesmoke` tag.
13. The first-run checklist can be closed and reopened, restores that preference per browser, and
    recomputes progress from server state after reload; clearing browser data reopens it without
    losing server-derived completion.
14. `#/delivery/readiness` uses the transition evaluator enforced by mutations and exposes the
    bounded whole-record consistency scan with scope-safe filters and repair navigation.
