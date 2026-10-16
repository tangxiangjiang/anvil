# Lessons learned

Short notes that should influence how we drive Anvil milestones. Prefer linking from phase docs over duplicating long narratives.

## 2026-09-30 — oneshot asked to implement p_0.2.1

**Setup:** `./bin/oneshot.exe . "那你帮我做下p_0.2.1的功能"` after MVP worked for Q&A / file edits.

**What happened:** Agent explored docs/code, started `internal/store` + `Resume` wiring, then hit `max steps exceeded (32)` and exited with startup/run error. Working tree left a partial but directionally correct implementation.

**Takeaways for later phases**

| Lesson | Implication |
|--------|-------------|
| Oneshot ≠ milestone delivery | Use Cursor (or a future interactive CLI) for multi-file phases; oneshot for smoke / small prompts |
| Cap steps vs task size | Raise `MaxSteps` or split tasks when using the SDK on large changes; document defaults |
| Always review agent diffs | Check tests, phase Acceptance checkboxes, ID consistency, silent error swallows |
| Rules are not automatic for oneshot | If we want phase discipline inside Anvil runs, encode it in system prompt / tools, not only `.cursor/rules` |
| Partial agent code is salvageable | Prefer “clean up + test” over discard when structure matches design |

See also: [m_0.2/p_0.2.1.md](m_0.2/p_0.2.1.md) notes section.

## 2026-10-16 — the first install of the VSIX found a panel layout bug

**Setup:** `code --install-extension anvil-vscode-0.35.7-win32-x64.vsix`, then one real session in the chat panel.

**What happened:** the panel rendered a long transcript as evenly spaced slivers with no scrollbar. Every transcript item was a flex item in a column flex container, so it shrank instead of the container overflowing — and tool cards set `overflow: hidden`, which sets their automatic minimum size to 0, so they could shrink to a couple of pixels while the flex `gap` between them stayed 10px. 115 extension tests, `tsc`, `eslint`, `go test ./...` and the packaging smoke test were all green throughout: none of them render anything.

**Takeaways for later phases**

| Lesson | Implication |
|--------|-------------|
| The editor client is the one surface with no automated rendering check | For panel work, the reviewable artefacts are the DOM skeleton and the stylesheet's invariants — write them down in the design chapter, not only in the CSS |
| A stylesheet rule can be tested without a browser | `test/unit/chatLayout.test.ts` parses `media/chat.css` and asserts the scroll-container and height-chain invariants; it fails against the pre-fix file |
| `overflow: hidden` + flex column is a trap | Any flex item that clips its own content has a minimum size of 0; a scroll container must stop its children shrinking (`flex: 0 0 auto`) |
| Install the package early | This was found in the first five minutes of the manual pass; the longer a packaged build goes uninstalled, the more that pass has to find at once |

See also: [m_0.35/p_0.35.7.md](m_0.35/p_0.35.7.md), [35-vscode-extension.md](../../design/v1.0/35-vscode-extension.md).

## 2026-10-16 — moving the chat from a sidebar view into an editor tab

**Setup:** p_0.35.8 rebuilt the extension's three surfaces: the conversation became a `WebviewPanel` instead of a `WebviewView`, the activity bar became a session tree, and the composer became a floating card.

**What happened:** nothing failed in a new way — but four things about a webview that is *created on demand and reused* only showed up because the design was read back before the code was written, and none of them are visible in a passing test suite.

**Takeaways for later phases**

| Lesson | Implication |
|--------|-------------|
| A webview message sent before the document's listener exists is dropped, silently | `post()` has to queue until the document announces `ready`, and flush the queue *before* the re-announcement or messages arrive out of order. Opening a panel and posting into it in the same tick is this bug, every time |
| A closed tab is not a closed session | Splitting a view from what it displays means the close/reopen path becomes a real state to design (`retainContextWhenHidden`, `onDidDispose`, re-announce on `ready`), not a lifecycle detail |
| Reusing one webview for a second subject needs an explicit reset | Pointing the same document at another agent left the old messages, cards and index maps in place — a `reset` message that empties the DOM *and* the maps, posted before the new state, is the fix |
| A CSS variable spelled two ways fails silently | The distance the script measures and the distance the stylesheet reserves are the same name; a rename on one side leaves the reserve at its default and hides content behind the card. `test/unit/chatLayout.test.ts` checks it across both files, and the check was confirmed non-tautological by breaking it on purpose |
| A variable set for a sibling does not inherit | The transcript is the dock's sibling, so the measured height has to be written on `documentElement`; setting it on the dock reaches nothing. Worth knowing before debugging the CSS |
| A regenerable temp directory must not fail a check | `smoke:vsix` printed OK and then exited 1 on `EPERM` deleting the `anvil-host.exe` it had just run. A green check that reports failure trains you to ignore the exit code |
| A defect fixed after review is not a lesson until it is guarded | Where the p_0.35.7 bug became a stylesheet invariant, this pass's invariants are the layout's shape: out of flow, measured once, the variables agreed across files |

See also: [m_0.35/p_0.35.8.md](m_0.35/p_0.35.8.md), [35-vscode-extension.md](../../design/v1.0/35-vscode-extension.md).

## 2026-10-16 — rendering a Markdown reply while it is still arriving

**Setup:** p_0.35.9 typesets the assistant's reply and streams it. There is no incremental Markdown renderer to borrow, so the transcript freezes every block except the last and re-parses the remainder on each frame.

**What happened:** the scheme is only sound if a block can be extended *solely* by the source that immediately follows it. That prerequisite is invisible in the code and easy to state and easy to break: a bare `-` on its own line parsed as a paragraph, so the next character turned it into a list and a block that had already been frozen — and drawn — changed underneath the renderer. A test that parses every prefix of a document and checks that no frozen block changed found it; reading the parser would not have.

**Takeaways for later phases**

| Lesson | Implication |
|--------|-------------|
| A frozen-prefix scheme needs a test that grows the input one character at a time | Freeze-and-reparse passes on every whole document and fails on prefixes. Walk each prefix, re-run the exact cycle the renderer runs, and assert the frozen part is unchanged — that test is the contract, not the parser |
| A blank line is not a parse boundary | An indented code block continues across one, a blank line inside an open fence would split the code, `- a\n\n- b` is one loose list, and an item's second paragraph has a blank line inside it. "Seal the block on a blank line" is the intuitive rule and it is wrong four ways |
| A field's name is not a slot | `start` was the block's offset and the ordered list's first number went into it too, which broke re-parsing for every list not starting at 1. Where two numbers nearly coincide, the near-miss is the bug; the re-parse test found this one as well |
| A decode field on a struct shared with the request encoder is a leak | `openAIMessage` encodes requests *and* decodes responses, so giving it a `reasoning` field is how chain-of-thought starts being echoed upstream. The fix cost nothing to keep small — decode-only deltas live in the streaming struct — and a test asserting the serialized request body has no `reasoning` key keeps it that way |
| Reasoner output is a firehose, and a dropped event is permanent | `Run.emit` drops silently when its buffer fills, and the transcript is append-only, so a dropped *text* chunk corrupts the turn. Coalesce reasoning by size (200 chars) at the source rather than forwarding one event per token |

See also: [m_0.35/p_0.35.9.md](m_0.35/p_0.35.9.md), [35-vscode-extension.md](../../design/v1.0/35-vscode-extension.md).

## 2026-10-16 — a deny that has to hold when nobody is there

**Setup:** p_0.36.1 adds a user-authored rule file whose `deny` must hold even with no approval
channel: a library embedder that installs no callback, the CLI, and `bypass`.

**What happened:** the SDK says it in one line — *groups alone open no gate: with no
`PermissionFunc` installed there is nobody to ask, and `Execute` keeps running every tool* —
and that line decides where policy may live. Implemented in the callback, a `deny` is not a
deny; it is a request to a component that may not exist. Reading the existing comment was the
whole design. The same sentence, applied to a different mechanism, then showed that the file
hooks' shell/write/edit denies had been living inside the composed callback: correct until the
day a rule `allow` short-circuits the callback, and then silently skipped. Moving them to
`preExec` also *defined* the precedence order, rather than the order being asserted in a doc
and true nowhere.

**Takeaways for later phases**

| Lesson | Implication |
|--------|-------------|
| A policy implemented inside an optional callback is not a policy | `Options.Permission` is optional, so anything that must hold without a client belongs in `Execute` (or `preExec`), before the callback. Ask "who runs this when the callback is nil?" of every new check |
| Two checks at different stages are the reason a precedence list is real | The moment hook denies moved to `preExec`, "hook deny > rule deny" stopped being a claim in a table and became a property of the code path. If the order cannot be read off the call graph, it is documentation, not design |
| A feature that was never wired is indistinguishable from one that does not work | `.anvil/hooks.json` deny rules were implemented, unit-tested and documented, and loaded by **neither** client, because loading hung off `SettingSourceProject` and no client set it. A unit test that constructs the SDK by hand cannot see this; the test has to go through the client's own option-building path |
| A rule that admits a call must be provable from what the user was shown | Matching facts come from the preview, so a read — which names no target path on its card — cannot be matched by path. The honest move was to document the boundary and keep the tool-name rule, not to match raw argument JSON that no card displays |
| An analyzer that refuses everything unusual will refuse the ordinary | "No unquoted brace metacharacter" made `find . -exec rm {} +` unprovable, and find's `{}` is not brace expansion — bash expands a brace only with a comma or a range. The escape hatch had to be exactly `{}` as a whole token, because `{-delete,}` *would* expand and rewrite a later argument, and a matcher comparing the line as written would then match something the shell never runs |
| A token prefix is the only safe prefix | `git status:*` must not admit `git status && rm -rf /` nor `git statusx`, and `strings.HasPrefix` admits both. Split, lex, compare argv — and require a single segment, so a compound line can never match a prefix rule |

See also: [m_0.36/p_0.36.1.md](m_0.36/p_0.36.1.md), [36-tool-permissions.md](../../design/v1.0/36-tool-permissions.md).

## 2026-10-16 — the button that writes the rule

**Setup:** p_0.36.2 puts the rule table on the approval card: "always allow" becomes the rule
shapes that would decide the call in front of the user, written into
`.anvil/permissions.json` through two new methods.

**What happened:** the interesting failures were not in the decision — p_0.36.1 had already
made that deterministic — but in the conversation around it. A rule the card writes is a
*claim about the future*, so the client must not be the one making it: the options come from
the host, each already evaluated against the very call being shown, and the host answers the
approval itself only after re-reading the file and finding that an allow rule now decides it.
The card's job shrank to rendering three endings. The other half was that two languages share
one vocabulary with no generator between them: a renamed field in Go is an `undefined` in
TypeScript, a `kind` the host invents is a button with no label, and both are silent — which
is why the check is a test that reads both sides as text, not a comment asking for care.

**Takeaways for later phases**

| Lesson | Implication |
|--------|-------------|
| A button that writes a rule which does nothing teaches that rules do not work | Every option offered was evaluated against the call it is offered for; a shape that would not decide it is not offered at all. One bad button is worse than no button, and this is the case users would conclude the whole feature is broken from |
| "Always allow" is a promise about the *next* call, and it can only be kept by reading again | The rule set is composed per call from the file as it now stands, not captured at session start. Same reason a hand edit takes effect under a running session — and the reason a rule from the card needs no session rebuild |
| An interface with three outcomes must render three outcomes | Approved / written-but-outranked / not written. The middle one is the one a client gets wrong by assuming: `decided_by` is what says the file changed and the call did not |
| A label derived from a pattern is the pattern grammar implemented a second time | In the least testable place available (a webview). The host sends `kind`; the card maps it to words; `test/unit/permissionKinds.test.ts` reads both files and fails if they drift |
| Two languages, one vocabulary, no generator | A renamed field is a silent `undefined`. A test that reads both sides as text is cheap, and this repository has been bitten by exactly this before |
| A file under edit is a state, not a failure | `permissions.get` reports a malformed file as a result with a place (so the editor can put a squiggle on it), while a malformed file *at session start* stays a hard error. Different question — "what does my file say" vs "what policy am I governed by" — so different answer |
| A new public method can be the cheaper fix | The host may import the public `anvil` package only, so the client-side glue became public wrappers (`LoadPermissionRules`, `AddPermissionRule`, `EvaluatePermissionRules`) over the same evaluation. The alternative — a second evaluator in the host — is how an explanation starts disagreeing with the decision it explains |
| The extension version and the host banner move together | The drift check compares *minor* lines, so bumping `package.json` alone would warn on every spawn. Two version strings, one release line: bump them in the same commit |

See also: [m_0.36/p_0.36.2.md](m_0.36/p_0.36.2.md), [36-tool-permissions.md](../../design/v1.0/36-tool-permissions.md), [35-vscode-extension.md](../../design/v1.0/35-vscode-extension.md).
