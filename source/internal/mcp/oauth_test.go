// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package mcp_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anvil-dev/anvil/internal/mcp"
)

// oauthFixture is one authorization server and one MCP server that demands a
// bearer. The AS validates exactly what the design says it must: the PKCE
// verifier against the challenge, the exact redirect URI, the client id and the
// RFC 8707 resource parameter.
type oauthFixture struct {
	t  *testing.T
	as *httptest.Server
	// mcpURL is set after both servers exist.
	mcpURL string

	mu            sync.Mutex
	clientID      string
	redirectURI   string
	challenge     string
	resource      string
	registered    bool
	access        string
	refresh       string
	issued        map[string]bool
	refreshes     int
	tokenCalls    int
	rejectRefresh bool
}

func newOAuthFixture(t *testing.T) *oauthFixture {
	t.Helper()
	f := &oauthFixture{t: t, clientID: "client-1", access: "access-1", refresh: "refresh-1", issued: map[string]bool{}}
	f.as = httptest.NewServer(http.HandlerFunc(f.serveAS))
	t.Cleanup(f.as.Close)

	// The resource's well-known location, both at the root and with the
	// resource's own path appended, as RFC 9728 derives it.
	wellKnown := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"resource":              f.mcpURL,
			"authorization_servers": []string{f.as.URL},
		})
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/oauth-protected-resource", wellKnown)
	mux.HandleFunc("/.well-known/oauth-protected-resource/mcp", wellKnown)
	mux.HandleFunc("/mcp", f.serveMCP)
	mcpSrv := httptest.NewServer(mux)
	t.Cleanup(mcpSrv.Close)
	f.mcpURL = mcpSrv.URL + "/mcp"
	return f
}

func (f *oauthFixture) bearerOK(header string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return header == "Bearer "+f.access
}

func (f *oauthFixture) serveMCP(w http.ResponseWriter, r *http.Request) {
	if !f.bearerOK(r.Header.Get("Authorization")) {
		w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+f.as.URL+`/.well-known/oauth-protected-resource"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var req rpcIn
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	switch req.Method {
	case "notifications/initialized":
		w.WriteHeader(http.StatusAccepted)
		return
	case "initialize":
		w.Header().Set("Mcp-Session-Id", "s1")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0", "id": req.ID,
			"result": map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}},
		})
	case "tools/list":
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0", "id": req.ID,
			"result": map[string]any{"tools": []map[string]any{{"name": "echo", "description": "echo", "inputSchema": map[string]any{"type": "object"}}}},
		})
	case "tools/call":
		var p struct {
			Arguments map[string]any `json:"arguments"`
		}
		_ = json.Unmarshal(req.Params, &p)
		text, _ := p.Arguments["text"].(string)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0", "id": req.ID,
			"result": map[string]any{"content": []map[string]string{{"type": "text", "text": "echo:" + text}}},
		})
	default:
		http.Error(w, "unknown method", 400)
	}
}

func (f *oauthFixture) serveAS(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/.well-known/oauth-protected-resource":
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"resource":              f.mcpURL,
			"authorization_servers": []string{f.as.URL},
		})
	case "/.well-known/oauth-authorization-server":
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                           f.as.URL,
			"authorization_endpoint":           f.as.URL + "/authorize",
			"token_endpoint":                   f.as.URL + "/token",
			"registration_endpoint":            f.as.URL + "/register",
			"code_challenge_methods_supported": []string{"S256"},
		})
	case "/register":
		var body struct {
			RedirectURIs []string `json:"redirect_uris"`
			GrantTypes   []string `json:"grant_types"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.RedirectURIs) != 1 {
			http.Error(w, "bad registration", 400)
			return
		}
		f.mu.Lock()
		f.redirectURI = body.RedirectURIs[0]
		f.registered = true
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"client_id": f.clientID, "token_endpoint_auth_method": "none"})
	case "/token":
		f.serveToken(w, r)
	case "/authorize":
		// The browser's view of the authorization endpoint is the redirect the
		// test issues itself; nothing else lands here.
		http.Error(w, "the test drives the callback directly", 400)
	default:
		http.NotFound(w, r)
	}
}

func (f *oauthFixture) serveToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", 400)
		return
	}
	f.mu.Lock()
	f.tokenCalls++
	reject := f.rejectRefresh
	expectedRefresh := f.refresh
	expectedRedirect := f.redirectURI
	f.mu.Unlock()

	writeJSON := func(status int, body map[string]any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	}

	switch r.Form.Get("grant_type") {
	case "authorization_code":
		if code := r.Form.Get("code"); !f.consumeCode(code) {
			writeJSON(400, map[string]any{"error": "invalid_grant", "error_description": "unknown code"})
			return
		}
		if got := r.Form.Get("redirect_uri"); got != expectedRedirect {
			writeJSON(400, map[string]any{"error": "invalid_grant", "error_description": "redirect_uri mismatch"})
			return
		}
		if r.Form.Get("client_id") != f.clientID {
			writeJSON(400, map[string]any{"error": "invalid_client"})
			return
		}
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if base64.RawURLEncoding.EncodeToString(sum[:]) != f.challengeValue() {
			writeJSON(400, map[string]any{"error": "invalid_grant", "error_description": "pkce mismatch"})
			return
		}
		if r.Form.Get("resource") != f.mcpURL {
			writeJSON(400, map[string]any{"error": "invalid_target", "error_description": "resource mismatch"})
			return
		}
		writeJSON(200, map[string]any{
			"access_token": "access-1", "refresh_token": "refresh-1",
			"token_type": "Bearer", "expires_in": 3600, "scope": "read",
		})
	case "refresh_token":
		if reject || r.Form.Get("refresh_token") != expectedRefresh {
			writeJSON(400, map[string]any{"error": "invalid_grant", "error_description": "refresh token rejected"})
			return
		}
		f.mu.Lock()
		f.access = "access-2"
		f.refresh = "refresh-2"
		f.refreshes++
		f.mu.Unlock()
		writeJSON(200, map[string]any{
			"access_token": "access-2", "refresh_token": "refresh-2",
			"token_type": "Bearer", "expires_in": 3600,
		})
	default:
		writeJSON(400, map[string]any{"error": "unsupported_grant_type"})
	}
}

func (f *oauthFixture) consumeCode(code string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if code != "code-1" || f.issued[code] {
		return false
	}
	f.issued[code] = true
	return true
}

func (f *oauthFixture) setChallenge(challenge string) {
	f.mu.Lock()
	f.challenge = challenge
	f.mu.Unlock()
}

func (f *oauthFixture) challengeValue() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.challenge
}

func (f *oauthFixture) isRegistered() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.registered
}

func (f *oauthFixture) setRejectRefresh(v bool) {
	f.mu.Lock()
	f.rejectRefresh = v
	f.mu.Unlock()
}

func (f *oauthFixture) counters() (refreshes, tokenCalls int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.refreshes, f.tokenCalls
}

// 401 first, then discovery, registration, PKCE and the callback: the whole
// flow through the public OAuth API.
func TestOAuthAuthorizationCodeFlow(t *testing.T) {
	f := newOAuthFixture(t)
	ctx := context.Background()
	dir := t.TempDir()
	auth := mcp.NewOAuth(mcp.OAuthConfig{Server: "docs", ServerURL: f.mcpURL, TokenDir: dir})

	// The first connect has no token and meets the 401.
	_, err := mcp.Start(ctx, mcp.ServerConfig{Name: "docs", Transport: "http", URL: f.mcpURL, AuthToken: auth.Token})
	var required *mcp.AuthRequiredError
	if !errors.As(err, &required) {
		t.Fatalf("start without a token: %v want *AuthRequiredError", err)
	}
	if required.ResourceMetadataURL == "" {
		t.Fatal("the 401 carried no resource metadata URL")
	}

	authorization, err := auth.Begin(ctx, required.Challenge)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	u, err := url.Parse(authorization.URL)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if u.Path != "/authorize" {
		t.Fatalf("authorization endpoint=%q", u.Path)
	}
	if q.Get("response_type") != "code" || q.Get("client_id") != "client-1" {
		t.Fatalf("authorization params=%v", q)
	}
	if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" {
		t.Fatalf("no PKCE S256 challenge: %v", q)
	}
	if q.Get("state") == "" {
		t.Fatal("no state parameter")
	}
	if q.Get("resource") != f.mcpURL {
		t.Fatalf("resource=%q want %q", q.Get("resource"), f.mcpURL)
	}
	if !strings.HasPrefix(q.Get("redirect_uri"), "http://127.0.0.1:") {
		t.Fatalf("redirect_uri=%q want a loopback address", q.Get("redirect_uri"))
	}
	f.setChallenge(q.Get("code_challenge"))

	if !f.isRegistered() {
		t.Fatal("no dynamic registration happened")
	}

	// The person approves in the browser; the AS sends them back with a code.
	callback := q.Get("redirect_uri") + "?code=code-1&state=" + url.QueryEscape(q.Get("state"))
	resp, err := http.Get(callback)
	if err != nil {
		t.Fatalf("callback: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("callback status=%d", resp.StatusCode)
	}
	if err := auth.Wait(ctx); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	token, err := auth.Token(ctx)
	if err != nil || token != "access-1" {
		t.Fatalf("Token=%q err=%v", token, err)
	}

	// The token is what a real session uses.
	sess, err := mcp.Start(ctx, mcp.ServerConfig{Name: "docs", Transport: "http", URL: f.mcpURL, AuthToken: auth.Token})
	if err != nil {
		t.Fatalf("Start with the token: %v", err)
	}
	defer sess.Close()
	out, err := sess.Call(ctx, "echo", json.RawMessage(`{"text":"signed-in"}`))
	if err != nil || out != "echo:signed-in" {
		t.Fatalf("call=%q err=%v", out, err)
	}

	// One file, private where the OS supports the mode.
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil || len(files) != 1 {
		t.Fatalf("token files=%v err=%v", files, err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(files[0])
		if err != nil {
			t.Fatal(err)
		}
		if mode := info.Mode().Perm(); mode != 0o600 {
			t.Fatalf("token file mode=%o want 600", mode)
		}
	}
}

// An expiring token is refreshed once, persisted, and used without a
// reconnect; a rejected refresh goes back to sign-in instead of looping.
func TestOAuthRefreshAndInvalidGrant(t *testing.T) {
	f := newOAuthFixture(t)
	ctx := context.Background()
	dir := t.TempDir()
	auth := mcp.NewOAuth(mcp.OAuthConfig{Server: "docs", ServerURL: f.mcpURL, TokenDir: dir})
	f.authorize(t, ctx, auth)

	expireTokenFile(t, dir)

	token, err := auth.Token(ctx)
	if err != nil || token != "access-2" {
		t.Fatalf("Token after expiry=%q err=%v", token, err)
	}
	if refreshes, _ := f.counters(); refreshes != 1 {
		t.Fatalf("refreshes=%d want one", refreshes)
	}
	rec := readTokenFile(t, dir)
	if rec.AccessToken != "access-2" || rec.RefreshToken != "refresh-2" {
		t.Fatalf("persisted record=%+v want the refreshed tokens", rec)
	}

	// A second call with a fresh expiry does not refresh again.
	if token, err := auth.Token(ctx); err != nil || token != "access-2" {
		t.Fatalf("Token=%q err=%v", token, err)
	}
	if refreshes, _ := f.counters(); refreshes != 1 {
		t.Fatalf("refreshes=%d want still one", refreshes)
	}

	expireTokenFile(t, dir)
	f.setRejectRefresh(true)
	_, before := f.counters()
	_, err = auth.Token(ctx)
	var required *mcp.AuthRequiredError
	if !errors.As(err, &required) {
		t.Fatalf("rejected refresh: %v want *AuthRequiredError", err)
	}
	_, after := f.counters()
	if after != before+1 {
		t.Fatalf("token calls=%d want one attempt, no retry loop", after-before)
	}
	rec = readTokenFile(t, dir)
	if rec.AccessToken != "" || rec.RefreshToken != "" {
		t.Fatalf("record=%+v want the tokens cleared", rec)
	}
	for _, secret := range []string{"access-1", "access-2", "refresh-1", "refresh-2"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("error %q contains a token", err)
		}
	}
}

// SignOut deletes the file, and a second Begin while one is pending returns
// the same URL rather than opening a second listener.
func TestOAuthSignOutAndRepeatedBegin(t *testing.T) {
	f := newOAuthFixture(t)
	ctx := context.Background()
	dir := t.TempDir()
	auth := mcp.NewOAuth(mcp.OAuthConfig{Server: "docs", ServerURL: f.mcpURL, TokenDir: dir})
	f.authorize(t, ctx, auth)
	if !auth.HasToken() {
		t.Fatal("HasToken is false after a completed flow")
	}

	first, err := auth.Begin(ctx, "")
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	second, err := auth.Begin(ctx, "")
	if err != nil {
		t.Fatalf("second Begin: %v", err)
	}
	if first.URL != second.URL {
		t.Fatalf("a second Begin started another flow")
	}
	auth.Cancel()

	if err := auth.SignOut(); err != nil {
		t.Fatalf("SignOut: %v", err)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	if len(files) != 0 {
		t.Fatalf("token files after SignOut=%v", files)
	}
	if auth.HasToken() {
		t.Fatal("HasToken is true after SignOut")
	}
}

// The authorization server's error parameters are reported without echoing
// anything secret back.
func TestOAuthCallbackErrorIsReported(t *testing.T) {
	f := newOAuthFixture(t)
	ctx := context.Background()
	auth := mcp.NewOAuth(mcp.OAuthConfig{Server: "docs", ServerURL: f.mcpURL, TokenDir: t.TempDir()})

	authorization, err := auth.Begin(ctx, `Bearer resource_metadata="`+f.as.URL+`/.well-known/oauth-protected-resource"`)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	u, _ := url.Parse(authorization.URL)
	callback := u.Query().Get("redirect_uri") + "?error=access_denied&state=" + url.QueryEscape(u.Query().Get("state"))
	resp, err := http.Get(callback)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	err = auth.Wait(ctx)
	if err == nil || !strings.Contains(err.Error(), "access_denied") {
		t.Fatalf("Wait=%v want the authorization server's error", err)
	}
}

func (f *oauthFixture) authorize(t *testing.T, ctx context.Context, auth *mcp.OAuth) {
	t.Helper()
	_, err := mcp.Start(ctx, mcp.ServerConfig{Name: "docs", Transport: "http", URL: f.mcpURL, AuthToken: auth.Token})
	var required *mcp.AuthRequiredError
	if !errors.As(err, &required) {
		t.Fatalf("expected a 401 first: %v", err)
	}
	authorization, err := auth.Begin(ctx, required.Challenge)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	u, _ := url.Parse(authorization.URL)
	f.setChallenge(u.Query().Get("code_challenge"))
	callback := u.Query().Get("redirect_uri") + "?code=code-1&state=" + url.QueryEscape(u.Query().Get("state"))
	resp, err := http.Get(callback)
	if err != nil {
		t.Fatalf("callback: %v", err)
	}
	_ = resp.Body.Close()
	if err := auth.Wait(ctx); err != nil {
		t.Fatalf("Wait: %v", err)
	}
}

func expireTokenFile(t *testing.T, dir string) {
	t.Helper()
	rec := readTokenFile(t, dir)
	rec.ExpiresAt = time.Now().Add(10 * time.Second)
	writeTokenFile(t, dir, rec)
}

func readTokenFile(t *testing.T, dir string) *mcp.TokenRecord {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil || len(files) != 1 {
		t.Fatalf("token files=%v err=%v", files, err)
	}
	body, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	var rec mcp.TokenRecord
	if err := json.Unmarshal(body, &rec); err != nil {
		t.Fatal(err)
	}
	return &rec
}

func writeTokenFile(t *testing.T, dir string, rec *mcp.TokenRecord) {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil || len(files) != 1 {
		t.Fatalf("token files=%v err=%v", files, err)
	}
	body, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(files[0], body, 0o600); err != nil {
		t.Fatal(err)
	}
}
