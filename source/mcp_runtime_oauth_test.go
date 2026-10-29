// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
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
)

// runtimeOAuthFixture is a remote MCP server that demands a bearer plus the
// authorization server that issues it. It stands in for the whole discovery
// chain the runtime follows after a 401.
type runtimeOAuthFixture struct {
	t      *testing.T
	as     *httptest.Server
	mcpURL string

	mu          sync.Mutex
	challenge   string
	redirectURI string
	registered  bool
}

func newRuntimeOAuthFixture(t *testing.T) *runtimeOAuthFixture {
	t.Helper()
	f := &runtimeOAuthFixture{t: t}
	f.as = httptest.NewServer(http.HandlerFunc(f.serveAS))
	t.Cleanup(f.as.Close)

	mux := http.NewServeMux()
	wellKnown := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"resource":              f.mcpURL,
			"authorization_servers": []string{f.as.URL},
		})
	}
	mux.HandleFunc("/.well-known/oauth-protected-resource", wellKnown)
	mux.HandleFunc("/mcp", f.serveMCP)
	mcpSrv := httptest.NewServer(mux)
	t.Cleanup(mcpSrv.Close)
	f.mcpURL = mcpSrv.URL + "/mcp"
	return f
}

func (f *runtimeOAuthFixture) serveMCP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer token-1" {
		w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+f.as.URL+`/.well-known/oauth-protected-resource"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var req struct {
		ID     int64           `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	switch req.Method {
	case "notifications/initialized":
		w.WriteHeader(http.StatusAccepted)
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
			"result": map[string]any{"tools": []map[string]any{{
				"name": "echo", "description": "echo", "inputSchema": map[string]any{"type": "object"},
			}}},
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

func (f *runtimeOAuthFixture) serveAS(w http.ResponseWriter, r *http.Request) {
	writeJSON := func(status int, body map[string]any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	}
	switch r.URL.Path {
	case "/.well-known/oauth-protected-resource":
		writeJSON(200, map[string]any{"resource": f.mcpURL, "authorization_servers": []string{f.as.URL}})
	case "/.well-known/oauth-authorization-server":
		writeJSON(200, map[string]any{
			"issuer":                           f.as.URL,
			"authorization_endpoint":           f.as.URL + "/authorize",
			"token_endpoint":                   f.as.URL + "/token",
			"registration_endpoint":            f.as.URL + "/register",
			"code_challenge_methods_supported": []string{"S256"},
		})
	case "/register":
		var body struct {
			RedirectURIs []string `json:"redirect_uris"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.RedirectURIs) != 1 {
			http.Error(w, "bad registration", 400)
			return
		}
		f.mu.Lock()
		f.redirectURI = body.RedirectURIs[0]
		f.registered = true
		f.mu.Unlock()
		writeJSON(200, map[string]any{"client_id": "client-1", "token_endpoint_auth_method": "none"})
	case "/token":
		_ = r.ParseForm()
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		f.mu.Lock()
		challenge, redirectURI := f.challenge, f.redirectURI
		f.mu.Unlock()
		switch {
		case r.Form.Get("grant_type") != "authorization_code",
			r.Form.Get("code") != "code-1",
			r.Form.Get("redirect_uri") != redirectURI,
			r.Form.Get("resource") != f.mcpURL,
			base64.RawURLEncoding.EncodeToString(sum[:]) != challenge:
			writeJSON(400, map[string]any{"error": "invalid_grant"})
			return
		}
		writeJSON(200, map[string]any{
			"access_token": "token-1", "refresh_token": "refresh-1", "token_type": "Bearer", "expires_in": 3600,
		})
	default:
		http.NotFound(w, r)
	}
}

func (f *runtimeOAuthFixture) setChallenge(challenge string) {
	f.mu.Lock()
	f.challenge = challenge
	f.mu.Unlock()
}

// A remote server that answers 401 is a status, not a failed session; sign-in
// turns it into a connected server whose tools are callable, and signing out
// puts it back.
func TestMCPRuntimeOAuthSignInAndOut(t *testing.T) {
	f := newRuntimeOAuthFixture(t)
	ctx := context.Background()
	tokenDir := t.TempDir()
	rt := NewMCPRuntime(MCPRuntimeOptions{
		CWD:            t.TempDir(),
		Inline:         map[string]MCPServerConfig{"docs": {Transport: "http", URL: f.mcpURL}},
		TokenDir:       tokenDir,
		ConnectTimeout: 5 * time.Second,
	})
	defer rt.Close()

	if err := rt.Reload(ctx); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	status, ok := rt.Status("docs")
	if !ok || status.State != MCPStateAuthRequired {
		t.Fatalf("status=%+v want auth_required", status)
	}
	if strings.Contains(status.Error, "token") {
		t.Fatalf("status error mentions a token: %q", status.Error)
	}

	first, err := rt.Authenticate(ctx, "docs")
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	second, err := rt.Authenticate(ctx, "docs")
	if err != nil {
		t.Fatalf("second Authenticate: %v", err)
	}
	if first.URL != second.URL {
		t.Fatal("a second Authenticate started a second flow")
	}

	u, err := url.Parse(first.URL)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("resource") != f.mcpURL || q.Get("code_challenge_method") != "S256" {
		t.Fatalf("authorization parameters=%v", q)
	}
	f.setChallenge(q.Get("code_challenge"))

	resp, err := http.Get(q.Get("redirect_uri") + "?code=code-1&state=" + url.QueryEscape(q.Get("state")))
	if err != nil {
		t.Fatalf("callback: %v", err)
	}
	_ = resp.Body.Close()

	waitForRuntimeState(t, rt, "docs", MCPStateConnected)
	status, _ = rt.Status("docs")
	if status.ToolCount != 1 || len(status.Tools) != 1 || status.Tools[0].Name != "echo" {
		t.Fatalf("status after sign-in=%+v", status)
	}
	out, err := rt.Call(ctx, "docs", "echo", json.RawMessage(`{"text":"in"}`))
	if err != nil || out != "echo:in" {
		t.Fatalf("Call=%q err=%v", out, err)
	}

	files, err := filepath.Glob(filepath.Join(tokenDir, "*.json"))
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

	if err := rt.SignOut("docs"); err != nil {
		t.Fatalf("SignOut: %v", err)
	}
	status, _ = rt.Status("docs")
	if status.State != MCPStateAuthRequired {
		t.Fatalf("status after SignOut=%+v want auth_required", status)
	}
	if _, err := os.Stat(files[0]); !os.IsNotExist(err) {
		t.Fatalf("token file survived SignOut: %v", err)
	}
	if tools := rt.Tools(); len(tools) != 0 {
		t.Fatalf("tools after SignOut=%+v", tools)
	}
}

func waitForRuntimeState(t *testing.T, rt *MCPRuntime, name string, want MCPState) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if status, ok := rt.Status(name); ok && status.State == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	status, _ := rt.Status(name)
	t.Fatalf("%s never reached %s: %+v", name, want, status)
}
