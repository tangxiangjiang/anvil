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
	"github.com/anvil-dev/anvil/internal/web/delivery"
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
	srv := web.New(cfg, store, nil, static, nil)
	h := srv.Handler()

	res := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/setup/status", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	h.ServeHTTP(res, req)
	if res.Code != 200 || !bytes.Contains(res.Body.Bytes(), []byte(`"needs_setup":true`)) {
		t.Fatalf("%d %s", res.Code, res.Body.String())
	}
	if !bytes.Contains(res.Body.Bytes(), []byte(`"setup_allowed":true`)) {
		t.Fatalf("expected setup_allowed true: %s", res.Body.String())
	}

	body, _ := json.Marshal(map[string]string{"username": "root", "password": "password1"})
	res = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/setup/admin", bytes.NewReader(body))
	req.RemoteAddr = "192.168.1.10:9999"
	h.ServeHTTP(res, req)
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
	req = httptest.NewRequest(http.MethodPost, "/api/v1/setup/admin", bytes.NewReader(body))
	req.RemoteAddr = "127.0.0.1:1"
	h.ServeHTTP(res, req)
	if res.Code != 409 {
		t.Fatalf("want 409 got %d", res.Code)
	}
}

func TestSetupBlockedFromPublicIP(t *testing.T) {
	dir := t.TempDir()
	cfg := web.DefaultConfig()
	cfg.DBPath = filepath.Join(dir, "block.db")
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
	h := web.New(cfg, store, nil, static, nil).Handler()

	res := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/setup/status", nil)
	req.RemoteAddr = "8.8.8.8:443"
	h.ServeHTTP(res, req)
	if res.Code != 200 {
		t.Fatalf("%d %s", res.Code, res.Body.String())
	}
	if !bytes.Contains(res.Body.Bytes(), []byte(`"needs_setup":true`)) ||
		!bytes.Contains(res.Body.Bytes(), []byte(`"setup_allowed":false`)) {
		t.Fatalf("body %s", res.Body.String())
	}

	body, _ := json.Marshal(map[string]string{"username": "root", "password": "password1"})
	res = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/setup/admin", bytes.NewReader(body))
	req.RemoteAddr = "10.0.0.5:80"
	h.ServeHTTP(res, req)
	if res.Code != 403 {
		t.Fatalf("want 403 got %d %s", res.Code, res.Body.String())
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
	srv := web.New(cfg, store, nil, static, nil)
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

	res = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/fleets", nil)
	req.Header.Set("Authorization", "Bearer "+out.Token)
	h.ServeHTTP(res, req)
	if res.Code != 200 || !bytes.Contains(res.Body.Bytes(), []byte(`"fleets"`)) {
		t.Fatalf("fleets %d %s", res.Code, res.Body.String())
	}

	res = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/clusters", nil)
	req.Header.Set("Authorization", "Bearer "+out.Token)
	h.ServeHTTP(res, req)
	if res.Code != 200 || !bytes.Contains(res.Body.Bytes(), []byte(`"clusters"`)) {
		t.Fatalf("clusters %d %s", res.Code, res.Body.String())
	}
	// scheduler at :1 is unreachable — expect online:false
	if !bytes.Contains(res.Body.Bytes(), []byte(`"online":false`)) {
		t.Fatalf("expected offline cluster: %s", res.Body.String())
	}
}

func TestDeliveryAPI(t *testing.T) {
	dir := t.TempDir()
	cfg := web.DefaultConfig()
	cfg.DBPath = filepath.Join(dir, "del.db")
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
	del, err := delivery.Open(store.DB())
	if err != nil {
		t.Fatal(err)
	}
	if err := del.EnsureSeed(); err != nil {
		t.Fatal(err)
	}
	static, err := web.StaticFS()
	if err != nil {
		t.Fatal(err)
	}
	h := web.New(cfg, store, del, static, nil).Handler()

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

	res = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/delivery", nil)
	req.Header.Set("Authorization", "Bearer "+out.Token)
	h.ServeHTTP(res, req)
	if res.Code != 200 || !bytes.Contains(res.Body.Bytes(), []byte(`"eng_reqs"`)) {
		t.Fatalf("snapshot %d %s", res.Code, res.Body.String())
	}

	create, _ := json.Marshal(map[string]any{
		"worker_id": "w_api", "label": "api-w", "fleet": "f", "node": "n",
		"project_rel": "p", "title": "API eng", "reqs_rel": "docs/reqs",
		"major": 0,
	})
	res = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/delivery/eng-reqs", bytes.NewReader(create))
	req.Header.Set("Authorization", "Bearer "+out.Token)
	h.ServeHTTP(res, req)
	if res.Code != 201 {
		t.Fatalf("create %d %s", res.Code, res.Body.String())
	}
	var eng struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(res.Body.Bytes(), &eng)

	verBody, _ := json.Marshal(map[string]any{"major": 0, "minor": 2, "patch": 1, "title": "M2"})
	res = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/delivery/eng-reqs/"+eng.ID+"/versions", bytes.NewReader(verBody))
	req.Header.Set("Authorization", "Bearer "+out.Token)
	h.ServeHTTP(res, req)
	if res.Code != 201 {
		t.Fatalf("add ver %d %s", res.Code, res.Body.String())
	}

	patch, _ := json.Marshal(map[string]string{"problem": "need schema"})
	res = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPatch, "/api/v1/delivery/eng-reqs/"+eng.ID+"/versions/0/2/1", bytes.NewReader(patch))
	req.Header.Set("Authorization", "Bearer "+out.Token)
	h.ServeHTTP(res, req)
	if res.Code != 200 || !bytes.Contains(res.Body.Bytes(), []byte(`need schema`)) {
		t.Fatalf("patch %d %s", res.Code, res.Body.String())
	}

	res = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/delivery/eng-reqs/"+eng.ID+"/versions/0/2/1/approve", bytes.NewReader([]byte("{}")))
	req.Header.Set("Authorization", "Bearer "+out.Token)
	h.ServeHTTP(res, req)
	if res.Code != 200 || !bytes.Contains(res.Body.Bytes(), []byte(`"approved"`)) {
		t.Fatalf("approve %d %s", res.Code, res.Body.String())
	}

	majBody, _ := json.Marshal(map[string]any{"title": "2.0", "status": "planning"})
	res = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/delivery/majors", bytes.NewReader(majBody))
	req.Header.Set("Authorization", "Bearer "+out.Token)
	h.ServeHTTP(res, req)
	if res.Code != 201 || !bytes.Contains(res.Body.Bytes(), []byte(`"major":2`)) {
		t.Fatalf("add major %d %s", res.Code, res.Body.String())
	}
}
