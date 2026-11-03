// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package store_test

import (
	"errors"
	"testing"

	"github.com/anvil-dev/anvil/internal/store"
)

func TestUpdateRecordsCommitsAuditBatchAtomically(t *testing.T) {
	for _, kind := range []string{store.KindJSONL, store.KindSQLite} {
		t.Run(kind, func(t *testing.T) {
			backend, err := store.Open(t.TempDir(), kind)
			if err != nil {
				t.Fatal(err)
			}
			defer backend.Close()
			const agentID = "agent-update"
			if err := backend.Append(agentID, store.Record{Type: store.TypeMeta, AgentID: agentID}); err != nil {
				t.Fatal(err)
			}
			if err := backend.UpdateRecords(agentID, func(records []store.Record) ([]store.Record, error) {
				return append(records,
					store.Record{Type: store.TypePlanArtifact, ArtifactID: "artifact", Revision: 2},
					store.Record{Type: store.TypeWorkState, ArtifactID: "artifact", ItemID: "item", Revision: 2},
				), nil
			}); err != nil {
				t.Fatal(err)
			}
			records, err := backend.Load(agentID)
			if err != nil {
				t.Fatal(err)
			}
			if len(records) != 3 || records[1].Time.IsZero() || records[2].Time.IsZero() {
				t.Fatalf("records=%+v", records)
			}

			refused := errors.New("stale revision")
			if err := backend.UpdateRecords(agentID, func(records []store.Record) ([]store.Record, error) {
				records = append(records, store.Record{Type: store.TypeWorkState, ItemID: "must-not-land"})
				return records, refused
			}); !errors.Is(err, refused) {
				t.Fatalf("rollback error=%v", err)
			}
			records, err = backend.Load(agentID)
			if err != nil {
				t.Fatal(err)
			}
			if len(records) != 3 {
				t.Fatalf("failed update committed %d records", len(records))
			}

			if err := backend.UpdateRecords(agentID, func(records []store.Record) ([]store.Record, error) {
				records[0].AgentID = "rewritten"
				return records, nil
			}); err == nil {
				t.Fatal("append-only prefix rewrite was accepted")
			}
		})
	}
}
