# Anvil for VS Code

The editor surface for the Anvil agent: a chat panel on the activity bar that
streams a turn, shows every tool call as a card, and stops for your approval
before anything writes to the workspace.

This extension holds no agent logic. It spawns the `anvil-host` sidecar — a
stdio program built from the Anvil Go SDK — and renders what that process
reports. The protocol between the two is chapter 35 of the design docs
(`doc/design/v1.0/35-vscode-extension.md`); the TypeScript mirror of it is
`src/protocol.ts`.

## Running it

```bash
npm install
npm run compile
npm run build:host   # builds source/cmd/anvil-host into out/
```

Then press <kbd>F5</kbd> (or the `Run Extension` launch config) to open a second
VS Code window with the extension loaded, and open the Anvil icon in the
activity bar.

The host binary is looked up as `anvil.hostPath` — `anvil-host` on `PATH` by
default, or a path relative to the workspace folder. `Run Extension (trusted
workspace, no reload)` opens that window straight onto this repository, which is
the quicker loop if you are working on both sides at once.

## Configuring a model

| Setting | Meaning |
| --- | --- |
| `anvil.provider` | `openai` or `anthropic` |
| `anvil.model` | Model id; empty uses the host's default |
| `anvil.baseUrl` | OpenAI-compatible base URL; empty uses the provider default |
| `anvil.maxSteps` | Step limit per turn; `0` uses the host's default |
| `anvil.store` | `jsonl`, `sqlite`, or empty for the host's default |
| `anvil.hostPath` | The sidecar to spawn |

The API key is **not** a setting. Run **Anvil: Set API Key**; it goes into VS
Code's `SecretStorage` and from there into the child process environment
(`ANVIL_API_KEY`). It is never written to a settings file, an output channel, or
a log line.

## What it will and will not do

- **Refuses untrusted workspaces.** The extension declares
  `untrustedWorkspaces.supported: false`, and `HostManager.assertTrusted()`
  throws before any spawn — so a folder you have not trusted cannot run the
  agent, whatever the UI does.
- **Asks before mutating calls.** `write`, `edit`, `shell` and `verify` raise an
  approval prompt by default; so does any tool the host adds from outside the
  SDK (MCP, plugins). The prompt shows a preview the host computed — the argv
  for a command, the diff for a file, a summary otherwise. (Rendering the diff
  inside the prompt is not the same as the diff-review surface, which is
  p_0.35.5.)
- **Kills its child.** One host per window, stopped on deactivate, on a folder
  change, and on a settings change (the host reads its configuration at spawn).
- **Has no `@` context, image paste, diff review, session history, plan mode,
  slash commands, or packaging yet.** Those are later phases; see
  `doc/roadmaps/v1.0/m_0.35/`.

## Tests

```bash
npm test        # compile, lint, build the host, run everything
npm run test:unit
npm run test:integration
```

Two kinds of test live under `test/integration/`. Most drive
`test/fixtures/fake-host.mjs` — a scripted host that speaks the real protocol,
so the extension's client, session state machine, and rendering can be tested
without a key or a network. The rest drive the **real** `anvil-host` binary with
the model call routed at a port nothing listens on, which pins the Go side of the
wire; they are skipped when the sidecar has not been built.

`src/settings.ts`, `src/hostManager.ts`, `src/chatPanel.ts` and `src/extension.ts`
import `vscode` and are therefore not covered by these tests — they need an
extension host. Keep them thin; the behaviour worth testing belongs on this side
of the `vscode` boundary.
