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
