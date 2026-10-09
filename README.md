# Anvil

Independent **local-only** Go Agent SDK. Capability-aligned with Cursor’s local agent API shape (`Create` / `Prompt` / `Send` / `Stream` / `Wait`), but runs your own tool loop against **OpenAI-compatible** or **Anthropic** HTTP APIs — not the Cursor runtime.

## Layout

| Path | Purpose |
|------|---------|
| [`doc/design/`](doc/design/) | Versioned design (current: [`v1.0/`](doc/design/v1.0/)) |
| [`doc/roadmaps/`](doc/roadmaps/) | Versioned milestones (current: [`v1.0/`](doc/roadmaps/v1.0/)) |
| [`source/`](source/) | Go module (`github.com/anvil-dev/anvil`) |

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

| Milestone | Doc |
|-----------|-----|
| 0.1 MVP | [`m_0.1_mvp.md`](doc/roadmaps/v1.0/m_0.1_mvp.md) |
| 0.2 Resume / Cancel / interactive | [`m_0.2_resume.md`](doc/roadmaps/v1.0/m_0.2_resume.md) |
| 0.3 Extensibility | [`m_0.3_extensibility.md`](doc/roadmaps/v1.0/m_0.3_extensibility.md) |
| 0.4 Coding UX (stream / patch / rules) | [`m_0.4_coding_ux.md`](doc/roadmaps/v1.0/m_0.4_coding_ux.md) |
| 0.5 Operator (List/Get / usage / eval) | [`m_0.5_operator.md`](doc/roadmaps/v1.0/m_0.5_operator.md) |
| 0.6 Scale (progress / MCP HTTP / SQLite / CLI) | [`m_0.6_scale.md`](doc/roadmaps/v1.0/m_0.6_scale.md) |
| 0.7 Streaming & errors (design 07) | [`m_0.7_streaming_errors.md`](doc/roadmaps/v1.0/m_0.7_streaming_errors.md) |
| 0.8 Security (design 08) | [`m_0.8_security.md`](doc/roadmaps/v1.0/m_0.8_security.md) |
| 0.9 Extensibility depth (design 09) | [`m_0.9_extensibility.md`](doc/roadmaps/v1.0/m_0.9_extensibility.md) |
| 0.10 Cursor SDK local parity (design 10) | [`m_0.10_sdk_parity.md`](doc/roadmaps/v1.0/m_0.10_sdk_parity.md) |
| Index | [`doc/roadmaps/v1.0/README.md`](doc/roadmaps/v1.0/README.md) |

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

### SDK local parity (0.10)

Surface alignment with Cursor SDK *local* knobs (not Cursor runtime quality). See [`doc/design/v1.0/10-sdk-parity.md`](doc/design/v1.0/10-sdk-parity.md).

```go
Local: anvil.LocalOptions{
  CWD:            ".",
  SettingSources: []anvil.SettingSource{anvil.SettingSourceProject}, // .cursor/mcp.json + hooks
}
// Options.SystemPrompt, BuiltinTools / DisallowedTools
// Send(ctx, msg, anvil.WithForce()); agent.Reload(ctx); anvil.ListRuns(...)
// anvil.Prewarm(ctx, cwd, sources)
```

Still deferred: Bridge, cloud agents, full hooks catalog, sub-agents, IDE plugin.