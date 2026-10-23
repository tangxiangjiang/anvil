// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package delivery

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anvil-dev/anvil/internal/web/auth"
)

func createSearchRequirement(t *testing.T, s *Store, worker, project, workspace, title, body string) (EngReq, ReqVersion) {
	t.Helper()
	eng, err := s.CreateEngReq(CreateEngReqInput{
		Worker:      WorkerRef{ID: worker, Label: worker},
		ProjectID:   project,
		WorkspaceID: workspace,
		Title:       title,
		AutoMajor:   true,
		Success:     body,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(eng.Versions) == 0 {
		t.Fatal("created requirement has no version")
	}
	return eng, eng.Versions[0]
}

func hasSearchHit(hits []SearchResult, kind, id string) bool {
	for _, hit := range hits {
		if hit.Kind == kind && (id == "" || hit.ID == id) {
			return true
		}
	}
	return false
}

func TestSearchFTSIncrementalUnicodeAndSafety(t *testing.T) {
	s := openTestStore(t)
	if err := s.EnsureSeed(); err != nil {
		t.Fatal(err)
	}
	eng, v := createSearchRequirement(t, s, "search-worker", "project-a", "workspace-a",
		"FTS ranking title", "完成标准包含 雪崩熔断 and needlebody")
	id := fmt.Sprintf("%s:%d.%d.%d", eng.ID, v.Major, v.Minor, v.Patch)

	for _, q := range []string{"needlebody", "雪崩熔断"} {
		hits, err := s.Search(q, "", 50)
		if err != nil {
			t.Fatalf("search %q: %v", q, err)
		}
		if !hasSearchHit(hits, "requirement_version", id) {
			t.Fatalf("search %q did not find %s: %+v", q, id, hits)
		}
	}
	if _, err := s.Search(`"needlebody" OR * : (bad)`, "", 50); err != nil {
		t.Fatalf("special characters became invalid FTS syntax: %v", err)
	}
	hits, err := s.Search(`'"():*+-`, "", 50)
	if err != nil || len(hits) != 0 {
		t.Fatalf("punctuation-only query = %+v, %v", hits, err)
	}
	if _, err := s.Search(strings.Repeat("界", searchMaxQueryRunes+1), "", 50); !errors.Is(err, ErrSearchQueryTooLong) {
		t.Fatalf("long query error = %v", err)
	}

	changed := "incremental-sync-token"
	if _, err := s.UpdateVersion(UpdateVersionInput{
		EngReqID: eng.ID, Major: v.Major, Minor: v.Minor, Patch: v.Patch, Success: &changed,
	}); err != nil {
		t.Fatal(err)
	}
	hits, err = s.Search(changed, "", 50)
	if err != nil || !hasSearchHit(hits, "requirement_version", id) {
		t.Fatalf("updated body was not indexed: %+v, %v", hits, err)
	}
	hits, _ = s.Search("needlebody", "", 50)
	if hasSearchHit(hits, "requirement_version", id) {
		t.Fatalf("stale body remained indexed: %+v", hits)
	}
}

func TestSearchScopeAndSensitiveColumns(t *testing.T) {
	s := openTestStore(t)
	if err := s.EnsureSeed(); err != nil {
		t.Fatal(err)
	}
	a, _ := createSearchRequirement(t, s, "scope-worker-a", "project-a", "workspace-a", "A", "shared-scope-token")
	b, _ := createSearchRequirement(t, s, "scope-worker-b", "project-b", "workspace-b", "B", "shared-scope-token")

	hits, err := s.Search("shared-scope-token", "workspace-a", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || !strings.HasPrefix(hits[0].ID, a.ID+":") || strings.HasPrefix(hits[0].ID, b.ID+":") {
		t.Fatalf("scope leaked: %+v", hits)
	}
	if _, err := s.db.Exec(`UPDATE delivery_eng_reqs SET workspace_id=? WHERE id=?`, "workspace-a-moved", a.ID); err != nil {
		t.Fatal(err)
	}
	hits, _ = s.Search("shared-scope-token", "workspace-a", 50)
	if len(hits) != 0 {
		t.Fatalf("old scope retained rows after scope move: %+v", hits)
	}
	hits, _ = s.Search("shared-scope-token", "workspace-a-moved", 50)
	if len(hits) != 1 || !strings.HasPrefix(hits[0].ID, a.ID+":") {
		t.Fatalf("new scope was not synchronized: %+v", hits)
	}

	if err := s.BeginJob(JobRecord{JobID: "secret-job", Kind: "workbench", EngReqID: a.ID}); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendJobEvent(JobEvent{
		JobID: "secret-job", Type: "tool", Text: "safe searchable event", Args: "api_key=do-not-index-this",
	}); err != nil {
		t.Fatal(err)
	}
	hits, _ = s.Search("do-not-index-this", "", 50)
	if len(hits) != 0 {
		t.Fatalf("job args leaked through search: %+v", hits)
	}
	hits, _ = s.Search("searchable event", "workspace-a-moved", 50)
	if !hasSearchHit(hits, "job_event", "secret-job:1") {
		t.Fatalf("job history body not indexed: %+v", hits)
	}
}

func TestSearchRanksTitleBeforeBodyAndMarksSnippet(t *testing.T) {
	s := openTestStore(t)
	if err := s.EnsureSeed(); err != nil {
		t.Fatal(err)
	}
	titleEng, _ := createSearchRequirement(t, s, "rank-worker-title", "rank-title", "rank-space",
		"rankneedle", "ordinary completion")
	_, _ = createSearchRequirement(t, s, "rank-worker-body", "rank-body", "rank-space",
		"ordinary title", "the body contains rankneedle here")
	hits, err := s.Search("rankneedle", "rank-space", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) < 2 || !strings.HasPrefix(hits[0].ID, titleEng.ID+":") {
		t.Fatalf("title weight did not rank first: %+v", hits)
	}
	foundMarkedBody := false
	for _, hit := range hits {
		if strings.Contains(hit.Excerpt, "‹rankneedle›") {
			foundMarkedBody = true
		}
	}
	if !foundMarkedBody {
		t.Fatalf("snippet/highlight markers missing: %+v", hits)
	}
}

func TestSearchBackfillReopenAndDeleteRestore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "search-reopen.db")
	as, err := auth.Open(path, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(as.DB())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureSeed(); err != nil {
		t.Fatal(err)
	}
	eng, v := createSearchRequirement(t, s, "reopen-worker", "reopen-project", "reopen-workspace",
		"Reopen", "backfill-reopen-token")
	id := fmt.Sprintf("%s:%d.%d.%d", eng.ID, v.Major, v.Minor, v.Patch)

	// Simulate the pre-FTS database: source rows exist, but the migration marker
	// and index rows do not. Open must backfill them.
	if _, err := as.DB().Exec(`DELETE FROM delivery_search; DELETE FROM delivery_search_meta`); err != nil {
		t.Fatal(err)
	}
	if err := as.Close(); err != nil {
		t.Fatal(err)
	}
	as, err = auth.Open(path, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer as.Close()
	s, err = Open(as.DB())
	if err != nil {
		t.Fatal(err)
	}
	hits, err := s.Search("backfill-reopen-token", "", 50)
	if err != nil || !hasSearchHit(hits, "requirement_version", id) {
		t.Fatalf("reopen backfill failed: %+v, %v", hits, err)
	}

	// Seed major 1 is the last planning generation and therefore exercises the
	// real trash transaction. Its FTS rows disappear and return with the payload.
	seed, err := s.GetEngReq("eng_1")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.insertMilestoneVersions(seed.ID, 1, 1, "Trash search", CreateEngReqInput{
		Success: "initial restore text",
	}); err != nil {
		t.Fatal(err)
	}
	seed, err = s.GetEngReq("eng_1")
	if err != nil {
		t.Fatal(err)
	}
	var target ReqVersion
	for _, candidate := range seed.Versions {
		if candidate.Major == 1 && candidate.Minor == 1 {
			target = candidate
			break
		}
	}
	restoreText := "trash-restore-search-token"
	if _, err := s.UpdateVersion(UpdateVersionInput{
		EngReqID: seed.ID, Major: target.Major, Minor: target.Minor, Patch: target.Patch, Success: &restoreText,
	}); err != nil {
		t.Fatal(err)
	}
	item, err := s.DeleteMajorToTrash(1)
	if err != nil {
		t.Fatal(err)
	}
	hits, _ = s.Search(restoreText, "", 50)
	if len(hits) != 0 {
		t.Fatalf("deleted rows remained indexed: %+v", hits)
	}
	if _, err := s.RestoreMajorFromTrash(item.ID, ""); err != nil {
		t.Fatal(err)
	}
	hits, err = s.Search(restoreText, "", 50)
	if err != nil || len(hits) == 0 {
		t.Fatalf("restored rows were not re-indexed: %+v, %v", hits, err)
	}
}

func TestSearchIncludesAuditBodyButNotAttribution(t *testing.T) {
	s := openTestStore(t)
	if err := s.EnsureSeed(); err != nil {
		t.Fatal(err)
	}
	eng, _ := createSearchRequirement(t, s, "audit-worker", "audit-project", "audit-workspace", "Audit", "body")
	if err := s.RecordAudit(AuditEntry{
		Actor: "private-actor-token", RequestID: "private-request-token",
		Action: "PATCH requirements", EntityKind: "requirement", EntityID: eng.ID,
		Summary: "requirement updated", Detail: `{"success":"audit-body-token"}`,
	}); err != nil {
		t.Fatal(err)
	}
	hits, err := s.Search("audit-body-token", "audit-workspace", 50)
	if err != nil || !hasSearchHit(hits, "audit", "") {
		t.Fatalf("audit body not indexed: %+v, %v", hits, err)
	}
	for _, secret := range []string{"private-actor-token", "private-request-token"} {
		hits, _ = s.Search(secret, "", 50)
		if len(hits) != 0 {
			t.Fatalf("%s leaked through search: %+v", secret, hits)
		}
	}
}
