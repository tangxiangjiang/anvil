# Anvil

Independent **local-only** Go Agent SDK. Capability-aligned with Cursor’s local agent API shape (`Create` / `Prompt` / `Send` / `Stream` / `Wait`), but runs your own tool loop against **OpenAI-compatible** or **Anthropic** HTTP APIs — not the Cursor runtime.

## Layout

| Path | Purpose |
|------|---------|
| [`doc/design/`](doc/design/) | Full design |
| [`doc/roadmaps/`](doc/roadmaps/) | Versioned milestones / phases |
| [`source/`](source/) | Go module (`github.com/anvil-dev/anvil`) |

## Quick start

```bash
cd source
go test ./...

export ANVIL_API_KEY=...
export ANVIL_BASE_URL=https://your-gateway.example.com/v1   # OpenAI-compatible
export ANVIL_MODEL=private/deepseek-v4-pro
export ANVIL_PROVIDER=openai   # or anthropic

go run ./examples/oneshot /path/to/repo "Fix the failing test"
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

## MVP status (0.1)

See [`doc/roadmaps/m_0.1_mvp.md`](doc/roadmaps/m_0.1_mvp.md).
