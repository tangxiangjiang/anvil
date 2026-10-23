// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package web

import (
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/anvil-dev/anvil/internal/web/auth"
)

func TestClusterReviewBFFScopeAuthAndStructuredErrors(t *testing.T) {
	t.Setenv("ANVIL_SCHEDULER_TOKEN", "scheduler-secret")
	var calls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer scheduler-secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.URL.Query().Get("worker") != "worker-1" {
			http.Error(w, "bad worker", http.StatusBadRequest)
			return
		}
		switch r.URL.Path {
		case "/v1/nodes/node-1/sandbox/file":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true, "path": r.URL.Query().Get("path"), "content": "<script>x</script>", "encoding": "utf-8",
			})
		case "/v1/nodes/node-1/git/status":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"ok":false,"code":"not_repository","error":"not a repository"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	store, err := auth.Open(filepath.Join(t.TempDir(), "review.db"), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.CreateAdmin("admin", "password123"); err != nil {
		t.Fatal(err)
	}
	token, _, err := store.Login("admin", "password123")
	if err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.DBPath = filepath.Join(t.TempDir(), "unused.db")
	cfg.SchedulerURL = upstream.URL
	cfg.Fleets = []FleetEntry{{ID: "fleet-1", Name: "Fleet", SchedulerURL: upstream.URL}}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	server := New(cfg, store, nil, nil, nil, log.New(discard{}, "", 0))

	filePath := "/api/v1/clusters/fleet-1/nodes/node-1/sandbox/file?worker=worker-1&path=" +
		url.QueryEscape("docs/xss.md")
	unauth := httptest.NewRecorder()
	server.Handler().ServeHTTP(unauth, httptest.NewRequest(http.MethodGet, filePath, nil))
	if unauth.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated = %d", unauth.Code)
	}
	got := call(t, server, token, http.MethodGet, filePath, "")
	if got.Code != http.StatusOK || got.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("file = %d %s", got.Code, got.Body.String())
	}
	var file map[string]any
	_ = json.Unmarshal(got.Body.Bytes(), &file)
	if file["content"] != "<script>x</script>" || file["path"] != "docs/xss.md" {
		t.Fatalf("file = %#v", file)
	}

	bad := call(t, server, token, http.MethodGet,
		"/api/v1/clusters/fleet-1/nodes/node-1/sandbox/file?worker=../other&path=x", "")
	if bad.Code != http.StatusBadRequest || calls != 1 {
		t.Fatalf("invalid scope = %d calls=%d body=%s", bad.Code, calls, bad.Body.String())
	}

	status := call(t, server, token, http.MethodGet,
		"/api/v1/clusters/fleet-1/nodes/node-1/git/status?worker=worker-1", "")
	if status.Code != http.StatusBadRequest {
		t.Fatalf("status = %d %s", status.Code, status.Body.String())
	}
	var problem map[string]any
	_ = json.Unmarshal(status.Body.Bytes(), &problem)
	if problem["code"] != "not_repository" {
		t.Fatalf("structured error = %#v", problem)
	}
}
