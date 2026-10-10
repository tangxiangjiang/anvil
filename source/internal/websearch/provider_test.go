// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package websearch_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/anvil-dev/anvil/internal/websearch"
)

func TestHTTPProviderSuccessBearerFromEnv(t *testing.T) {
	var sawAuth string
	var sawBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&sawBody)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]string{{
				"title": "Go site", "url": "https://go.dev", "snippet": "The Go Programming Language",
			}},
		})
	}))
	defer srv.Close()

	t.Setenv("ANVIL_WEB_SEARCH_KEY", "secret-test-key")
	p, err := websearch.NewHTTP(websearch.HTTPConfig{
		BaseURL:   srv.URL,
		APIKeyEnv: "ANVIL_WEB_SEARCH_KEY",
		Timeout:   5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	hits, err := p.Search(context.Background(), "golang", websearch.SearchOpts{MaxResults: 3})
	if err != nil {
		t.Fatal(err)
	}
	if sawAuth != "Bearer secret-test-key" {
		t.Fatalf("auth %q", sawAuth)
	}
	if sawBody["query"] != "golang" {
		t.Fatalf("body %+v", sawBody)
	}
	if len(hits) != 1 || hits[0].URL != "https://go.dev" {
		t.Fatalf("%+v", hits)
	}
	// Ensure we never accidentally put the key into hit text.
	formatted := websearch.FormatHits(hits)
	if strings.Contains(formatted, "secret-test-key") {
		t.Fatal("api key leaked into hits")
	}
	_ = os.Unsetenv("ANVIL_WEB_SEARCH_KEY")
}

func TestHTTPProviderUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	p, err := websearch.NewHTTP(websearch.HTTPConfig{BaseURL: srv.URL, APIKey: "x"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Search(context.Background(), "q", websearch.SearchOpts{})
	if err == nil || !strings.Contains(err.Error(), "auth") {
		t.Fatalf("err=%v", err)
	}
}

func TestHTTPProviderTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	defer srv.Close()
	p, err := websearch.NewHTTP(websearch.HTTPConfig{
		BaseURL: srv.URL,
		Timeout: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Search(context.Background(), "q", websearch.SearchOpts{})
	if err == nil {
		t.Fatal("expected timeout")
	}
}
