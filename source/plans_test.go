// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/anvil-dev/anvil/internal/store"
)

func planFixture(t *testing.T) ([]byte, PlanArtifact) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "doc", "fixtures", "plan-schema-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := DecodePlanArtifact(data)
	if err != nil {
		t.Fatal(err)
	}
	return data, artifact
}

func TestPlanSchema(t *testing.T) {
	data, artifact := planFixture(t)
	if artifact.Schema != PlanSchemaVersion || artifact.ArtifactID != "artifact-release" {
		t.Fatalf("artifact=%+v", artifact)
	}
	if len(artifact.Items) != 2 || artifact.Items[1].DependsOn[0] != "prepare" {
		t.Fatalf("items=%+v", artifact.Items)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if raw["schema"] != float64(PlanSchemaVersion) {
		t.Fatalf("fixture schema=%v", raw["schema"])
	}
}

func TestPlanValidate(t *testing.T) {
	_, valid := planFixture(t)
	tests := []struct {
		name string
		edit func(*PlanArtifact)
		want string
	}{
		{"unknown schema", func(a *PlanArtifact) { a.Schema++ }, "unsupported plan schema"},
		{"duplicate id", func(a *PlanArtifact) {
			a.Items[1].ID = a.Items[0].ID
			a.Items[1].DependsOn = []string{}
		}, "duplicate plan item"},
		{"missing dependency", func(a *PlanArtifact) { a.Items[1].DependsOn = []string{"absent"} }, "missing item"},
		{"cycle", func(a *PlanArtifact) { a.Items[0].DependsOn = []string{"build"} }, "dependency cycle"},
		{"pending running", func(a *PlanArtifact) { a.Items[1].State = WorkStateRunning }, "pending item"},
		{"denied proposed", func(a *PlanArtifact) {
			a.Items[1].Approval = WorkApprovalDenied
		}, "denied item"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			artifact := valid
			artifact.Items = append([]WorkItem(nil), valid.Items...)
			for i := range artifact.Items {
				artifact.Items[i].DependsOn = make([]string, len(valid.Items[i].DependsOn))
				copy(artifact.Items[i].DependsOn, valid.Items[i].DependsOn)
			}
			tc.edit(&artifact)
			if err := artifact.Validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
		})
	}
	unknown := strings.Replace(string(planMustJSON(t, valid)), `"schema":1`, `"schema":1,"future":true`, 1)
	if _, err := DecodePlanArtifact([]byte(unknown)); err == nil ||
		!strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown field error=%v", err)
	}
}

func TestPlanRoundTrip(t *testing.T) {
	_, artifact := planFixture(t)
	encoded, err := EncodePlanArtifact(artifact)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodePlanArtifact(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, artifact) {
		t.Fatalf("decoded=%+v\nartifact=%+v", decoded, artifact)
	}
}

func TestPlanStore(t *testing.T) {
	_, artifact := planFixture(t)
	for _, kind := range []string{store.KindJSONL, store.KindSQLite} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			backend, err := store.Open(dir, kind)
			if err != nil {
				t.Fatal(err)
			}
			if err := backend.Append("agent-plan", store.Record{
				Type: store.TypeMeta, AgentID: "agent-plan",
			}); err != nil {
				t.Fatal(err)
			}
			if err := backend.Close(); err != nil {
				t.Fatal(err)
			}
			opts := ListOptions{Local: LocalOptions{CWD: dir, Store: kind}}
			if err := SavePlanArtifact(context.Background(), "agent-plan", artifact, opts); err != nil {
				t.Fatal(err)
			}
			if err := SavePlanArtifact(context.Background(), "agent-plan", artifact, opts); !errors.Is(err, ErrPlanRevisionConflict) {
				t.Fatalf("duplicate artifact revision error=%v", err)
			}
			state := WorkStateRecord{
				ArtifactID: artifact.ArtifactID, ItemID: "prepare", Revision: 1,
				ExecutionID: "run-prepare", State: WorkStateCompleted, Actor: "host-local",
				Evidence: json.RawMessage(`{"kind":"test","passed":true}`),
			}
			if err := SaveWorkState(context.Background(), "agent-plan", state, opts); err != nil {
				t.Fatal(err)
			}
			artifacts, err := ListPlanArtifacts(context.Background(), "agent-plan", opts)
			if err != nil {
				t.Fatal(err)
			}
			states, err := ListWorkStates(context.Background(), "agent-plan", opts)
			if err != nil {
				t.Fatal(err)
			}
			if len(artifacts) != 1 || !reflect.DeepEqual(artifacts[0], artifact) {
				t.Fatalf("artifacts=%+v", artifacts)
			}
			if len(states) != 1 || states[0].Time.IsZero() {
				t.Fatalf("states=%+v", states)
			}
			states[0].Time = time.Time{}
			if !reflect.DeepEqual(states[0], state) {
				t.Fatalf("states=%+v", states)
			}
		})
	}
}

func planMustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
