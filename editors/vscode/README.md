# Anvil for VS Code

The editor surface for the Anvil agent: a chat panel on the activity bar that
streams a turn, shows every tool call as a card, and stops for your approval
before anything writes to the workspace.

This extension holds no agent logic. It spawns the `anvil-host` sidecar — a
stdio program built from the Anvil Go SDK — and renders what that process
reports. The protocol between the two is chapter 35 of the design docs
(`doc/design/v1.0/35-vscode-extension.md`); the TypeScript mirror of it is
`src/protocol.ts`.

## Installing a packaged build

Download or build the VSIX for your platform and install it:

```bash
code --install-extension anvil-vscode-0.35.7-win32-x64.vsix
```

Nothing else is needed: the `anvil-host` sidecar for that platform travels
inside the package, so a machine with no Go toolchain runs it. Reload the
window, open a folder you trust, run **Anvil: Set API Key**, and open the Anvil
icon in the activity bar.

## Running it from source

```bash
npm install
npm run compile
npm run build:host   # builds source/cmd/anvil-host into bin/
```

Then press <kbd>F5</kbd> (or the `Run Extension` launch config) to open a second
VS Code window with the extension loaded, and open the Anvil icon in the
activity bar. `Run Extension (trusted workspace, no reload)` opens that window
straight onto this repository, which is the quicker loop if you are working on
both sides at once.

The sidecar is found in this order, and the first hit wins:

1. **`anvil.hostPath`**, when it is set — a path you chose, checked against
   nothing, so a host you built yourself always works.
2. **The bundled binary**, `bin/anvil-host[.exe]` inside the extension. This is
   the packaged case and the F5 case, and it is the only source that is verified
   before it runs (see *Provenance*).
3. **`anvil-host` on `PATH`** — for a build you installed yourself. A missing
   binary fails with the spawn error and a note about which of the three was
   being looked for.

## Packaging

```bash
npm run package:vsix                 # this machine's platform
npm run package:vsix -- --target linux-x64
npm run package:all                  # one VSIX per target, in sequence
npm run smoke:vsix -- --vsix anvil-vscode-0.35.7-win32-x64.vsix
```

Targets: `win32-x64`, `win32-arm64`, `linux-x64`, `linux-arm64`, `darwin-x64`,
`darwin-arm64` — the same list `src/hostBundle.ts` resolves against, so a
package can only be built for a platform the extension knows how to find a host
on. Each VSIX carries exactly one sidecar, cross-compiled with `CGO_ENABLED=0`
by `scripts/build-host.mjs`; the platform has to be chosen at package time
because nothing in an installed extension says which machine it will run on.

A packaged VSIX contains the compiled JavaScript, the media, the README, and
`bin/` — never `src/`, the tests, the scripts, or the workspace's own `.anvil/`
state.

### Provenance

Building the sidecar writes `bin/hosts.json` beside it: the file name, its
sha256, its size, the Go toolchain, the protocol revision, and when it was
built. At spawn the extension hashes the bundled binary and refuses to run it if
the digest is not the one recorded — the check is skipped for a configured path
or a `PATH` lookup, where there is nothing truthful to compare against.

That is a check on the package, not a signature: it catches a package that was
damaged, half-replaced, or assembled from a stale `bin/`, which are the ways
this actually goes wrong. It is not a defense against someone who can write to
the installed extension directory — they can rewrite the manifest too. What it
does mean is that the binary that runs is the binary that was built with the
JavaScript that is running it.

`initialize` then asserts the protocol revision on the wire, and the host's
version banner is compared against the extension's release line; a mismatch
names both sides and stops rather than failing later inside a tool call.

## Troubleshooting

| Symptom | Cause and fix |
| --- | --- |
| `could not start the Anvil host: … (no bundled host for this platform…)` | The VSIX is for another platform. Install the one matching your OS and arch, or build `source/cmd/anvil-host` and set `anvil.hostPath`. |
| `the bundled Anvil host … does not match the checksum recorded when it was built` | The installed package changed after it was built. Reinstall it, or point `anvil.hostPath` at a binary you built. |
| `… speaks editor-host protocol N; this extension speaks M` | The sidecar and the extension are from different revisions. Reinstall the extension; if you set `anvil.hostPath`, point it at a matching build. |
| `Anvil refuses to run in an untrusted workspace.` | Expected. Trust the folder (**Workspaces: Manage Workspace Trust**) — the extension will not start an agent that can edit and execute in a folder you have not trusted. |
| The panel opens but nothing streams | The provider refused the run; the panel says which and why. Check **Anvil: Set API Key** and `anvil.baseUrl` / `anvil.model`. |
| Nothing happens in a folder with no files opened | The host's working directory is the workspace folder, so a single-file window has none. Open a folder. |

## Configuring a model

| Setting | Meaning |
| --- | --- |
| `anvil.provider` | `openai` or `anthropic` |
| `anvil.model` | Model id; empty uses the host's default |
| `anvil.baseUrl` | OpenAI-compatible base URL; empty uses the provider default |
| `anvil.maxSteps` | Step limit per turn; `0` uses the host's default |
| `anvil.store` | `jsonl`, `sqlite`, or empty for the host's default |
| `anvil.vision` | Accept pasted images; off by default |
| `anvil.rules` | Load `AGENTS.md` and `.anvil/rules/*.md`; on by default |
| `anvil.cursorRules` | Also load `.cursor/rules/*.md|.mdc`; on by default |
| `anvil.hostPath` | The sidecar to spawn; empty uses the bundled one, then `PATH` |

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
- **Tells you what it read.** The panel reports which rule files this workspace
  loaded — `AGENTS.md`, `.anvil/rules/*.md`, and optionally `.cursor/rules/*` —
  and says so plainly when one was skipped or cut short by the size budget.
- **Keeps the conversation across a mode change.** `ask`, `accept edits`,
  `plan`, and `bypass` are session options in the SDK, so switching one closes
  the session and re-opens it under the same agent: same history, new mode. The
  mode is remembered per workspace. `bypass` is flagged in the panel, and a
  mode change is refused while a turn is running.
- **Ships its own sidecar.** A platform VSIX carries the `anvil-host` binary it
  was built with, verified against the checksum recorded at build time before it
  is run. The API key is not in the package: it is in VS Code's SecretStorage,
  and from there in the child process environment.
- **Does not have transcript replay or checkpoints yet.** A resumed session
  continues the agent's real context, but the SDK exposes no transcript reader,
  so the panel marks the resume instead of printing a history it does not have.
  See `doc/roadmaps/v1.0/m_0.35/`.

## Commands and sessions

Type `/` in the composer for the catalogue. Built-ins:

| Command | What it does |
| --- | --- |
| `/plan [request]` | Switch to plan mode; anything after it is sent as the request |
| `/model [id]` | Choose the model this conversation uses |
| `/resume [agent id]` | Continue a past session in this workspace |
| `/clear` | Start a new conversation |
| `/help` | List the commands |

Skills in the workspace (`.anvil/skills/<id>/SKILL.md`) are listed as commands
of their own: running one turns that skill on for the conversation. `@`
references a file or folder for the next prompt.

## Tests

```bash
npm test        # compile, lint, build the host, run everything
npm run test:unit
npm run test:integration
npm run smoke:vsix -- --vsix <file>   # open a built VSIX and run the host it carries
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
of the `vscode` boundary. `src/commands.ts` and `src/hostBundle.ts` are split
that way on purpose: they decide what a typed line means and which binary to
run, and `chatPanel.ts` / `hostManager.ts` only carry those decisions out.

The packaging step has its own check, since it is the part no unit test can
reach: `npm run smoke:vsix` extracts a built VSIX to a temporary directory,
asserts what is and is not inside it, verifies the binary against the manifest
it shipped with, and — on a package for this machine — runs that binary out of
the extracted tree and completes the handshake. A package that only works from
the directory it was built in fails it.
