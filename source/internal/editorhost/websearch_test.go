// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package editorhost

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/anvil-dev/anvil"
)

// The acceptance's first clause: turning the setting on is what puts the web
// tools on the session's options, and the key is named by the host, not sent by
// the client.
func TestBuildOptionsCarriesTheWebSearchConfiguration(t *testing.T) {
	dir := newWorkspace(t)
	srv := New(Config{CWD: dir, Model: "stub-model"}, strings.NewReader(""), &bytes.Buffer{})
	sess := newSession("sess-test", srv.cwdPath(), ModeAsk, false)

	opts, rpcErr := srv.buildOptions(SessionParams{WebSearch: &WebSearchParams{
		Provider:   "http",
		BaseURL:    "https://search.example.test/v1",
		MaxResults: 3,
	}}, sess)
	if rpcErr != nil {
		t.Fatalf("buildOptions: %s", rpcErr.Message)
	}
	ws := opts.WebSearch
	if !ws.Enabled {
		t.Fatal("web search is off")
	}
	if ws.Provider != "http" || ws.BaseURL != "https://search.example.test/v1" {
		t.Fatalf("web search=%+v want the http endpoint", ws)
	}
	if ws.APIKeyEnv != webSearchKeyEnv {
		t.Fatalf("APIKeyEnv=%q want %q", ws.APIKeyEnv, webSearchKeyEnv)
	}
	if ws.APIKey != "" {
		t.Fatal("the key must not travel on the wire")
	}
	if ws.MaxResults != 3 {
		t.Fatalf("MaxResults=%d want 3", ws.MaxResults)
	}
	if !ws.AllowFetchURL {
		t.Fatal("fetch_url is part of the web tools")
	}
	if ws.AllowPrivateURLs {
		t.Fatal("the client setting must not relax the fetch policy")
	}
}

// Off is absent, not disabled: no endpoint to read, no key to look for, and no
// tools registered.
func TestBuildOptionsLeavesWebSearchOffUnlessAsked(t *testing.T) {
	dir := newWorkspace(t)
	srv := New(Config{CWD: dir, Model: "stub-model"}, strings.NewReader(""), &bytes.Buffer{})
	sess := newSession("sess-test", srv.cwdPath(), ModeAsk, false)

	opts, rpcErr := srv.buildOptions(SessionParams{}, sess)
	if rpcErr != nil {
		t.Fatalf("buildOptions: %s", rpcErr.Message)
	}
	if opts.WebSearch != (anvil.WebSearchOptions{}) {
		t.Fatalf("web search=%+v want the zero value", opts.WebSearch)
	}
}

// An http provider with no endpoint would register a tool that fails on every
// call. Refusing the session names the setting that is missing.
func TestBuildOptionsRefusesWebSearchWithoutAnEndpoint(t *testing.T) {
	dir := newWorkspace(t)
	srv := New(Config{CWD: dir, Model: "stub-model"}, strings.NewReader(""), &bytes.Buffer{})
	sess := newSession("sess-test", srv.cwdPath(), ModeAsk, false)

	_, rpcErr := srv.buildOptions(SessionParams{WebSearch: &WebSearchParams{Provider: "http"}}, sess)
	if rpcErr == nil {
		t.Fatal("an endpoint-less http provider was accepted")
	}
	if rpcErr.Code != CodeInvalidParams || !strings.Contains(rpcErr.Message, "web_search.base_url") {
		t.Fatalf("error=%+v want invalid params naming web_search.base_url", rpcErr)
	}
}

func TestBuildOptionsRefusesAnUnknownWebSearchProvider(t *testing.T) {
	dir := newWorkspace(t)
	srv := New(Config{CWD: dir, Model: "stub-model"}, strings.NewReader(""), &bytes.Buffer{})
	sess := newSession("sess-test", srv.cwdPath(), ModeAsk, false)

	_, rpcErr := srv.buildOptions(SessionParams{WebSearch: &WebSearchParams{Provider: "galaxy"}}, sess)
	if rpcErr == nil || rpcErr.Code != CodeInvalidParams {
		t.Fatalf("error=%v want invalid params", rpcErr)
	}
}

// The model only calls what the session advertises, so the tool list in the
// request is the reachability check that matters. A run is used rather than the
// options because `web_search` is registered during Create.
func TestSessionWithWebSearchAdvertisesTheWebTools(t *testing.T) {
	llm := newStubLLM(t, textTurn("ok"))
	h, _ := initHost(t, llm.baseURL())

	sess := resultOf(t, h.callOK(MethodSessionNew, SessionParams{WebSearch: &WebSearchParams{
		Provider: "http",
		BaseURL:  "https://search.example.test/v1",
	}}))
	h.callOK(MethodPrompt, PromptParams{SessionID: sess["session_id"].(string), Message: "hello"})
	h.collectRunOK(15 * time.Second)

	body := llm.lastRequestBody(t)
	for _, name := range []string{"web_search", "fetch_url"} {
		if !strings.Contains(body, `"`+name+`"`) {
			t.Fatalf("the session did not advertise %s: %s", name, body)
		}
	}
}

func TestSessionWithoutWebSearchAdvertisesNoWebTools(t *testing.T) {
	llm := newStubLLM(t, textTurn("ok"))
	h, _ := initHost(t, llm.baseURL())

	sess := resultOf(t, h.callOK(MethodSessionNew, SessionParams{}))
	h.callOK(MethodPrompt, PromptParams{SessionID: sess["session_id"].(string), Message: "hello"})
	h.collectRunOK(15 * time.Second)

	body := llm.lastRequestBody(t)
	for _, name := range []string{"web_search", "fetch_url"} {
		if strings.Contains(body, `"`+name+`"`) {
			t.Fatalf("a session with web search off advertised %s: %s", name, body)
		}
	}
}

// The wire type is the contract: no key field may be added to it. The key lives
// in the child environment, and this fails the day someone puts it on the wire.
func TestWebSearchParamsHasNoKeyField(t *testing.T) {
	raw, err := json.Marshal(SessionParams{WebSearch: &WebSearchParams{
		Provider:   "http",
		BaseURL:    "https://search.example.test/v1",
		MaxResults: 5,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "key") {
		t.Fatalf("the web search params carry a key field: %s", raw)
	}
}
