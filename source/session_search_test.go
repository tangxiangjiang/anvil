// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/anvil-dev/anvil/internal/store"
)

func TestSessionQueryParity(t *testing.T) {
	jsonDir := t.TempDir()
	sqliteDir := t.TempDir()
	seedSessionSearchCorpus(t, jsonDir, store.KindJSONL)
	seedSessionSearchCorpus(t, sqliteDir, store.KindSQLite)

	queries := []SessionSearchOptions{
		{Query: `niagara "shader failure" -resolved`},
		{Query: `role:tool kind:message permutation`},
		{Query: `status:error shader`},
		{Query: `alpha omega`},
		{Query: `kind:plan package acceptance`},
		{Query: `task title`},
		{Query: ``},
		{Query: `secret-build`, IncludeArgs: true},
		{Query: `shader`, AgentID: "agent-alpha"},
		{Query: `shader`, Limit: 1},
	}
	for _, query := range queries {
		query := query
		t.Run(query.Query, func(t *testing.T) {
			jsonOpts := query
			jsonOpts.Local = LocalOptions{CWD: jsonDir, Store: store.KindJSONL}
			sqliteOpts := query
			sqliteOpts.Local = LocalOptions{CWD: sqliteDir, Store: store.KindSQLite}
			gotJSON, err := SearchSessions(context.Background(), jsonOpts)
			if err != nil {
				t.Fatal(err)
			}
			gotSQLite, err := SearchSessions(context.Background(), sqliteOpts)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(gotJSON, gotSQLite) {
				t.Fatalf("JSONL=%+v\nSQLite=%+v", gotJSON, gotSQLite)
			}
		})
	}
}

func TestSessionQueryJSONL(t *testing.T) {
	dir := t.TempDir()
	seedSessionSearchCorpus(t, dir, store.KindJSONL)
	search := func(query string) SessionSearchResult {
		t.Helper()
		result, err := SearchSessions(context.Background(), SessionSearchOptions{
			Local: LocalOptions{CWD: dir, Store: store.KindJSONL}, Query: query,
		})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}

	unicodeResult := search(`ＮＩＡＧＡＲＡ café`)
	if len(unicodeResult.Hits) != 1 || unicodeResult.Hits[0].AgentID != "agent-alpha" {
		t.Fatalf("unicode result=%+v", unicodeResult)
	}
	if len(unicodeResult.Hits[0].Match) != 2 {
		t.Fatalf("match ranges=%+v", unicodeResult.Hits[0].Match)
	}
	for _, match := range unicodeResult.Hits[0].Match {
		if match.Start < 0 || match.End <= match.Start ||
			match.End > utf8.RuneCountInString(unicodeResult.Hits[0].Snippet) {
			t.Fatalf("invalid match %+v in %q", match, unicodeResult.Hits[0].Snippet)
		}
	}

	tool := search(`role:tool kind:message permutation`)
	if len(tool.Hits) != 1 || tool.Hits[0].Role != "tool" || tool.Hits[0].Name != "shell" {
		t.Fatalf("tool result=%+v", tool)
	}
	errorResult := search(`status:error shader`)
	if len(errorResult.Hits) != 1 || errorResult.Hits[0].Kind != "error" {
		t.Fatalf("error result=%+v", errorResult)
	}
	interrupted := search(`status:interrupted ownership`)
	if len(interrupted.Hits) != 1 || interrupted.Hits[0].RunID != "run-interrupted" {
		t.Fatalf("interrupted result=%+v", interrupted)
	}
	excluded := search(`shader -permutation`)
	for _, hit := range excluded.Hits {
		if hit.Role == "tool" {
			t.Fatalf("excluded tool hit=%+v", hit)
		}
	}
	punctuation := search(`shader_error`)
	if len(punctuation.Hits) != 1 || punctuation.Hits[0].Kind != "error" {
		t.Fatalf("punctuation result=%+v", punctuation)
	}
	if got := search(`after:2026-11-02 shader`); len(got.Hits) != 0 {
		t.Fatalf("after filter=%+v", got)
	}
	metadata := search("")
	if len(metadata.Hits) != 2 ||
		metadata.Hits[0].Kind != "meta" || metadata.Hits[1].Kind != "meta" {
		t.Fatalf("empty query must remain metadata-only: %+v", metadata)
	}
}

func TestSessionQueryJSONLSnippetBound(t *testing.T) {
	dir := t.TempDir()
	seedSessionSearchCorpus(t, dir, store.KindJSONL)
	backend := store.New(dir)
	if err := backend.Append("agent-alpha", store.Record{
		Type: store.TypeMessage, RunID: "run-long", Role: "assistant",
		Time:    time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC),
		Content: strings.Repeat("左", 300) + " NEEDLE " + strings.Repeat("右", 300),
	}); err != nil {
		t.Fatal(err)
	}
	result, err := SearchSessions(context.Background(), SessionSearchOptions{
		Local: LocalOptions{CWD: dir, Store: store.KindJSONL}, Query: "needle",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Hits) != 1 {
		t.Fatalf("result=%+v", result)
	}
	hit := result.Hits[0]
	if got := utf8.RuneCountInString(hit.Snippet); got > MaxSessionSnippetRunes {
		t.Fatalf("snippet has %d runes: %q", got, hit.Snippet)
	}
	if !strings.HasPrefix(hit.Snippet, "…") || !strings.HasSuffix(hit.Snippet, "…") ||
		len(hit.Match) != 1 {
		t.Fatalf("snippet=%q match=%+v", hit.Snippet, hit.Match)
	}
}

func TestSessionQueryJSONLCaseFoldRange(t *testing.T) {
	snippet, ranges := sessionSearchSnippet("aß", []sessionSearchTerm{{text: "s"}})
	if snippet != "aß" || len(ranges) != 1 || ranges[0] != (TextRange{Start: 1, End: 2}) {
		t.Fatalf("snippet=%q ranges=%+v", snippet, ranges)
	}
}

func TestSessionQueryPrivacy(t *testing.T) {
	dir := t.TempDir()
	seedSessionSearchCorpus(t, dir, store.KindJSONL)
	var networkCalls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		networkCalls.Add(1)
	}))
	defer server.Close()
	t.Setenv("ANVIL_BASE_URL", server.URL)
	t.Setenv("ANVIL_API_KEY", "must-not-be-used")

	base := SessionSearchOptions{
		Local: LocalOptions{CWD: dir, Store: store.KindJSONL}, Query: "secret-build",
	}
	hidden, err := SearchSessions(context.Background(), base)
	if err != nil {
		t.Fatal(err)
	}
	if len(hidden.Hits) != 0 {
		t.Fatalf("tool args leaked by default: %+v", hidden.Hits)
	}
	base.IncludeArgs = true
	included, err := SearchSessions(context.Background(), base)
	if err != nil {
		t.Fatal(err)
	}
	if len(included.Hits) != 1 || included.Hits[0].Name != "shell" {
		t.Fatalf("explicit args result=%+v", included)
	}
	if err := store.New(dir).Append("agent-alpha", store.Record{
		Type: store.TypeWorkState, RunID: "run-private", ArtifactID: "artifact-private",
		ItemID: "private", State: "completed", Evidence: []byte(`{"secret":"private-evidence"}`),
	}); err != nil {
		t.Fatal(err)
	}
	privateResult, err := SearchSessions(context.Background(), SessionSearchOptions{
		Local: LocalOptions{CWD: dir, Store: store.KindJSONL},
		Query: "private-evidence", IncludeArgs: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(privateResult.Hits) != 0 {
		t.Fatalf("undeclared workflow evidence was searched: %+v", privateResult.Hits)
	}
	if got := networkCalls.Load(); got != 0 {
		t.Fatalf("local search made %d network calls", got)
	}
}

func TestSessionQueryValidationAndCancellation(t *testing.T) {
	dir := t.TempDir()
	seedSessionSearchCorpus(t, dir, store.KindJSONL)
	for _, opts := range []SessionSearchOptions{
		{Query: `"unterminated`},
		{Query: `role:unknown`},
		{Query: `after:2026-99-99`},
		{Query: `-kind:message`},
		{Query: `role:user`, Roles: []string{"assistant"}},
		{Limit: -1},
		{Cursor: "not-yet-supported"},
	} {
		opts.Local = LocalOptions{CWD: dir, Store: store.KindJSONL}
		_, err := SearchSessions(context.Background(), opts)
		var queryErr *SessionSearchQueryError
		if !errors.As(err, &queryErr) {
			t.Fatalf("opts=%+v error=%v", opts, err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := SearchSessions(ctx, SessionSearchOptions{
		Local: LocalOptions{CWD: dir, Store: store.KindJSONL}, Query: "shader",
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error=%v", err)
	}

	backend := store.New(dir)
	midCtx, midCancel := context.WithCancel(context.Background())
	wrapped := &cancelAfterSearchLoad{Backend: backend, cancel: midCancel}
	opts := SessionSearchOptions{
		Local: LocalOptions{CWD: dir, Store: store.KindJSONL}, Query: "shader",
	}
	compiled, err := compileSessionSearch(opts)
	if err != nil {
		t.Fatal(err)
	}
	_, err = searchSessionsBackend(midCtx, opts, compiled, DefaultSessionSearchLimit, wrapped)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("mid-scan cancel error=%v", err)
	}
}

func TestSessionQueryAppendsUseLoadedRecordSnapshot(t *testing.T) {
	for _, kind := range []string{store.KindJSONL, store.KindSQLite} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			seedSessionSearchCorpus(t, dir, kind)
			backend, err := store.Open(dir, kind)
			if err != nil {
				t.Fatal(err)
			}
			defer backend.Close()
			wrapped := &appendAfterSearchLoad{
				Backend: backend,
				append: func() error {
					return backend.Append("agent-alpha", store.Record{
						Type: store.TypeMessage, RunID: "run-late", Role: "user",
						Content: "late-arrival", Time: time.Date(2026, 11, 1, 11, 0, 0, 0, time.UTC),
					})
				},
			}
			opts := SessionSearchOptions{
				Local: LocalOptions{CWD: dir, Store: kind},
				Query: "late-arrival", AgentID: "agent-alpha",
			}
			compiled, err := compileSessionSearch(opts)
			if err != nil {
				t.Fatal(err)
			}
			first, err := searchSessionsBackend(
				context.Background(), opts, compiled, DefaultSessionSearchLimit, wrapped,
			)
			if err != nil {
				t.Fatal(err)
			}
			if len(first.Hits) != 0 {
				t.Fatalf("record appended after Load entered the active scan: %+v", first)
			}
			second, err := SearchSessions(context.Background(), opts)
			if err != nil {
				t.Fatal(err)
			}
			if len(second.Hits) != 1 || second.Hits[0].RunID != "run-late" {
				t.Fatalf("next scan did not observe append: %+v", second)
			}
		})
	}
}

func TestSessionQueryDeletedSessionsAreAbsent(t *testing.T) {
	for _, kind := range []string{store.KindJSONL, store.KindSQLite} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			seedSessionSearchCorpus(t, dir, kind)
			backend, err := store.Open(dir, kind)
			if err != nil {
				t.Fatal(err)
			}
			if err := backend.Delete("agent-beta"); err != nil {
				t.Fatal(err)
			}
			if err := backend.Close(); err != nil {
				t.Fatal(err)
			}
			result, err := SearchSessions(context.Background(), SessionSearchOptions{
				Local: LocalOptions{CWD: dir, Store: kind}, Query: "task title",
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Hits) != 0 {
				t.Fatalf("deleted session result=%+v", result)
			}
		})
	}
}

func TestSessionQuerySQLite(t *testing.T) {
	dir := t.TempDir()
	seedSessionSearchCorpus(t, dir, store.KindSQLite)
	result, err := SearchSessions(context.Background(), SessionSearchOptions{
		Local: LocalOptions{CWD: dir, Store: store.KindSQLite}, Query: "tie",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Hits) != 2 ||
		result.Hits[0].AgentID != "agent-alpha" ||
		result.Hits[1].AgentID != "agent-beta" {
		t.Fatalf("tie order=%+v", result.Hits)
	}
	spread, err := SearchSessions(context.Background(), SessionSearchOptions{
		Local: LocalOptions{CWD: dir, Store: store.KindSQLite}, Query: "alpha omega",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(spread.Hits) != 1 || spread.Hits[0].RunID != "run-spread" {
		t.Fatalf("spread=%+v", spread)
	}
}

type appendAfterSearchLoad struct {
	store.Backend
	once   sync.Once
	append func() error
	err    error
}

type cancelAfterSearchLoad struct {
	store.Backend
	cancel context.CancelFunc
}

func (b *cancelAfterSearchLoad) Load(agentID string) ([]store.Record, error) {
	records, err := b.Backend.Load(agentID)
	b.cancel()
	return records, err
}

func (b *appendAfterSearchLoad) Load(agentID string) ([]store.Record, error) {
	records, err := b.Backend.Load(agentID)
	if err != nil {
		return nil, err
	}
	b.once.Do(func() { b.err = b.append() })
	if b.err != nil {
		return nil, b.err
	}
	return records, nil
}

func seedSessionSearchCorpus(t *testing.T, dir, kind string) {
	t.Helper()
	backend, err := store.Open(dir, kind)
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	base := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	appendRecord := func(agentID string, rec store.Record) {
		t.Helper()
		if err := backend.Append(agentID, rec); err != nil {
			t.Fatal(err)
		}
	}
	appendRecord("agent-alpha", store.Record{
		Type: store.TypeMeta, Time: base, AgentID: "agent-alpha",
		CWD: dir, Provider: "openai", Model: "test", Title: "Release café",
	})
	appendRecord("agent-alpha", store.Record{
		Type: store.TypeMessage, Time: base.Add(time.Minute), RunID: "run-alpha",
		Role: "user", Content: "Investigate Ｎｉａｇａｒａ shader failure and café rendering",
	})
	appendRecord("agent-alpha", store.Record{
		Type: store.TypeMessage, Time: base.Add(2 * time.Minute), RunID: "run-alpha",
		Role: "assistant", Content: "I will inspect the engine.", ToolCalls: []store.ToolCall{{
			ID: "tool-1", Name: "shell", Args: []byte(`{"command":"secret-build"}`),
		}},
	})
	appendRecord("agent-alpha", store.Record{
		Type: store.TypeMessage, Time: base.Add(3 * time.Minute), RunID: "run-alpha",
		Role: "tool", Name: "shell", Content: "Missing global shader permutation",
	})
	appendRecord("agent-alpha", store.Record{
		Type: store.TypeRunEnd, Time: base.Add(4 * time.Minute), RunID: "run-alpha",
		Status: "error", Code: "shader_error", Err: "shader compilation failed",
	})
	artifact, err := planArtifactRecord(PlanArtifact{
		Schema: PlanSchemaVersion, ArtifactID: "artifact-release", RunID: "run-plan",
		Kind: PlanKindComplex, Revision: 1, Title: "Package release", Body: "Build package",
		Items: []WorkItem{{
			ID: "build", Title: "Build", Goal: "Package is ready",
			Plan: []string{"compile package"}, Files: []string{},
			Acceptance: []string{"package acceptance passes"}, DependsOn: []string{},
			Approval: WorkApprovalPending, State: WorkStateProposed,
			Source: WorkSourceComplex, ExecutionIDs: []string{},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	artifact.Time = base.Add(5 * time.Minute)
	appendRecord("agent-alpha", artifact)
	appendRecord("agent-alpha", store.Record{
		Type: store.TypeMessage, Time: base.Add(10 * time.Minute), RunID: "run-tie",
		Role: "user", Content: "tie",
	})

	appendRecord("agent-beta", store.Record{
		Type: store.TypeMeta, Time: base, AgentID: "agent-beta",
		CWD: dir, Provider: "openai", Model: "test", Title: "Task title",
		ParentAgentID: "agent-alpha", Kind: "subagent", TaskTitle: "Task title worker",
	})
	appendRecord("agent-beta", store.Record{
		Type: store.TypeMessage, Time: base.Add(6 * time.Minute), RunID: "run-spread",
		Role: "user", Content: "alpha starts here",
	})
	appendRecord("agent-beta", store.Record{
		Type: store.TypeMessage, Time: base.Add(7 * time.Minute), RunID: "run-spread",
		Role: "assistant", Content: "omega finishes here",
	})
	appendRecord("agent-beta", store.Record{
		Type: store.TypeRunState, Time: base.Add(8 * time.Minute), RunID: "run-interrupted",
		RunState: "interrupted", Cause: "host ownership was lost",
	})
	appendRecord("agent-beta", store.Record{
		Type: store.TypeMessage, Time: base.Add(10 * time.Minute), RunID: "run-tie",
		Role: "user", Content: "tie",
	})
}
