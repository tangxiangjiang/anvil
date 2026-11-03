// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package editorhost

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	anvil "github.com/anvil-dev/anvil"
)

func TestPlanRoundTripHostWire(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "doc", "fixtures", "plan-schema-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var artifact WorkflowArtifact
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&artifact); err != nil {
		t.Fatal(err)
	}
	if artifact.Schema != 1 || artifact.ArtifactID != "artifact-release" || len(artifact.Items) != 2 {
		t.Fatalf("artifact=%+v", artifact)
	}
	wire, err := json.Marshal(artifact)
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip WorkflowArtifact
	if err := json.Unmarshal(wire, &roundTrip); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(roundTrip, artifact) {
		t.Fatalf("roundTrip=%+v artifact=%+v", roundTrip, artifact)
	}
}

func TestPlanRoundTripEventIdentity(t *testing.T) {
	event, ok := projectEvent("session-1", "run-1", anvil.Event{
		Type: anvil.EventTodo, ArtifactID: "artifact-release",
		WorkItemID: "build", ExecutionID: "run-build",
		Items: []anvil.TodoItem{{
			ID: "todo-build", Text: "build", Status: anvil.TodoPending, WorkItemID: "build",
		}},
	})
	if !ok || event.ArtifactID != "artifact-release" ||
		event.WorkItemID != "build" || event.ExecutionID != "run-build" {
		t.Fatalf("event=%+v ok=%v", event, ok)
	}
	if event.Items == nil || len(*event.Items) != 1 ||
		(*event.Items)[0].ID != "todo-build" || (*event.Items)[0].WorkItemID != "build" {
		t.Fatalf("items=%+v", event.Items)
	}
}
