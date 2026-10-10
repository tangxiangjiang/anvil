// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package web_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/anvil-dev/anvil/internal/web"
	"github.com/anvil-dev/anvil/internal/web/auth"
)

func TestSetupWizardThenLogin(t *testing.T) {
	dir := t.TempDir()
	cfg := web.DefaultConfig()
	cfg.DBPath = filepath.Join(dir, "setup.db")
	cfg.SchedulerURL = "http://127.0.0.1:1"
	_ = cfg.Normalize()
	store, err := auth.Open(cfg.DBPath, 7*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	static, err := web.StaticFS()
	if err != nil {
		t.Fatal(err)
	}
	srv := web.New(cfg, store, static, nil)
	h := srv.Handler()

	res := httptest.NewRecorder()
	h.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/v1/setup/status", nil))
	if res.Code != 200 || !bytes.Contains(res.Body.Bytes(), []byte(`"needs_setup":true`)) {
		t.Fatalf("%d %s", res.Code, res.Body.String())
	}

	body, _ := json.Marshal(map[string]string{"username": "root", "password": "password1"})
	res = httptest.NewRecorder()
	h.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/api/v1/setup/admin", bytes.NewReader(body)))
	if res.Code != 200 {
		t.Fatalf("setup %d %s", res.Code, res.Body.String())
	}
	var out struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(res.Body.Bytes(), &out)
	if out.Token == "" {
		t.Fatal("expected token after setup")
	}

	res = httptest.NewRecorder()
	h.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/api/v1/setup/admin", bytes.NewReader(body)))
	if res.Code != 409 {
		t.Fatalf("want 409 got %d", res.Code)
	}
}

func TestAuthLoginMe(t *testing.T) {
	dir := t.TempDir()
	cfg := web.DefaultConfig()
	cfg.DBPath = filepath.Join(dir, "w.db")
	cfg.SchedulerURL = "http://127.0.0.1:1"
	_ = cfg.Normalize()
	store, err := auth.Open(cfg.DBPath, 7*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.EnsureAdmin("admin", "password1"); err != nil {
		t.Fatal(err)
	}
	static, err := web.StaticFS()
	if err != nil {
		t.Fatal(err)
	}
	srv := web.New(cfg, store, static, nil)
	h := srv.Handler()

	body, _ := json.Marshal(map[string]string{"username": "admin", "password": "password1"})
	res := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	h.ServeHTTP(res, req)
	if res.Code != 200 {
		t.Fatalf("login %d %s", res.Code, res.Body.String())
	}
	var out struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(res.Body.Bytes(), &out)
	if out.Token == "" {
		t.Fatal("no token")
	}

	res = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	req.Header.Set("Authorization", "Bearer "+out.Token)
	h.ServeHTTP(res, req)
	if res.Code != 200 {
		t.Fatalf("me %d", res.Code)
	}

	res = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	h.ServeHTTP(res, req)
	if res.Code != 401 {
		t.Fatalf("want 401 got %d", res.Code)
	}

	res = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(res, req)
	if res.Code != 200 {
		t.Fatalf("index %d", res.Code)
	}
}
