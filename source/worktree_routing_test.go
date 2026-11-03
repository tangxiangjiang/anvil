// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anvil-dev/anvil/internal/checkpoint"
	"github.com/anvil-dev/anvil/internal/store"
	"github.com/anvil-dev/anvil/internal/tools"
)

func TestWorktreeWorkerRouting(t *testing.T) {
	base, managed, workers := newRoutedWorktreeFixture(t, 2)
	routes := make([]WorktreeWorkerRoute, 0, len(workers))
	for _, worker := range workers {
		route, err := StartWorktreeWorkerRoute(
			context.Background(), routeOptions(base, managed, worker),
		)
		if err != nil {
			t.Fatal(err)
		}
		routes = append(routes, route)
	}
	for i, route := range routes {
		name := filepath.Join(route.worktreeRoot, "worker.txt")
		if err := os.WriteFile(name, []byte(route.WorkerID), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(base, "worker.txt")); !os.IsNotExist(err) {
			t.Fatalf("worker %d wrote into primary checkout: %v", i, err)
		}
	}
	for i, route := range routes {
		body, err := os.ReadFile(filepath.Join(route.worktreeRoot, "worker.txt"))
		if err != nil || string(body) != route.WorkerID {
			t.Fatalf("worker %d content=%q err=%v", i, body, err)
		}
	}

	worker := workers[0]
	ownerPath := worktreeOwnerPath(managed, worker.ID)
	if err := os.Remove(ownerPath); err != nil {
		t.Fatal(err)
	}
	_, err := RestoreWorktreeWorkerRoute(
		context.Background(), routeOptions(base, managed, routes[0].workerManifest()),
	)
	assertWorktreeErrorCode(t, err, "ownership")

	base2, managed2, prepared := newRoutedWorktreeFixture(t, 1)
	artifact, err := GetStoredPlan(
		context.Background(), "agent-one", "artifact-one",
		ListOptions{Local: LocalOptions{CWD: base2}},
	)
	if err != nil {
		t.Fatal(err)
	}
	artifact.Revision++
	artifact.Items[0].Files = []string{"../primary.txt"}
	if err := SavePlanArtifact(
		context.Background(), "agent-one", artifact,
		ListOptions{Local: LocalOptions{CWD: base2}},
	); err != nil {
		t.Fatal(err)
	}
	escaped := routeOptions(base2, managed2, prepared[0])
	escaped.ExpectedPlanRevision = artifact.Revision
	_, err = StartWorktreeWorkerRoute(context.Background(), escaped)
	assertWorktreeErrorCode(t, err, "plan")
}

func TestWorktreeWorkerAttribution(t *testing.T) {
	base, managed, workers := newRoutedWorktreeFixture(t, 1)
	route, err := StartWorktreeWorkerRoute(
		context.Background(), routeOptions(base, managed, workers[0]),
	)
	if err != nil {
		t.Fatal(err)
	}
	event := Event{Type: EventProgress}
	if err := stampWorktreeEvent(&route, &event); err != nil {
		t.Fatal(err)
	}
	if event.WorkerID != route.WorkerID ||
		event.ArtifactID != route.ArtifactID ||
		event.WorkItemID != route.WorkItemID ||
		event.ExecutionID != route.ExecutionID ||
		event.WorkspaceFingerprint != route.WorkspaceFingerprint {
		t.Fatalf("event attribution=%+v route=%+v", event, route)
	}
	wire, err := json.Marshal(route)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(wire), base) ||
		strings.Contains(string(wire), route.worktreeRoot) {
		t.Fatalf("route wire exposed an absolute path: %s", wire)
	}
	spoofed := Event{ArtifactID: "artifact-spoofed"}
	if err := stampWorktreeEvent(&route, &spoofed); err == nil {
		t.Fatal("spoofed event attribution was accepted")
	}

	parent := Options{
		Provider: "openai", APIKey: "test", BaseURL: "http://example.invalid",
		Model: Model{ID: "test"}, HostID: "host-one",
		Local: LocalOptions{CWD: base}, Checkpoints: &CheckpointOptions{},
		completer: usageCompleter{},
	}
	workerOpts, err := WorktreeWorkerOptions(parent, route)
	if err != nil {
		t.Fatal(err)
	}
	if workerOpts.Local.CWD != route.worktreeRoot ||
		workerOpts.storeCWD != base ||
		workerOpts.Local.AllowShellEnabled() {
		t.Fatalf("worker options were not confined: %+v", workerOpts.Local)
	}
	if len(workerOpts.confine) == 0 ||
		!strings.Contains(workerOpts.brief, "README.md") {
		t.Fatalf("trusted plan did not produce worker confinement: %q", workerOpts.brief)
	}
	agent, err := Create(context.Background(), workerOpts)
	if err != nil {
		t.Fatal(err)
	}
	defer agent.Close()
	protected := agent.registry.Execute(
		context.Background(), "write_file",
		json.RawMessage(`{"path":".git/config","content":"forged"}`),
	)
	if !protected.IsError {
		t.Fatal("worktree worker was allowed to write linked Git metadata")
	}
	run, err := agent.Send(context.Background(), "report")
	if err != nil {
		t.Fatal(err)
	}
	seen := false
	for emitted := range run.Stream(context.Background()) {
		if emitted.WorkerID != route.WorkerID ||
			emitted.WorkspaceFingerprint != route.WorkspaceFingerprint {
			t.Fatalf("unstamped routed event: %+v", emitted)
		}
		seen = true
	}
	if _, err := run.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !seen {
		t.Fatal("routed agent emitted no events")
	}
	stored, err := latestStoredWorktree(
		LocalOptions{CWD: base}, base, route.AgentID, route.WorkerID,
	)
	if err != nil || stored.State != WorktreeReviewReady {
		t.Fatalf("terminal worker=%+v err=%v", stored, err)
	}
	if _, err := os.Stat(filepath.Join(base, ".anvil", "agents", agent.AgentID()+".jsonl")); err != nil {
		t.Fatalf("worker transcript was not stored with coordinator: %v", err)
	}
	info, err := Get(
		context.Background(), agent.AgentID(),
		ListOptions{Local: LocalOptions{CWD: base}},
	)
	if err != nil || info.WorkerID != route.WorkerID ||
		info.WorkspaceFingerprint != route.WorkspaceFingerprint {
		t.Fatalf("session projection=%+v err=%v", info, err)
	}
}

func TestWorktreeWorkerCheckpoint(t *testing.T) {
	base, managed, workers := newRoutedWorktreeFixture(t, 2)
	routes := make([]WorktreeWorkerRoute, 2)
	for i := range workers {
		route, err := StartWorktreeWorkerRoute(
			context.Background(), routeOptions(base, managed, workers[i]),
		)
		if err != nil {
			t.Fatal(err)
		}
		routes[i] = route
	}
	route := routes[0]
	cpOpts := CheckpointOptions{Dir: filepath.Join(
		base, ".anvil", "checkpoints", "worktrees", route.CheckpointNamespace,
	)}
	path := filepath.Join(route.worktreeRoot, "README.md")
	scope := checkpointScope(route)
	if err := checkpoint.CaptureScoped(
		cpOpts.Dir, "agent-capture", "run-capture", scope,
		"README.md", path,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("worker change\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreCheckpoint(
		route.worktreeRoot, "agent-capture", "run-capture", cpOpts,
	); err == nil {
		t.Fatal("unscoped restore accepted a worktree checkpoint")
	}

	siblingPath := filepath.Join(routes[1].worktreeRoot, "README.md")
	before, _ := os.ReadFile(siblingPath)
	_, err := checkpoint.RestoreScoped(
		cpOpts.Dir, "agent-capture", "run-capture", checkpointScope(routes[1]),
		func(rel string) (string, error) {
			return tools.ResolveInRoot(routes[1].worktreeRoot, filepath.FromSlash(rel))
		},
	)
	if err == nil {
		t.Fatal("sibling route restored another worker checkpoint")
	}
	after, _ := os.ReadFile(siblingPath)
	if string(after) != string(before) {
		t.Fatal("failed sibling restore changed sibling content")
	}

	restored, err := RestoreWorktreeCheckpoint(
		context.Background(), route, "agent-capture", "run-capture", cpOpts,
	)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(path)
	if len(restored) != 1 || string(body) != "base\n" {
		t.Fatalf("restored=%+v body=%q", restored, body)
	}
}

func TestWorktreeWorkerRestart(t *testing.T) {
	base, managed, workers := newRoutedWorktreeFixture(t, 1)
	route, err := StartWorktreeWorkerRoute(
		context.Background(), routeOptions(base, managed, workers[0]),
	)
	if err != nil {
		t.Fatal(err)
	}
	wrongHost := routeOptions(base, managed, route.workerManifest())
	wrongHost.OwnerHostID = "host-restarted"
	if _, err := RestoreWorktreeWorkerRoute(context.Background(), wrongHost); err == nil {
		t.Fatal("restart silently claimed another host's running worker")
	}
	changed, err := ReconcileWorktreeWorkerRoutes(
		context.Background(), WorktreeWorkerReconcileOptions{
			Local: LocalOptions{CWD: base}, WorktreeRoot: managed,
			AgentID: "agent-one", CurrentHostID: "host-restarted",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) != 1 || changed[0].State != WorktreeInterrupted {
		t.Fatalf("reconciled=%+v", changed)
	}
	retry := routeOptions(base, managed, changed[0])
	retry.OwnerHostID = "host-restarted"
	if _, err := RestoreWorktreeWorkerRoute(context.Background(), retry); err == nil {
		t.Fatal("interrupted worker was silently rerouted")
	}
	if body, _ := os.ReadFile(filepath.Join(base, "README.md")); string(body) != "base\n" {
		t.Fatalf("restart reconciliation changed primary checkout: %q", body)
	}

	base2, managed2, prepared := newRoutedWorktreeFixture(t, 1)
	original := prepared[0]
	key, err := readWorktreeOwnerKey(managed2)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := routeFromWorktree(original, 1, managed2)
	if err != nil {
		t.Fatal(err)
	}
	crashed := original
	crashed.State = WorktreeRunning
	crashed.CheckpointID = preview.CheckpointNamespace
	crashed.Revision++
	crashed.LeaseUntil = time.Now().Add(time.Minute)
	crashed.UpdatedAt = time.Now().UTC()
	if err := writeWorktreeOwner(managed2, key, crashed); err != nil {
		t.Fatal(err)
	}
	recovered, err := StartWorktreeWorkerRoute(
		context.Background(), routeOptions(base2, managed2, original),
	)
	if err != nil || recovered.ManifestRevision != crashed.Revision {
		t.Fatalf("recovered=%+v err=%v", recovered, err)
	}
	cancelled, err := InterruptWorktreeWorker(
		context.Background(),
		routeOptions(base2, managed2, recovered.workerManifest()),
		"user cancelled worker",
	)
	if err != nil || cancelled.State != WorktreeInterrupted ||
		cancelled.Failure != "user cancelled worker" {
		t.Fatalf("cancelled=%+v err=%v", cancelled, err)
	}
}

func newRoutedWorktreeFixture(
	t *testing.T,
	count int,
) (string, string, []WorktreeWorker) {
	t.Helper()
	base, managed := newWorktreeTestRepository(t)
	workers := make([]WorktreeWorker, 0, count)
	items := make([]WorkItem, 0, count)
	for i := 0; i < count; i++ {
		itemID := "item-" + string(rune('a'+i))
		options := worktreeTestOptions(base, managed)
		options.WorkItemID = itemID
		options.Attempt = i + 1
		worker, err := PrepareWorktreeWorker(context.Background(), options)
		if err != nil {
			t.Fatal(err)
		}
		workers = append(workers, worker)
		items = append(items, WorkItem{
			ID: itemID, Title: "Work " + itemID, Goal: "test routing",
			Plan: []string{"edit assigned tree"}, Files: []string{"README.md"},
			Acceptance: []string{"tree is isolated"}, DependsOn: []string{},
			Approval: WorkApprovalApproved, State: WorkStateReady,
			Source: WorkSourcePlan, ExecutionIDs: []string{},
		})
	}
	backend, err := store.Open(base, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.Append("agent-one", store.Record{
		Type: store.TypeMeta, AgentID: "agent-one", CWD: base,
	}); err != nil {
		backend.Close()
		t.Fatal(err)
	}
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	artifact := PlanArtifact{
		Schema: PlanSchemaVersion, ArtifactID: "artifact-one", RunID: "run-plan",
		Kind: PlanKindPlan, Revision: 1, Items: items,
	}
	if err := SavePlanArtifact(
		context.Background(), "agent-one", artifact,
		ListOptions{Local: LocalOptions{CWD: base}},
	); err != nil {
		t.Fatal(err)
	}
	for _, worker := range workers {
		result, err := ClaimStoredPlanExecution(
			context.Background(), "agent-one", PlanExecutionRequest{
				ArtifactID: "artifact-one", ItemID: worker.WorkItemID,
				ExpectedRevision: 1, ExecutionID: worker.ExecutionID,
			}, ListOptions{Local: LocalOptions{CWD: base}},
		)
		if err != nil || !result.Started {
			t.Fatalf("claim %s: started=%v err=%v", worker.WorkItemID, result.Started, err)
		}
	}
	return base, managed, workers
}

func routeOptions(
	base, managed string,
	worker WorktreeWorker,
) WorktreeWorkerRouteOptions {
	return WorktreeWorkerRouteOptions{
		Local: LocalOptions{CWD: base}, WorktreeRoot: managed,
		AgentID: worker.AgentID, WorkerID: worker.ID,
		ExpectedRevision: worker.Revision, ExpectedPlanRevision: 1,
		OwnerHostID: worker.OwnerHostID, Lease: time.Minute,
	}
}

func checkpointScope(route WorktreeWorkerRoute) checkpoint.Scope {
	return checkpoint.Scope{
		RootAgentID: route.AgentID, WorkerID: route.WorkerID,
		ExecutionID: route.ExecutionID, BaseCommit: route.BaseCommit,
		WorkspaceFingerprint: route.WorkspaceFingerprint,
		RootDigest:           route.RootDigest,
	}
}

func (route WorktreeWorkerRoute) workerManifest() WorktreeWorker {
	return WorktreeWorker{
		ID: route.WorkerID, AgentID: route.AgentID,
		ArtifactID: route.ArtifactID, WorkItemID: route.WorkItemID,
		ExecutionID: route.ExecutionID, Revision: route.ManifestRevision,
		OwnerHostID: route.ownerHostID,
	}
}

func assertAttributionError(t *testing.T, err error) {
	t.Helper()
	var lifecycle *WorktreeLifecycleError
	if !errors.As(err, &lifecycle) || lifecycle.Code != "attribution" {
		t.Fatalf("err=%v", err)
	}
}

func cleanOneLine(value string) string {
	return strings.TrimSpace(strings.ReplaceAll(value, "\n", " "))
}
