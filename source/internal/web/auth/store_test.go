// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package auth_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/anvil-dev/anvil/internal/web/auth"
)

func TestLoginSessionTTL(t *testing.T) {
	dir := t.TempDir()
	store, err := auth.Open(filepath.Join(dir, "web.db"), 7*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.EnsureAdmin("admin", "secret12"); err != nil {
		t.Fatal(err)
	}
	tok, user, err := store.Login("admin", "secret12")
	if err != nil || tok == "" || user.Username != "admin" {
		t.Fatalf("%v %+v", err, user)
	}
	u2, err := store.LookupSession(tok)
	if err != nil || u2.Username != "admin" {
		t.Fatal(err)
	}
	if _, _, err := store.Login("admin", "wrong"); err == nil {
		t.Fatal("expected bad password")
	}
	store.Logout(tok)
	if _, err := store.LookupSession(tok); err == nil {
		t.Fatal("expected logout")
	}
}

func TestCreateAdminWizard(t *testing.T) {
	dir := t.TempDir()
	store, err := auth.Open(filepath.Join(dir, "web.db"), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	need, err := store.NeedsSetup()
	if err != nil || !need {
		t.Fatalf("need=%v err=%v", need, err)
	}
	if _, err := store.CreateAdmin("root", "short"); err == nil {
		t.Fatal("expected short password error")
	}
	u, err := store.CreateAdmin("root", "longenough")
	if err != nil || u.Username != "root" {
		t.Fatalf("%v %+v", err, u)
	}
	need, _ = store.NeedsSetup()
	if need {
		t.Fatal("setup should be done")
	}
	if _, err := store.CreateAdmin("other", "longenough"); err == nil {
		t.Fatal("expected already completed")
	}
}

func TestExpiredSession(t *testing.T) {
	dir := t.TempDir()
	store, err := auth.Open(filepath.Join(dir, "web.db"), time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	_ = store.EnsureAdmin("a", "password1")
	tok, _, err := store.Login("a", "password1")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	if _, err := store.LookupSession(tok); err == nil {
		t.Fatal("expected expired")
	}
}
