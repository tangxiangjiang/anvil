// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/anvil-dev/anvil"
)

// TestApplyWorkerLLMLayersOverDaemon pins the precedence the feature depends on:
// a worker's own settings win over the fleet default, and an empty field means
// "inherit", not "clear".
func TestApplyWorkerLLMLayersOverDaemon(t *testing.T) {
	opts := anvil.Options{
		Provider: anvil.ProviderOpenAI,
		BaseURL:  "http://fleet/v1",
		APIKey:   "fleet-key",
		Model:    anvil.Model{ID: "fleet-model"},
		MaxSteps: 64,
	}
	applyWorkerLLM(&opts, &WorkerLLMBody{Model: "cheap-model"})

	if opts.Model.ID != "cheap-model" {
		t.Fatalf("model = %q, want the worker's", opts.Model.ID)
	}
	// Everything the worker did not name is still the fleet's.
	if opts.BaseURL != "http://fleet/v1" {
		t.Fatalf("base url = %q, want inherited", opts.BaseURL)
	}
	if opts.APIKey != "fleet-key" {
		t.Fatalf("api key = %q, want inherited", opts.APIKey)
	}
	if opts.MaxSteps != 64 {
		t.Fatalf("max steps = %d, want inherited", opts.MaxSteps)
	}
}

// TestApplyWorkerLLMNilIsInherit: a worker with no override must behave exactly as
// every worker did before the field existed.
func TestApplyWorkerLLMNilIsInherit(t *testing.T) {
	opts := anvil.Options{BaseURL: "http://fleet/v1", Model: anvil.Model{ID: "m"}}
	applyWorkerLLM(&opts, nil)
	if opts.BaseURL != "http://fleet/v1" || opts.Model.ID != "m" {
		t.Fatalf("nil override changed the options: %+v", opts)
	}
}

// TestApplyWorkerLLMOverridesEveryField covers the full override case.
func TestApplyWorkerLLMOverridesEveryField(t *testing.T) {
	t.Setenv("ANVIL_WORKER_TEST_KEY", "worker-key")
	opts := anvil.Options{
		Provider: anvil.ProviderOpenAI,
		BaseURL:  "http://fleet/v1",
		APIKey:   "fleet-key",
		Model:    anvil.Model{ID: "fleet-model"},
		MaxSteps: 64,
	}
	applyWorkerLLM(&opts, &WorkerLLMBody{
		Provider:  "anthropic",
		BaseURL:   "http://other/v1/",
		Model:     "worker-model",
		APIKeyEnv: "ANVIL_WORKER_TEST_KEY",
		MaxSteps:  8,
	})
	if opts.Provider != anvil.ProviderAnthropic {
		t.Fatalf("provider = %q", opts.Provider)
	}
	// Trailing slash is trimmed so the provider joins paths correctly.
	if opts.BaseURL != "http://other/v1" {
		t.Fatalf("base url = %q", opts.BaseURL)
	}
	if opts.Model.ID != "worker-model" || opts.MaxSteps != 8 {
		t.Fatalf("model/steps = %q/%d", opts.Model.ID, opts.MaxSteps)
	}
	if opts.APIKey != "worker-key" {
		t.Fatalf("api key = %q, want the one from the named env", opts.APIKey)
	}
}

// TestApplyWorkerLLMMissingKeyEnvKeepsInherited: a worker that names an
// unset variable must not blank the key. Silently losing the credential would turn
// a misconfiguration into an authentication error against the provider.
func TestApplyWorkerLLMMissingKeyEnvKeepsInherited(t *testing.T) {
	opts := anvil.Options{APIKey: "fleet-key"}
	applyWorkerLLM(&opts, &WorkerLLMBody{APIKeyEnv: "ANVIL_THIS_IS_NOT_SET_ANYWHERE"})
	if opts.APIKey != "fleet-key" {
		t.Fatalf("api key = %q, want the inherited one", opts.APIKey)
	}
}

// TestSlotsSetWorkerLLM: re-pointing a worker takes effect without a restart, which
// is the point of a per-worker model.
func TestSlotsSetWorkerLLM(t *testing.T) {
	s := NewSlots(1)
	if _, err := s.AddBoundWorkerWithLLM("w1", "w1", "", "/tmp/w1", "", "w1", true,
		&WorkerLLMBody{Model: "first"}); err != nil {
		t.Fatal(err)
	}
	got := s.LLMFor("w1")
	if got == nil || got.Model != "first" {
		t.Fatalf("initial override = %+v", got)
	}
	if _, err := s.SetWorkerLLM("w1", &WorkerLLMBody{Model: "second"}); err != nil {
		t.Fatal(err)
	}
	if got := s.LLMFor("w1"); got == nil || got.Model != "second" {
		t.Fatalf("override after set = %+v", got)
	}
	// Clearing returns the worker to inheriting.
	if _, err := s.SetWorkerLLM("w1", nil); err != nil {
		t.Fatal(err)
	}
	if got := s.LLMFor("w1"); got != nil {
		t.Fatalf("override after clear = %+v", got)
	}
	if _, err := s.SetWorkerLLM("nope", nil); err == nil {
		t.Fatal("expected an error for an unknown worker")
	}
}

// TestWorkerLLMSurvivesPersistedState is the reason the field is on
// PersistedWorker: a sandbox worker list is restored on daemon start, and a
// provider that vanishes across a restart would repoint a worker at the fleet
// default without telling anyone.
func TestWorkerLLMSurvivesPersistedState(t *testing.T) {
	rec := PersistedWorker{
		ID:         "Demo",
		ProjectRel: "proj",
		Enabled:    true,
		LLM:        &WorkerLLMBody{Model: "cheap", MaxSteps: 12},
	}
	if rec.LLM == nil || rec.LLM.Model != "cheap" || rec.LLM.MaxSteps != 12 {
		t.Fatalf("persisted llm = %+v", rec.LLM)
	}
}

func TestSaveWorkersStateIncludesLLM(t *testing.T) {
	sandbox := t.TempDir()
	s := &Server{
		cfg:   Config{SandboxDir: sandbox},
		slots: NewSlots(0),
	}
	project := filepath.Join(sandbox, "proj")
	if _, err := s.slots.AddBoundWorkerWithLLM(
		"demo", "Demo", "", project, "", "demo", true,
		&WorkerLLMBody{Model: "cheap", APIKeyEnv: "WORKER_KEY"},
	); err != nil {
		t.Fatal(err)
	}
	s.saveWorkersState()
	raw, err := os.ReadFile(filepath.Join(sandbox, ".anvil", "workers.json"))
	if err != nil {
		t.Fatal(err)
	}
	var file WorkersStateFile
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	if len(file.Workers) != 1 || file.Workers[0].LLM == nil ||
		file.Workers[0].LLM.Model != "cheap" || file.Workers[0].LLM.APIKeyEnv != "WORKER_KEY" {
		t.Fatalf("persisted workers = %+v", file.Workers)
	}
	if string(raw) == "WORKER_KEY_VALUE" {
		t.Fatal("a key value was persisted")
	}
}
