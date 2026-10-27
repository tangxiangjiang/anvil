package editorhost

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anvil-dev/anvil"
)

func profileWire() SubagentProfileEntry {
	return SubagentProfileEntry{
		ID: "explorer", Description: "Investigates unfamiliar code",
		Instructions: "Trace behavior and report evidence.",
		Model:        "special-model", Tools: []string{"read", "search"}, MaxSteps: 12,
	}
}

func TestSubagentProfileRPCAndSessionLoading(t *testing.T) {
	dir := newWorkspace(t)
	srv := New(Config{CWD: dir, Model: "main-model"}, strings.NewReader(""), &bytes.Buffer{})
	raw, _ := json.Marshal(SubagentsUpsertParams{Profile: profileWire()})
	value, rpcErr := srv.handleSubagentsUpsert(raw)
	if rpcErr != nil {
		t.Fatal(rpcErr.Message)
	}
	result := value.(SubagentsResult)
	if !result.Exists || len(result.Profiles) != 1 || len(result.ToolGroups) == 0 || len(result.DefaultTools) == 0 {
		t.Fatalf("result=%+v", result)
	}

	sess := newSession("sess-test", srv.cwdPath(), ModeAsk, false)
	opts, rpcErr := srv.buildOptions(context.Background(), SessionParams{}, sess)
	if rpcErr != nil {
		t.Fatal(rpcErr.Message)
	}
	if len(opts.Subagents.Profiles) != 1 || opts.Subagents.Profiles[0].ID != "explorer" {
		t.Fatalf("profiles=%+v", opts.Subagents.Profiles)
	}

	disabled := false
	opts, rpcErr = srv.buildOptions(context.Background(), SessionParams{Subagents: &disabled}, sess)
	if rpcErr != nil {
		t.Fatal(rpcErr.Message)
	}
	if opts.Subagents.Enabled || len(opts.Subagents.Profiles) != 0 {
		t.Fatalf("disabled subagents=%+v", opts.Subagents)
	}
}

func TestSubagentProfileRPCReportsMalformedFileWithoutRewriting(t *testing.T) {
	dir := newWorkspace(t)
	path := anvil.SubagentProfilesPath(dir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	original := []byte(`{"version":2,"profiles":[]}`)
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}
	srv := New(Config{CWD: dir, Model: "main-model"}, strings.NewReader(""), &bytes.Buffer{})
	value, rpcErr := srv.handleSubagentsGet(json.RawMessage(`{}`))
	if rpcErr != nil {
		t.Fatal(rpcErr.Message)
	}
	if result := value.(SubagentsResult); result.Problem == "" {
		t.Fatalf("result=%+v", result)
	}
	raw, _ := json.Marshal(SubagentsUpsertParams{Profile: profileWire()})
	if _, rpcErr = srv.handleSubagentsUpsert(raw); rpcErr == nil {
		t.Fatal("malformed file was accepted")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(original) {
		t.Fatalf("file changed: %s", after)
	}
}
