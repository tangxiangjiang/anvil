package anvil

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anvil-dev/anvil/internal/llm"
	"github.com/anvil-dev/anvil/internal/prompt"
	"github.com/anvil-dev/anvil/internal/tools"
)

func testSubagentProfile() SubagentProfile {
	return SubagentProfile{
		ID:           "explorer",
		Description:  "Investigates unfamiliar code",
		Instructions: "Trace behavior and report evidence.",
		Model:        "special-model",
		Tools:        []string{"read", "search"},
		MaxSteps:     12,
	}
}

func TestSubagentProfilesRoundTripAndPreserveOrder(t *testing.T) {
	dir := t.TempDir()
	first := testSubagentProfile()
	second := first
	second.ID = "reviewer"
	second.Description = "Reviews changes"

	state, err := SaveSubagentProfiles(dir, []SubagentProfile{first, second})
	if err != nil {
		t.Fatal(err)
	}
	if !state.Exists || state.Problem != "" || len(state.Profiles) != 2 {
		t.Fatalf("state=%+v", state)
	}
	first.Description = "Explores a codebase"
	state, err = UpsertSubagentProfile(dir, first)
	if err != nil {
		t.Fatal(err)
	}
	if state.Profiles[0].Description != first.Description || state.Profiles[1].ID != second.ID {
		t.Fatalf("upsert changed profile order: %+v", state.Profiles)
	}
	state, err = RemoveSubagentProfile(dir, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Profiles) != 1 || state.Profiles[0].ID != second.ID {
		t.Fatalf("remove=%+v", state.Profiles)
	}
}

func TestMalformedSubagentProfilesAreNotOverwritten(t *testing.T) {
	dir := t.TempDir()
	path := SubagentProfilesPath(dir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	original := []byte(`{"version":1,"profiles":[],"unknown":true}`)
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}
	state := LoadSubagentProfiles(dir)
	if state.Problem == "" {
		t.Fatal("malformed file has no problem")
	}
	if _, err := UpsertSubagentProfile(dir, testSubagentProfile()); err == nil {
		t.Fatal("upsert unexpectedly rewrote malformed file")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(original) {
		t.Fatalf("file changed: %s", after)
	}
}

func TestSubagentProfileValidation(t *testing.T) {
	tests := []SubagentProfile{
		{ID: "UPPER", Description: "ok", Instructions: "ok"},
		{ID: "valid", Description: "", Instructions: "ok"},
		{ID: "valid", Description: "ok", Instructions: ""},
		{ID: "valid", Description: "ok", Instructions: "ok", MaxSteps: -1},
	}
	for _, profile := range tests {
		if err := ValidateSubagentProfiles([]SubagentProfile{profile}); err == nil {
			t.Fatalf("profile unexpectedly valid: %+v", profile)
		}
	}
	profile := testSubagentProfile()
	if err := ValidateSubagentProfiles([]SubagentProfile{profile, profile}); err == nil {
		t.Fatal("duplicate id unexpectedly valid")
	}
}

func TestSpawnProfileResolutionHonoursExplicitEmptyValues(t *testing.T) {
	var in spawnParams
	if err := json.Unmarshal([]byte(`{
		"prompt":"inspect","description":"inspect","profile":"explorer",
		"model":"","tools":[],"max_steps":0
	}`), &in); err != nil {
		t.Fatal(err)
	}
	got, instructions, err := resolveSubagentProfile([]SubagentProfile{testSubagentProfile()}, in)
	if err != nil {
		t.Fatal(err)
	}
	if got.Model != "" || len(got.Tools) != 0 || got.MaxSteps != 0 {
		t.Fatalf("explicit inherited defaults lost: %+v", got)
	}
	if instructions != testSubagentProfile().Instructions {
		t.Fatalf("instructions=%q", instructions)
	}
}

func TestSpawnProfileDefaultsAndPrompt(t *testing.T) {
	in := spawnParams{Profile: "explorer"}
	got, instructions, err := resolveSubagentProfile([]SubagentProfile{testSubagentProfile()}, in)
	if err != nil {
		t.Fatal(err)
	}
	if got.Model != "special-model" || got.MaxSteps != 12 || len(got.Tools) != 2 {
		t.Fatalf("resolved=%+v", got)
	}
	// The profile's instructions travel in the brief layer, so the role text and the
	// safety layer stay in the child's prompt around them.
	brief := subagentProfileBrief([]string{instructions})
	if !strings.HasPrefix(brief, "Profile instructions:\n") || !strings.Contains(brief, instructions) {
		t.Fatalf("brief=%q", brief)
	}
	if subagentProfileBrief(nil) != "" || subagentProfileBrief([]string{"  "}) != "" {
		t.Fatal("a child without profile instructions got a brief")
	}
	sys := prompt.Compose(prompt.Spec{CWD: "/ws", Role: prompt.RoleSubagent, Brief: brief})
	if !strings.HasPrefix(sys, prompt.Safety(prompt.Options{})) || !strings.Contains(sys, instructions) || !strings.HasSuffix(sys, prompt.Closing) {
		t.Fatalf("a profiled child's prompt lost a layer:\n%s", sys)
	}
}

func TestUnknownProfileFailsBeforeBudget(t *testing.T) {
	parent := &Agent{
		opts:   Options{Subagents: SubagentOptions{Enabled: true, Profiles: []SubagentProfile{testSubagentProfile()}}},
		budget: newSubagentBudget(2),
	}
	reg := tools.NewRegistry(t.TempDir(), false)
	_, err := spawnAgent(withAgent(context.Background(), parent), reg, json.RawMessage(
		`{"prompt":"inspect","description":"inspect","profile":"missing"}`,
	))
	if err == nil || !strings.Contains(err.Error(), "unknown subagent profile") {
		t.Fatalf("err=%v", err)
	}
	if parent.budget.used != 0 {
		t.Fatalf("unknown profile consumed budget: %d", parent.budget.used)
	}
}

func TestProfileCannotWidenParentToolsOrConsumeBudget(t *testing.T) {
	profile := testSubagentProfile()
	profile.Tools = []string{"write"}
	parent := &Agent{
		opts:   Options{Subagents: SubagentOptions{Enabled: true, Profiles: []SubagentProfile{profile}}},
		budget: newSubagentBudget(2),
	}
	reg := tools.NewRegistry(t.TempDir(), false)
	reg.Unregister("write_file")
	_, err := spawnAgent(withAgent(context.Background(), parent), reg, json.RawMessage(
		`{"prompt":"inspect","description":"inspect","profile":"explorer"}`,
	))
	if err == nil || !strings.Contains(err.Error(), "not available to the parent") {
		t.Fatalf("err=%v", err)
	}
	if parent.budget.used != 0 {
		t.Fatalf("invalid profile tools consumed budget: %d", parent.budget.used)
	}
}

func TestSubagentToolAdvertisesProfiles(t *testing.T) {
	tool := subagentTool(tools.NewRegistry(t.TempDir(), false), []SubagentProfile{testSubagentProfile()})
	var schema struct {
		Properties map[string]struct {
			Enum        []string `json:"enum"`
			Description string   `json:"description"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(tool.Parameters, &schema); err != nil {
		t.Fatal(err)
	}
	profile := schema.Properties["profile"]
	if len(profile.Enum) != 1 || profile.Enum[0] != "explorer" ||
		!strings.Contains(profile.Description, "Investigates unfamiliar code") {
		t.Fatalf("profile schema=%+v", profile)
	}
}

func TestChildModelRouteSelectsItsOwnProviderClient(t *testing.T) {
	parentCompleter := completerFunc(func(context.Context, llm.Request) (*llm.Response, error) {
		return nil, nil
	})
	parent := &Agent{
		opts: Options{
			Provider: ProviderAnthropic,
			APIKey:   "anthropic-key",
			BaseURL:  "https://anthropic.example",
			Model:    Model{ID: "claude-main"},
			ModelRoutes: map[string]ModelRoute{
				"gpt-child": {
					Provider: ProviderOpenAI,
					APIKey:   "openai-key",
					BaseURL:  "https://openai-gateway.example/v1",
					Capabilities: ModelCapabilities{
						Thinking: ThinkingOpenAIEffort,
					},
				},
			},
		},
		complete: parentCompleter,
	}

	child := parent.childOptions(spawnParams{Model: "gpt-child"}, nil, 1)
	if child.Provider != ProviderOpenAI || child.APIKey != "openai-key" ||
		child.BaseURL != "https://openai-gateway.example/v1" {
		t.Fatalf("child route=%+v", child)
	}
	if child.Model.ID != "gpt-child" || child.Model.Capabilities.Thinking != ThinkingOpenAIEffort {
		t.Fatalf("child model=%+v", child.Model)
	}
	if child.completer != nil {
		t.Fatal("child reused the parent protocol client")
	}
	if child.ModelRoutes["gpt-child"].BaseURL == "" {
		t.Fatal("nested children lost the route table")
	}
}

func TestUnroutedChildModelKeepsInheritedConnectionAndCompleter(t *testing.T) {
	parent := &Agent{
		opts: Options{
			Provider: ProviderAnthropic,
			APIKey:   "key",
			BaseURL:  "https://anthropic.example",
			Model:    Model{ID: "claude-main"},
		},
		complete: completerFunc(func(context.Context, llm.Request) (*llm.Response, error) {
			return nil, nil
		}),
	}
	child := parent.childOptions(spawnParams{Model: "claude-child"}, nil, 1)
	if child.Provider != ProviderAnthropic || child.BaseURL != parent.opts.BaseURL {
		t.Fatalf("historical inheritance changed: %+v", child)
	}
	if child.completer == nil {
		t.Fatal("an unrouted model discarded the inherited provider client")
	}
}

func TestApplyChildModelRoutesWorkflowWorkerOptions(t *testing.T) {
	parent := &Agent{
		opts: Options{
			Provider: ProviderAnthropic,
			APIKey:   "anthropic-key",
			BaseURL:  "https://anthropic.example",
			Model:    Model{ID: "claude-main"},
			ModelRoutes: map[string]ModelRoute{
				"gpt-worker": {
					Provider: ProviderOpenAI,
					APIKey:   "openai-key",
					BaseURL:  "https://gateway.example/v1",
				},
			},
		},
		complete: completerFunc(func(context.Context, llm.Request) (*llm.Response, error) {
			return nil, nil
		}),
		cwd: t.TempDir(),
	}
	opts := parent.workerOptions(&Brief{Goal: "implement"}, nil, 1)
	parent.applyChildModel(&opts, "gpt-worker")
	if opts.Provider != ProviderOpenAI || opts.BaseURL != "https://gateway.example/v1" ||
		opts.APIKey != "openai-key" || opts.Model.ID != "gpt-worker" {
		t.Fatalf("worker route=%+v", opts)
	}
	if opts.completer != nil {
		t.Fatal("routed workflow worker reused the parent protocol client")
	}
}

func TestChildModelRoutesEmitProviderNativeRequests(t *testing.T) {
	tests := []struct {
		name       string
		parent     Provider
		route      Provider
		wantPath   string
		wantHeader string
		response   map[string]any
	}{
		{
			name:       "anthropic root to openai child",
			parent:     ProviderAnthropic,
			route:      ProviderOpenAI,
			wantPath:   "/chat/completions",
			wantHeader: "Authorization",
			response: map[string]any{
				"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "ok"}}},
			},
		},
		{
			name:       "openai root to anthropic child",
			parent:     ProviderOpenAI,
			route:      ProviderAnthropic,
			wantPath:   "/v1/messages",
			wantHeader: "X-Api-Key",
			response: map[string]any{
				"content": []any{map[string]any{"type": "text", "text": "ok"}},
				"usage":   map[string]any{"input_tokens": 1, "output_tokens": 1},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotModel string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != tt.wantPath {
					t.Errorf("path=%q, want %q", r.URL.Path, tt.wantPath)
				}
				if r.Header.Get(tt.wantHeader) == "" {
					t.Errorf("missing %s header", tt.wantHeader)
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Errorf("decode request: %v", err)
				}
				gotModel, _ = body["model"].(string)
				_ = json.NewEncoder(w).Encode(tt.response)
			}))
			defer srv.Close()

			dir := t.TempDir()
			parent := &Agent{
				opts: Options{
					Provider: tt.parent,
					APIKey:   "parent-key",
					BaseURL:  "https://parent.invalid",
					Model:    Model{ID: "parent-model"},
					ModelRoutes: map[string]ModelRoute{
						"child-model": {
							Provider: tt.route,
							APIKey:   "route-key",
							BaseURL:  srv.URL,
						},
					},
				},
				cwd:      dir,
				complete: completerFunc(func(context.Context, llm.Request) (*llm.Response, error) { return nil, nil }),
			}
			child, err := Create(context.Background(), parent.childOptions(spawnParams{Model: "child-model"}, nil, 1))
			if err != nil {
				t.Fatal(err)
			}
			defer child.Close()
			if _, err := child.complete.Complete(context.Background(), llm.Request{
				Messages: []llm.Message{{Role: llm.RoleUser, Content: "hello"}},
			}); err != nil {
				t.Fatal(err)
			}
			if gotModel != "child-model" {
				t.Fatalf("model=%q, want child-model", gotModel)
			}
		})
	}
}
