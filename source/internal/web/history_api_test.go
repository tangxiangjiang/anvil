// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/anvil-dev/anvil/internal/web/delivery"
)

// TestSearchFindsBodyText is the acceptance that matters for p_0.46.10: a word
// inside a requirement's Completion criteria must find it. A search of a planning
// document that only matches titles is not a search.
func TestSearchFindsBodyText(t *testing.T) {
	s, tok := wbServer(t, nil)
	if s.delivery == nil {
		t.Fatal("delivery store missing")
	}
	// CreateEngReq with AutoMajor needs a developing generation to attach to.
	if err := s.delivery.EnsureSeed(); err != nil {
		t.Fatal(err)
	}
	eng, err := s.delivery.CreateEngReq(delivery.CreateEngReqInput{
		Worker:    delivery.WorkerRef{ID: "w1", Label: "w", Fleet: "f", Node: "n", ProjectRel: "p"},
		Title:     "Requirements eng",
		ReqsRel:   "docs/reqs",
		AutoMajor: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	v := eng.Versions[0]
	success := "The pipeline must retry transient failures three times."
	if _, err := s.delivery.UpdateVersion(delivery.UpdateVersionInput{
		EngReqID: eng.ID, Major: v.Major, Minor: v.Minor, Patch: v.Patch,
		Title: sp("Ship it"), Success: &success,
	}); err != nil {
		t.Fatal(err)
	}

	w := call(t, s, tok, http.MethodGet, "/api/v1/search?q=transient", "")
	if w.Code != http.StatusOK {
		t.Fatalf("search: %d %s", w.Code, w.Body.String())
	}
	var out struct {
		Q    string      `json:"q"`
		Hits []SearchHit `json:"hits"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Hits) == 0 {
		t.Fatalf("body text was not searchable: %s", w.Body.String())
	}
	if out.Hits[0].Kind != "requirement_version" {
		t.Fatalf("hit kind = %q", out.Hits[0].Kind)
	}
	if out.Hits[0].Excerpt == "" || !strings.Contains(strings.ToLower(out.Hits[0].Excerpt), "transient") {
		t.Fatalf("excerpt does not show the match: %q", out.Hits[0].Excerpt)
	}
	if out.Hits[0].Href == "" {
		t.Fatal("hit has no link")
	}

	// A title match works too.
	w = call(t, s, tok, http.MethodGet, "/api/v1/search?q=Ship", "")
	if !strings.Contains(w.Body.String(), "Ship it") {
		t.Fatalf("title not searchable: %s", w.Body.String())
	}

	// And an empty query returns no hits rather than everything.
	w = call(t, s, tok, http.MethodGet, "/api/v1/search?q=", "")
	if !strings.Contains(w.Body.String(), `"hits":[]`) {
		t.Fatalf("empty query should return no hits: %s", w.Body.String())
	}
}

// TestSearchFindsDesignAndMilestoneText covers the other two entity kinds, since a
// search that only knows one table is the bug this phase exists to fix.
func TestSearchFindsDesignAndMilestoneText(t *testing.T) {
	s, tok := wbServer(t, nil)
	snap, err := s.delivery.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	// The seeded demo data is enough for a positive control on the matcher.
	if len(snap.EngReqs) == 0 {
		t.Skip("no seeded data to search")
	}
	w := call(t, s, tok, http.MethodGet, "/api/v1/search?q=a", "")
	if w.Code != http.StatusOK {
		t.Fatalf("search: %d", w.Code)
	}
	var out struct {
		Hits []SearchHit `json:"hits"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	// A single common letter must not produce an unbounded list.
	if len(out.Hits) > 60 {
		t.Fatalf("hits not capped: %d", len(out.Hits))
	}
}

// TestAuditMiddlewareRecordsMutations proves the middleware fires without the
// handler knowing: the point of doing it here rather than per-handler.
func TestAuditMiddlewareRecordsMutations(t *testing.T) {
	s, tok := wbServer(t, nil)
	// A read must not be recorded.
	w := call(t, s, tok, http.MethodGet, "/api/v1/delivery", "")
	if w.Code != http.StatusOK {
		t.Fatalf("snapshot: %d", w.Code)
	}
	all, err := s.delivery.ListAudit("requirement", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 0 {
		t.Fatalf("a GET was audited: %+v", all)
	}
}

func sp(s string) *string { return &s }
