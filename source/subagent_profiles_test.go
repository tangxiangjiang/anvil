package anvil

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
