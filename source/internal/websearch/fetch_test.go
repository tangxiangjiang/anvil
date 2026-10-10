// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package websearch_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anvil-dev/anvil/internal/websearch"
)

func TestFetchSSRFBlocks(t *testing.T) {
	cases := []string{
		"http://127.0.0.1/",
		"http://10.0.0.1/x",
		"http://192.168.1.1/",
		"http://169.254.169.254/latest/meta-data/",
		"http://localhost/",
		"file:///etc/passwd",
	}
	for _, u := range cases {
		_, err := websearch.FetchURL(context.Background(), u, websearch.FetchOpts{})
		if err == nil {
			t.Fatalf("expected deny for %s", u)
		}
	}
}

func TestFetchPublicFixture(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!DOCTYPE html><html><body><h1>Hello Fixture</h1><p>Body text.</p></body></html>`))
	}))
	defer srv.Close()

	// httptest is loopback — require AllowPrivateURLs for the happy-path fixture.
	text, err := websearch.FetchURL(context.Background(), srv.URL, websearch.FetchOpts{
		AllowPrivateURLs: true,
		MaxBytes:         4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "Hello Fixture") || strings.Contains(text, "<h1>") {
		t.Fatalf("%q", text)
	}
}

func TestFetchPrivateDeniedByDefault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("secret"))
	}))
	defer srv.Close()
	_, err := websearch.FetchURL(context.Background(), srv.URL, websearch.FetchOpts{})
	if err == nil || !strings.Contains(err.Error(), "SSRF") {
		t.Fatalf("err=%v", err)
	}
}
