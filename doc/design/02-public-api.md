# 02 — Public API

## Entry points

```go
package anvil

func Create(ctx context.Context, opts Options) (*Agent, error)
func Prompt(ctx context.Context, message string, opts Options) (*RunResult, error)
func Resume(ctx context.Context, agentID string, opts Options) (*Agent, error)
```

- **Create** — validate options, allocate agent ID, return handle (no LLM call yet).
- **Prompt** — Create + Send + Wait + Close (one-shot; disposes for the caller).
- **Resume** — load persisted agent by ID from local store (milestone 0.2+).

## Agent

```go
type Agent struct { /* unexported fields */ }

func (a *Agent) AgentID() string
func (a *Agent) Send(ctx context.Context, message string, opts ...SendOption) (*Run, error)
func (a *Agent) Close() error
```

## Run

```go
type Run struct { /* unexported fields */ }

func (r *Run) ID() string
func (r *Run) Stream(ctx context.Context) <-chan Event
func (r *Run) Wait(ctx context.Context) (*RunResult, error)
func (r *Run) Cancel(ctx context.Context) error  // 0.2+
```

- Callers may consume `Stream`, call `Wait` only, or both.
- `Wait` is required for terminal status; skipping it may leak watchers (document as API contract).
- `Stream` channel is closed after the run finishes (success, error, or cancel).

## Options

```go
type Provider string

const (
    ProviderOpenAI    Provider = "openai"    // Chat Completions; also private OpenAI-compatible gateways
    ProviderAnthropic Provider = "anthropic" // Claude Messages API
)

type Options struct {
    Provider Provider      // default ProviderOpenAI
    APIKey   string        // or env ANVIL_API_KEY
    BaseURL  string        // override provider default root
    Model    Model
    Local    LocalOptions
    // MaxSteps, MaxWallTime — available 0.2+; may appear as SendOption earlier
}

type Model struct {
    ID string // e.g. "private/deepseek-v4-pro" or "claude-sonnet-4-5"
}

type LocalOptions struct {
    CWD        string // required for local; default "."
    AllowShell bool   // default true
    // StorePath override optional later
}
```

- OpenAI-compatible private gateways: `ProviderOpenAI` + custom `BaseURL` (e.g. `…/v1`) + model id.
- Anthropic: `ProviderAnthropic` + model id; default BaseURL `https://api.anthropic.com`.
- `cloud` options are **out of scope** unless a new design chapter adds them.

## Results and events

```go
type RunResult struct {
    ID     string
    Status RunStatus // finished | error | cancelled
    Result string    // final assistant text when finished
    Steps  int
    Usage  *Usage    // optional; tokens if provider returns them
    Err    string    // when Status == error
}

type Event struct {
    Type EventType // assistant | tool_call | tool_result | done | error
    // Type-specific payloads (text, tool name/args/result, etc.)
}
```

Exact event payload fields are specified in [07-streaming-and-errors.md](07-streaming-and-errors.md).

## Errors

- Startup / config / auth / network before a run executes → `*anvil.Error` with `Retryable bool`.
- Run that started and failed → `RunResult.Status == error` (and optionally a non-nil error from `Wait` wrapping the result).

Do not conflate the two in examples or CI exit codes (suggest exit 1 vs 2).

## Milestone mapping

| API | First milestone |
|-----|-----------------|
| Create, Close, Options | 0.1 |
| Send, Stream, Wait, Prompt | 0.1 |
| Resume, store-backed history | 0.2 |
| Cancel | 0.2 |
| Custom tools / MCP on Options | 0.3 |
| List / Get / Usage | 0.5 |
| Progress events / default MaxSteps 128 | 0.6 |
| MCP HTTP/SSE; Store backend; CLI | 0.6 |
