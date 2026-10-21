# 40 — Auto mode, and a permissions manager

**Milestone focus:** an `auto` rung on the mode ladder and a structured rules surface →
**[m_0.41_permission_auto_and_manager.md](../../roadmaps/v1.0/m_0.41_permission_auto_and_manager.md)**.

Chapter [36](36-tool-permissions.md) gave a call three possible answers — allow, ask, deny — in a
rule shape strong enough to say *which command*, *which path*, and *which token prefix*.
Chapter [37](37-permission-modes.md) gave the layer above it five modes. Both are sound, and both
stop one step short of what a person actually uses:

- the ladder jumps from **accept edits** (writes are fine, every shell call asks) to **don't ask**
  (nothing asks, everything unapproved is refused). There is no rung for *run the harmless
  things, ask about the rest*, which is the mode most turns want;
- the rule shape is richer than the editor for it. A user who wants to change one rule is asked
  to open `.anvil/permissions.json` and know the schema; the approval card can write a rule but
  nothing can show, edit or remove one.

This chapter is the fix for those two, and nothing else.

## `auto` is a deterministic classifier

Claude Code's auto mode asks a model whether a call looks safe. Anvil's `auto` does not make a
model call. It classifies with the **same facts a rule matches on** and the **same shell analyzer
the `readonly` pattern already trusts**, in a pure function:

```go
func AutoApproveCall(tool string, args json.RawMessage) bool
```

| The gated call | `auto` decides |
|---|---|
| `shell`, and `shellcmd.Analyze` proves the whole line read-only | allow |
| `shell`, anything else — mutating, unlexable, an expansion, a redirection, several commands with one unknown | ask |
| `write_file`, `edit_file`, `apply_patch` | allow |
| `run_tests`, `build` | allow |
| `web_*`, `fetch_url`, MCP tools, plugin tools, anything unknown | ask |

Three things about that table are deliberate.

**It classifies on `CallFacts`, not on the preview.** `AutoApproveCall` reads the tool name and its
arguments through `tools.CallFactsFor` — the one function the registry uses to decide rules. The
sentence *"auto allowed this"* and the sentence *"rule 3 allowed this"* therefore rest on identical
evidence. A classifier reading the approval preview instead would be classifying a rendering, and
the two could disagree the first time a preview was truncated.

**`write` and `edit` are safe because the sandbox says so, not because a diff looked small.** The
SDK refuses any path that resolves outside the workspace, before the tool runs, for every
path-taking tool ([08](08-security.md)). That guarantee is what `accept_edits` already leans on
when it drops `write` and `edit` from the gate; `auto` leans on the same one and adds the call to
the callback's allow set. Nothing here reads the file to form an opinion.

**`run_tests` and `build` are safe because the argv is not the model's.** The command comes from
`Options.Test` / `Options.Build`, which the operator configured; the model may append *validated
tokens* — no shell, no metacharacters (`verify.ValidateToken`) — to that fixed argv. There is no
command line for the analyzer to prove, because there is no shell. A project whose configured test
command is destructive is a project whose operator wrote a destructive command, and the mode is
not a second opinion about their own configuration.

### Why not a model classifier

A classifier call would be a second request per tool call, with four costs this design declines:

1. **Latency and money on the hot path.** Every `grep`, every `read_file`, every write would wait
   on an extra round trip. The gate only reaches the callback at all for groups this mode gates;
   the cheap path is the common path.
2. **A new injection surface.** The classifier would read tool arguments written by the model,
   and the model would learn that its prose can influence its own permission. The deterministic
   classifier reads only what the tool would do.
3. **No explanation.** "The classifier said so" is not a sentence a user can act on. "The analyzer
   proved the whole command non-mutating" names the rule shape they can write down themselves.
4. **It fails closed into asking, which is the mode's normal state anyway.** An uncertain
   classifier is only ever as good as `ask`, and `ask` needs no model.

The divergence is stated here rather than hidden: `auto` is *not* Claude's classifier, and the
README says so. It is the same promise — routine work without a prompt — kept with evidence Anvil
already has.

### Rules outrank the classifier

A `deny` is decided in `Registry.Execute` before any callback exists, so `auto` cannot talk it out
of the way. An `ask` rule is a written statement about one tool whose only meaning is to
interrupt, so it opens the card even in a mode that otherwise classifies — exactly the resolution
`bypass` uses ([37](37-permission-modes.md#an-explicit-rule-outranks-bypass)). The callback's
order is therefore:

1. rule of effect `ask` → ordinary approval card;
2. `AutoApproveCall` says safe → allow;
3. otherwise → ordinary approval card.

The session allowlist stays inside the ordinary hook, so *"always allow for this session"* still
wins over the classifier on a call it covers.

### What `auto` gates

The default six groups, unchanged: `write`, `edit`, `shell`, `verify`, `web`, `mcp`. The mode
does not narrow the gate the way `accept_edits` narrows it, because the gate is what makes the
classifier reachable: a `write` call that is not gated never asks the classifier, and the mode
would silently become a no-op if the sandbox ever changed. Gating it and answering yes keeps the
decision in one place, with one explanation.

An explicit `permission_groups` still overrides the base set, for every mode except `bypass`.

## The registry and the ladder are the same table

`PermissionModes` becomes:

```
ask, accept_edits, auto, plan, dont_ask, bypass
```

`auto` sits after `accept_edits` because it is strictly more permissive — same gates, a callback
that says yes to proven-safe calls — and before `plan`, which is not a trust level but a
different kind of session. `permissionHookForMode` gains one case and `groupsForMode` gains none;
`TestEveryModeResolvesToAHook` keeps the hook list complete. A workspace may declare
`"defaultMode": "auto"` in its rule file, which `honouredMode` accepts without change — it is a
word in the ladder, and the file may declare it.

## The permissions manager

The manager is a **webview document**, not a QuickPick wizard. Editing rules is a list operation
with four fields per row, a validation result, and a `defaultMode` beside it; a sequence of input
boxes cannot show the file as a whole or let a person fix the third rule without re-answering the
first two.

### The host writes, the panel edits

The panel never touches the file. It sends the list it has on screen to a new RPC:

```
permissions.set  { rules: RuleSpec[], default_mode?: string }  → PermissionsResult
```

The host validates the list the way a reader would — by marshalling it into the file shape and
running the same `permissions.Parse` the session runs at open — then writes atomically through
the existing temporary-file-plus-rename path. A set that would not compile never reaches the
disk, and a policy that will not compile in the reader cannot be written by the writer.

The alternative — the extension using `workspace.fs` to write JSON — would need a second
implementation of the schema and of every refusal, and the two would drift. The host already owns
`permissions.get` and `permissions.add`; this is the third verb of the same table.

**0.47 names the file the verb writes.** `permissions.set` and `permissions.add` take an optional
`source` — `workspace`, which is the only meaning an older client has, or `user` — and one save
carries the fields of one file's shape only. The manager still edits through this one door, now
twice; [37](37-permission-modes.md) §*Scopes and protected paths* is the design and the panel's
two sections are described there.

### `defaultMode` is edited through the same door

The panel edits the declaration with a select of the ladder. The host refuses `bypass` and any
word the ladder does not have, reusing `honouredMode` / `refusedMode` so the panel, the session
result and `permissions.get` say the same sentence. An empty value removes the key rather than
writing `""` — absence is a state the file already distinguishes from a declaration.

### A file that will not parse is not editable here

`permissions.get` returns a `problem` and **no rules** for a malformed file. The panel therefore:

- shows the message and the place, and a link to the diagnostics the host already files;
- disables the list editor and Save — rewriting a file that was not read would discard a policy
  somebody is halfway through writing, which is the failure chapter 36 is arranged around;
- offers **Open JSON** as the only way forward, which is the existing entry point and stays.

A missing file is different: the panel renders an empty list and creates the file on the first
save, the same way `permissions.add` does.

Since 0.47 each layer holds this state on its own. A malformed user file disables the user
section and says so, while the workspace section stays editable; a missing user file is
read-only until a **Create user file** step, because a save that silently brought a machine-wide
policy into existence is not something a person should discover afterwards.

### What the panel shows

- the absolute path, and whether the file exists;
- a `defaultMode` select with *Workspace ladder floor* as the empty entry;
- one row per rule: **effect** (allow / ask / deny), **tool** (a name or glob), **pattern**
  (meaning follows the call: a command, a path glob, or a tool glob), **reason**, and the
  one-based number a denial will quote;
- *Add rule*, *Delete* per row, *Save*, *Reload*, *Open JSON*;
- the same one-line reminder the old entry point gives when `anvil.permissions` is off: the file is
  not read, so what is edited decides nothing.

Rendering is `textContent` throughout. Rules are user text, and the panel is a document like any
other in this client.

### Entry points

The composer's **Permissions** button and `Anvil: Open Permission Rules` both open the manager.
`Anvil: Open Permission Rules File` remains for hand editing and is where **Open JSON** goes. The
approval card's *always allow* still writes through `permissions.add`; the manager reads the same
file on its next open or **Reload**, so the two paths converge on one file rather than one cache.

## Protocol

One additive method and its result type. No `ProtocolVersion` bump, for the reason p_0.36.2 gave:
a method an older host does not know returns `MethodNotFound`, and the panel can say so; nothing
existing changes shape.

## Non-goals

- ~~A user-level or global rule file.~~ **Superseded by 0.47.** Anvil's rule file is a
  workspace's committed policy; a second scope does need a second precedence table, and
  p_0.47.6 wrote one — the user file, the severity-across-scopes merge and protected paths are
  in [37](37-permission-modes.md) §*Scopes and protected paths*.
- Reordering rules by drag. Order decides among equals; the panel can add and delete, and the
  *first rule wins within an effect* sentence stays true.
- An LLM classifier, or any model call from the permission path.
- Importing Claude Code's `settings.json` permission syntax. The shapes are not the same and a
  lossy translation would be worse than a sentence saying so.
- Managing MCP server permissions, hooks, or `permission_groups` from this panel. It edits
  `.anvil/permissions.json` and, since p_0.47.6, the user file beside it — one layer per save,
  never both at once.

## Acceptance

1. `auto` is selectable in the composer, appears in the ladder on both sides of the wire, and a
   workspace may declare it as `defaultMode`.
2. In `auto`, a read-only shell command and an in-workspace `write_file` run without a card; a
   mutating command, a web call and an MCP call still show one; an `ask` rule shows one in `auto`
   exactly as it does in `bypass`.
3. The classifier is a pure function tested against the same facts the registry decides on; it
   never reads the filesystem and never calls a model.
4. The manager lists, adds, edits and deletes rules and changes `defaultMode`; every save is
   validated by the reader and written atomically; a malformed file is reported, not overwritten.
5. `go test ./...`, `npm test`, packaging and the packaged smoke test stay green.
