// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package llm_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/anvil-dev/anvil/internal/llm"
)

type wireCase struct {
	name      string
	anthropic bool
	call      func(srv *httptest.Server, req llm.Request) error
}

func wireCases() []wireCase {
	openai := func(stream bool) func(*httptest.Server, llm.Request) error {
		return func(srv *httptest.Server, req llm.Request) error {
			c := llm.NewOpenAI(llm.Config{APIKey: "k", BaseURL: srv.URL, Model: "m"})
			if stream {
				_, err := c.CompleteStream(context.Background(), req, func(llm.Delta) {})
				return err
			}
			_, err := c.Complete(context.Background(), req)
			return err
		}
	}
	anthropic := func(stream bool) func(*httptest.Server, llm.Request) error {
		return func(srv *httptest.Server, req llm.Request) error {
			c := llm.NewAnthropic(llm.Config{APIKey: "k", BaseURL: srv.URL, Model: "m"})
			if stream {
				_, err := c.CompleteStream(context.Background(), req, func(llm.Delta) {})
				return err
			}
			_, err := c.Complete(context.Background(), req)
			return err
		}
	}
	return []wireCase{
		{"openai_plain", false, openai(false)},
		{"openai_stream", false, openai(true)},
		{"anthropic_plain", true, anthropic(false)},
		{"anthropic_stream", true, anthropic(true)},
	}
}

func sentBody(t *testing.T, tc wireCase, req llm.Request) map[string]json.RawMessage {
	t.Helper()
	srv, body := captureServer(t)
	if err := tc.call(srv, req); err != nil {
		t.Fatal(err)
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(body(), &out); err != nil {
		t.Fatalf("request body: %v", err)
	}
	return out
}

// TestRequestEffortField: the field is present if and only if effort is configured,
// with the value each wire spells it with. The numbers are written out here on
// purpose; they are the contract, not whatever the adapter happens to hold.
func TestRequestEffortField(t *testing.T) {
	budgets := map[llm.Effort]int{llm.EffortLow: 1024, llm.EffortMedium: 4096, llm.EffortHigh: 16384}
	maxTokens := map[llm.Effort]int{llm.EffortDefault: 8192, llm.EffortLow: 8192, llm.EffortMedium: 8192, llm.EffortHigh: 24576}
	for _, tc := range wireCases() {
		for _, effort := range []llm.Effort{llm.EffortDefault, llm.EffortLow, llm.EffortMedium, llm.EffortHigh} {
			t.Run(tc.name+"/"+string(effort), func(t *testing.T) {
				req := goldenHistory()
				req.Effort = effort
				body := sentBody(t, tc, req)
				if !tc.anthropic {
					raw, has := body["reasoning_effort"]
					if effort == llm.EffortDefault {
						if has {
							t.Fatalf("reasoning_effort sent with no effort: %s", raw)
						}
					} else if string(raw) != `"`+string(effort)+`"` {
						t.Fatalf("reasoning_effort=%s want %q", raw, effort)
					}
					if _, has := body["thinking"]; has {
						t.Fatal("OpenAI request carries thinking")
					}
					return
				}
				var max int
				if err := json.Unmarshal(body["max_tokens"], &max); err != nil || max != maxTokens[effort] {
					t.Fatalf("max_tokens=%s want %d (%v)", body["max_tokens"], maxTokens[effort], err)
				}
				raw, has := body["thinking"]
				if effort == llm.EffortDefault {
					if has {
						t.Fatalf("thinking sent with no effort: %s", raw)
					}
					return
				}
				var thinking struct {
					Type         string `json:"type"`
					BudgetTokens int    `json:"budget_tokens"`
				}
				if err := json.Unmarshal(raw, &thinking); err != nil {
					t.Fatal(err)
				}
				if thinking.Type != "enabled" || thinking.BudgetTokens != budgets[effort] {
					t.Fatalf("thinking=%+v want enabled/%d", thinking, budgets[effort])
				}
				if _, has := body["reasoning_effort"]; has {
					t.Fatal("Anthropic request carries reasoning_effort")
				}
			})
		}
	}
}

func TestEffortValid(t *testing.T) {
	for _, e := range []llm.Effort{"", "low", "medium", "high"} {
		if !e.Valid() {
			t.Fatalf("%q should be valid", e)
		}
	}
	for _, e := range []llm.Effort{"HIGH", "max", "1024", " low", "none"} {
		if e.Valid() {
			t.Fatalf("%q should be refused", e)
		}
	}
}

// reasoningHistory is a history whose assistant turns had reasoning streamed during
// the run: the case in which a leak would actually happen. Reasoning is display-only
// and never reaches a Message, so the history carries the visible answer only.
func reasoningHistory(secret string) llm.Request {
	req := goldenHistory()
	req.Messages = append(req.Messages,
		llm.Message{Role: llm.RoleAssistant, Content: "the file says hello"},
		llm.Message{Role: llm.RoleUser, Content: "and b.txt?"},
	)
	_ = secret
	return req
}

// TestRequestNeverEchoesReasoning keeps the leak guard of the test it replaces and makes it
// stricter: both adapters, plain and streaming, with and without effort, after a run whose
// reasoning was streamed. No message carries a reasoning key in any spelling, no reasoning
// text appears anywhere in the body, and no top-level key outside the effort parameter is a
// reasoning key.
func TestRequestNeverEchoesReasoning(t *testing.T) {
	const secret = "SECRET-CHAIN-OF-THOUGHT-7731"
	for _, tc := range wireCases() {
		for _, effort := range []llm.Effort{llm.EffortDefault, llm.EffortHigh} {
			t.Run(tc.name+"/"+string(effort), func(t *testing.T) {
				// Stream reasoning through a real adapter first, so a leak would have text to leak.
				streamSrv, _ := reasoningServer(t,
					`{"choices":[{"delta":{"reasoning_content":"`+secret+`"}}]}`,
					`{"choices":[{"delta":{"content":"the file says hello"}}]}`,
				)
				var sawReasoning bool
				resp, err := llm.NewOpenAI(llm.Config{APIKey: "k", BaseURL: streamSrv.URL, Model: "m"}).CompleteStream(
					context.Background(), goldenHistory(), func(d llm.Delta) {
						if strings.Contains(d.Reasoning, secret) {
							sawReasoning = true
						}
					})
				if err != nil || !sawReasoning {
					t.Fatalf("setup: reasoning was not streamed (err=%v)", err)
				}
				req := reasoningHistory(secret)
				req.Messages[len(req.Messages)-2] = resp.Message
				req.Effort = effort

				srv, body := captureServer(t)
				if err := tc.call(srv, req); err != nil {
					t.Fatal(err)
				}
				raw := string(body())
				if strings.Contains(raw, secret) {
					t.Fatalf("reasoning text reached the request: %s", raw)
				}
				var top map[string]json.RawMessage
				if err := json.Unmarshal([]byte(raw), &top); err != nil {
					t.Fatal(err)
				}
				for _, key := range []string{"reasoning", "reasoning_content", "include_reasoning"} {
					if _, has := top[key]; has {
						t.Fatalf("request carries %q: %s", key, raw)
					}
				}
				var msgs []map[string]json.RawMessage
				if err := json.Unmarshal(top["messages"], &msgs); err != nil {
					t.Fatal(err)
				}
				for i, m := range msgs {
					for _, key := range []string{"reasoning", "reasoning_content", "thinking", "reasoning_effort"} {
						if _, has := m[key]; has {
							t.Fatalf("messages[%d] carries %q: %s", i, key, raw)
						}
					}
					if content, ok := m["content"]; ok && strings.Contains(string(content), `"thinking"`) {
						t.Fatalf("messages[%d] carries a thinking block: %s", i, content)
					}
				}
			})
		}
	}
}

// A provider that rejects the field gets the request once. The adapter reports the
// provider's message and does not strip the field and resend, because a silent
// downgrade would make a configured effort a lie.
func TestEffortRejectionIsVisibleAndNotRetried(t *testing.T) {
	for _, tc := range wireCases() {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				w.Write([]byte(`{"error":{"message":"unknown parameter: reasoning_effort"}}`))
			}))
			t.Cleanup(srv.Close)
			req := goldenHistory()
			req.Effort = llm.EffortHigh
			err := tc.call(srv, req)
			if err == nil || !strings.Contains(err.Error(), "reasoning_effort") {
				t.Fatalf("provider message not surfaced: %v", err)
			}
			var httpErr *llm.HTTPError
			if !errors.As(err, &httpErr) || httpErr.Status != 400 {
				t.Fatalf("not a 400 HTTPError: %v", err)
			}
			if n := requests.Load(); n != 1 {
				t.Fatalf("requests=%d, want exactly 1 (no strip-and-resend)", n)
			}
		})
	}
}
