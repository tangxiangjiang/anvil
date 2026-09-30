package anvil

// Provider selects the LLM wire protocol.
type Provider string

const (
	ProviderOpenAI    Provider = "openai"    // Chat Completions; OpenAI-compatible gateways
	ProviderAnthropic Provider = "anthropic" // Claude Messages API
)

// Options configures Create / Prompt / Resume.
type Options struct {
	Provider Provider
	APIKey   string
	BaseURL  string
	Model    Model
	Local    LocalOptions
	MaxSteps int // 0 = default (32)
}

// Model identifies the model id for the selected provider.
type Model struct {
	ID string
}

// LocalOptions applies to the local runtime only.
type LocalOptions struct {
	CWD        string
	AllowShell *bool // nil = true (default)
}

// AllowShellEnabled returns whether shell tool is enabled.
func (o LocalOptions) AllowShellEnabled() bool {
	if o.AllowShell == nil {
		return true
	}
	return *o.AllowShell
}

// SendOption mutates per-send behavior (reserved for 0.1+ extensions).
type SendOption func(*sendConfig)

type sendConfig struct{}
