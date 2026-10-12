// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package scheduler_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/anvil-dev/anvil/internal/scheduler"
	"github.com/anvil-dev/anvil/internal/web/auth"
)

func TestAppAuthAcceptsWebSession(t *testing.T) {
	t.Setenv("ANVIL_SCHEDULER_TOKEN", "sched-secret")
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "web.db")
	store, err := auth.Open(dbPath, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.EnsureAdmin("admin", "password1"); err != nil {
		t.Fatal(err)
	}
	tok, _, err := store.Login("admin", "password1")
	if err != nil {
		t.Fatal(err)
	}

	cfg := scheduler.DefaultConfig()
	cfg.TokenEnv = "ANVIL_SCHEDULER_TOKEN"
	hub := scheduler.NewHub(cfg, nil)
	hub.SetSessions(store)

	srv := httptest.NewServer(hub.AppHandler())
	t.Cleanup(srv.Close)

	get := func(authz string) int {
		req, err := http.NewRequest(http.MethodGet, srv.URL+"/v1/health", nil)
		if err != nil {
			t.Fatal(err)
		}
		if authz != "" {
			req.Header.Set("Authorization", authz)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.ReadAll(res.Body)
		res.Body.Close()
		return res.StatusCode
	}

	if code := get("Bearer sched-secret"); code != http.StatusOK {
		t.Fatalf("static token: %d", code)
	}
	if code := get("Bearer " + tok); code != http.StatusOK {
		t.Fatalf("session token: %d", code)
	}
	if code := get("Bearer garbage"); code != http.StatusUnauthorized {
		t.Fatalf("garbage: want 401 got %d", code)
	}
	if code := get(""); code != http.StatusUnauthorized {
		t.Fatalf("missing: want 401 got %d", code)
	}
}

func TestMetaEndpoint(t *testing.T) {
	t.Setenv("ANVIL_SCHEDULER_TOKEN", "sched-secret")
	cfg := scheduler.DefaultConfig()
	cfg.TokenEnv = "ANVIL_SCHEDULER_TOKEN"
	cfg.Name = "Test sched"
	cfg.Description = "meta test"
	cfg.ListenApp = "127.0.0.1:17800"
	cfg.ListenCluster = "tcp://127.0.0.1:17777"
	hub := scheduler.NewHub(cfg, nil)
	srv := httptest.NewServer(hub.AppHandler())
	t.Cleanup(srv.Close)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/meta", nil)
	req.Header.Set("Authorization", "Bearer sched-secret")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	var meta map[string]string
	if err := json.NewDecoder(res.Body).Decode(&meta); err != nil {
		t.Fatal(err)
	}
	if meta["name"] != "Test sched" || meta["description"] != "meta test" {
		t.Fatalf("%v", meta)
	}
	if meta["listen_app"] == "" || meta["listen_cluster"] == "" {
		t.Fatalf("missing listen fields: %v", meta)
	}
}
