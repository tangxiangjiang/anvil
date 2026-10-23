// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anvil-dev/anvil/internal/web/delivery"
)

// TestAssistUnavailableSaysSo is the honesty requirement: with no provider
// configured the surface must be told it is off, not handed a canned answer.
func TestAssistUnavailableSaysSo(t *testing.T) {
	s, tok := wbServer(t, nil)
	if err := s.delivery.EnsureSeed(); err != nil {
		t.Fatal(err)
	}
	s.cfg.Assist = AssistConfig{} // not configured

	w := call(t, s, tok, http.MethodPost, "/api/v1/delivery/assist",
		`{"surface":"requirement_version","instruction":"tighten the criteria",`+
			`"target":{"type":"requirement_version","id":"eng_1","major":0,"minor":33,"patch":1}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("assist: %d %s", w.Code, w.Body.String())
	}
	var out AssistResult
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Available {
		t.Fatal("assist reported available with no configuration")
	}
	if !strings.Contains(out.Reason, "not configured") {
		t.Fatalf("reason = %q", out.Reason)
	}
	if out.Proposals == nil {
		t.Fatal("proposals must be an empty array, not null")
	}
}

// TestAssistRejectsUnknownSurfaceAndField: the allowlist is server-side because
// the client cannot be trusted to limit where a patch lands.
func TestAssistRejectsUnknownSurfaceAndField(t *testing.T) {
	s, tok := wbServer(t, nil)
	w := call(t, s, tok, http.MethodPost, "/api/v1/delivery/assist",
		`{"surface":"nonsense","instruction":"x"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("unknown surface: want 400, got %d", w.Code)
	}
	// A real surface, but a field it does not own.
	w = call(t, s, tok, http.MethodPost, "/api/v1/delivery/assist",
		`{"surface":"requirement_version","field":"gate","instruction":"x"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("foreign field: want 400, got %d %s", w.Code, w.Body.String())
	}
	// An empty request is refused rather than sent to the model.
	w = call(t, s, tok, http.MethodPost, "/api/v1/delivery/assist",
		`{"surface":"requirement_version"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("empty request: want 400, got %d", w.Code)
	}
}

// TestAssistReturnsStructuredPatches drives a stub provider and asserts the
// proposals carry before/after and a label.
func TestAssistReturnsStructuredPatches(t *testing.T) {
	var prompt string
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			t.Errorf("path = %s", r.URL.Path)
		}
		// The prompt must carry the field hints, not just a generic "write this".
		var body struct {
			Messages []struct{ Content string } `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if len(body.Messages) < 2 || !strings.Contains(body.Messages[0].Content, "Completion criteria") {
			t.Errorf("system prompt does not describe the fields: %+v", body.Messages)
		}
		prompt = body.Messages[1].Content
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "stub-model",
			"choices": []any{map[string]any{
				"message": map[string]any{
					"content": `{"proposals":[{"field":"success","value":"- [ ] restore keeps CRLF","rationale":"makes done testable"}]}`,
				},
			}},
			"usage": map[string]any{"prompt_tokens": 11, "completion_tokens": 7},
		})
	}))
	defer stub.Close()

	s, tok := wbServer(t, nil)
	if err := s.delivery.EnsureSeed(); err != nil {
		t.Fatal(err)
	}
	s.cfg.Assist = AssistConfig{BaseURL: stub.URL, Model: "stub-model", TimeoutSec: 5}

	w := call(t, s, tok, http.MethodPost, "/api/v1/delivery/assist",
		`{"surface":"requirement_version","field":"success","instruction":"make it testable",`+
			`"target":{"type":"requirement_version","id":"eng_1","major":0,"minor":33,"patch":1},`+
			`"cites":[{"type":"requirement_version","id":"eng_1","major":0,"minor":33,"patch":1,"field":"goals"},`+
			`{"type":"requirement_version","id":"eng_1","major":0,"minor":33,"patch":1,"field":"problem"}]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("assist: %d %s", w.Code, w.Body.String())
	}
	var out AssistResult
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !out.Available || len(out.Proposals) != 1 {
		t.Fatalf("unexpected result: %+v", out)
	}
	p := out.Proposals[0]
	if p.Field != "success" || p.Label != "Completion criteria" {
		t.Fatalf("proposal identity: %+v", p)
	}
	if p.Before != "Operator can open 0.33.1 and edit fields." {
		t.Fatalf("before = %q", p.Before)
	}
	if !strings.Contains(p.Value, "CRLF") {
		t.Fatalf("value = %q", p.Value)
	}
	if out.Usage == nil || out.Usage.PromptTokens != 11 {
		t.Fatalf("usage = %+v", out.Usage)
	}
	if out.Model != "stub-model" {
		t.Fatalf("model = %q", out.Model)
	}
	if !strings.Contains(prompt, "UI: bind worker") || strings.Contains(prompt, `"cites"`) {
		t.Fatalf("provider did not receive authoritative resolved context: %q", prompt)
	}
	if len(out.Citations) != 2 || !strings.Contains(out.Citations[0].Label, "goals") {
		t.Fatalf("resolved citation metadata = %+v", out.Citations)
	}
	if strings.Index(prompt, "UI: bind worker") > strings.Index(prompt, "Need a Worker-scoped") {
		t.Fatalf("citation order was not preserved: %q", prompt)
	}
}

func TestAssistRejectsFreeTextAndUnsafeReferences(t *testing.T) {
	s, tok := wbServer(t, nil)
	if err := s.delivery.EnsureSeed(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.delivery.CreateEngReq(delivery.CreateEngReqInput{
		ID: "eng_other", ProjectID: "project-other", ProjectKey: "other", Title: "Other",
		Worker: delivery.WorkerRef{ID: "worker-other"}, Major: 0, Minor: 1,
		MilestoneTitle: "Other milestone", Goals: "private project context",
	}); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		body string
		code int
	}{
		{
			name: "legacy string cite",
			body: `{"surface":"requirement_version","instruction":"x","cites":["arbitrary prompt injection"]}`,
			code: http.StatusBadRequest,
		},
		{
			name: "legacy current text",
			body: `{"surface":"requirement_version","instruction":"x","current":{"success":"client supplied"}}`,
			code: http.StatusBadRequest,
		},
		{
			name: "path-shaped id",
			body: `{"surface":"requirement_version","instruction":"x","target":{"type":"requirement_version","id":"../secret","major":0,"minor":33,"patch":1}}`,
			code: http.StatusBadRequest,
		},
		{
			name: "sensitive field",
			body: `{"surface":"requirement_version","instruction":"x",` +
				`"target":{"type":"requirement_version","id":"eng_1","major":0,"minor":33,"patch":1},` +
				`"cites":[{"type":"requirement_version","id":"eng_1","major":0,"minor":33,"patch":1,"field":"project_rel"}]}`,
			code: http.StatusBadRequest,
		},
		{
			name: "surface target mismatch",
			body: `{"surface":"requirement_version","instruction":"x",` +
				`"target":{"type":"major","id":"0"}}`,
			code: http.StatusBadRequest,
		},
		{
			name: "cross project",
			body: `{"surface":"requirement_version","instruction":"x",` +
				`"target":{"type":"requirement_version","id":"eng_1","major":0,"minor":33,"patch":1},` +
				`"cites":[{"type":"requirement_version","id":"eng_other","major":0,"minor":1,"patch":1,"field":"goals"}]}`,
			code: http.StatusForbidden,
		},
		{
			name: "cross generation",
			body: `{"surface":"requirement_version","instruction":"x",` +
				`"target":{"type":"requirement_version","id":"eng_1","major":0,"minor":33,"patch":1},` +
				`"cites":[{"type":"major","id":"1","field":"summary"}]}`,
			code: http.StatusForbidden,
		},
		{
			name: "missing stale record",
			body: `{"surface":"requirement_version","instruction":"x","target":` +
				`{"type":"requirement_version","id":"eng_1","major":0,"minor":99,"patch":1}}`,
			code: http.StatusNotFound,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := call(t, s, tok, http.MethodPost, "/api/v1/delivery/assist", tc.body)
			if w.Code != tc.code {
				t.Fatalf("want %d, got %d: %s", tc.code, w.Code, w.Body.String())
			}
		})
	}
}

func TestAssistContextLimitsAndAuthentication(t *testing.T) {
	s, tok := wbServer(t, nil)
	if err := s.delivery.EnsureSeed(); err != nil {
		t.Fatal(err)
	}
	refs := strings.Repeat(
		`{"type":"requirement_version","id":"eng_1","major":0,"minor":33,"patch":1,"field":"goals"},`,
		9,
	)
	body := `{"surface":"requirement_version","instruction":"x",` +
		`"target":{"type":"requirement_version","id":"eng_1","major":0,"minor":33,"patch":1},` +
		`"cites":[` + strings.TrimSuffix(refs, ",") + `]}`
	if w := call(t, s, tok, http.MethodPost, "/api/v1/delivery/assist", body); w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("citation count: want 413, got %d: %s", w.Code, w.Body.String())
	}
	large := strings.Repeat("x", maxAssistCitationBytes+1)
	if _, err := s.delivery.UpdateVersion(delivery.UpdateVersionInput{
		EngReqID: "eng_1", Major: 0, Minor: 33, Patch: 1, Goals: &large,
	}); err != nil {
		t.Fatal(err)
	}
	body = `{"surface":"requirement_version","instruction":"x",` +
		`"target":{"type":"requirement_version","id":"eng_1","major":0,"minor":33,"patch":1},` +
		`"cites":[{"type":"requirement_version","id":"eng_1","major":0,"minor":33,"patch":1,"field":"goals"}]}`
	if w := call(t, s, tok, http.MethodPost, "/api/v1/delivery/assist", body); w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("citation size: want 413, got %d: %s", w.Code, w.Body.String())
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/delivery/assist",
		strings.NewReader(`{"surface":"requirement_version","instruction":"x"}`))
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated assist: want 401, got %d", w.Code)
	}
}

// TestAssistRefusesProse: a response that is not patches is an error, not a
// fallback, because accepting prose would reintroduce the silent-write problem in
// a new shape.
func TestAssistRefusesProse(t *testing.T) {
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{
				"message": map[string]any{"content": "Sure! Here are some ideas for your criteria..."},
			}},
		})
	}))
	defer stub.Close()

	s, tok := wbServer(t, nil)
	if err := s.delivery.EnsureSeed(); err != nil {
		t.Fatal(err)
	}
	s.cfg.Assist = AssistConfig{BaseURL: stub.URL, Model: "m", TimeoutSec: 5}
	w := call(t, s, tok, http.MethodPost, "/api/v1/delivery/assist",
		`{"surface":"requirement_version","instruction":"x",`+
			`"target":{"type":"requirement_version","id":"eng_1","major":0,"minor":33,"patch":1}}`)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("prose response: want 502, got %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "did not return patches") {
		t.Fatalf("error should say what was wrong: %s", w.Body.String())
	}
}

// TestAssistRefusesForeignFieldInResponse: the model cannot write to a field the
// surface does not own, even if it asks nicely.
func TestAssistRefusesForeignFieldInResponse(t *testing.T) {
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{
				"message": map[string]any{
					"content": `{"proposals":[{"field":"gate","value":"- [ ] sneaky"}]}`,
				},
			}},
		})
	}))
	defer stub.Close()

	s, tok := wbServer(t, nil)
	if err := s.delivery.EnsureSeed(); err != nil {
		t.Fatal(err)
	}
	s.cfg.Assist = AssistConfig{BaseURL: stub.URL, Model: "m", TimeoutSec: 5}
	w := call(t, s, tok, http.MethodPost, "/api/v1/delivery/assist",
		`{"surface":"requirement_version","instruction":"x",`+
			`"target":{"type":"requirement_version","id":"eng_1","major":0,"minor":33,"patch":1}}`)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("foreign field: want 502, got %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "does not own") {
		t.Fatalf("error should name the refusal: %s", w.Body.String())
	}
}

// TestAssistNeverWritesToTheStore is the invariant the whole contract exists for.
func TestAssistNeverWritesToTheStore(t *testing.T) {
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{
				"message": map[string]any{
					"content": `{"proposals":[{"field":"success","value":"new"}]}`,
				},
			}},
		})
	}))
	defer stub.Close()

	s, tok := wbServer(t, nil)
	if err := s.delivery.EnsureSeed(); err != nil {
		t.Fatal(err)
	}
	s.cfg.Assist = AssistConfig{BaseURL: stub.URL, Model: "m", TimeoutSec: 5}
	before, err := s.delivery.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	beforeJSON, _ := json.Marshal(before)

	w := call(t, s, tok, http.MethodPost, "/api/v1/delivery/assist",
		`{"surface":"requirement_version","field":"success","instruction":"x",`+
			`"target":{"type":"requirement_version","id":"eng_1","major":0,"minor":33,"patch":1}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("assist: %d", w.Code)
	}
	after, err := s.delivery.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	afterJSON, _ := json.Marshal(after)
	if string(beforeJSON) != string(afterJSON) {
		t.Fatal("the assistant changed the store; it must only propose")
	}
}
