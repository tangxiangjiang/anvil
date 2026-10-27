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
	"testing"

	"github.com/anvil-dev/anvil/internal/llm"
)

// stopServer answers every request on either wire with a body that ends the way the
// test asks: a finish_reason / stop_reason, or none.
func stopServer(t *testing.T, openaiFinish, anthropicStop string, stream bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/messages" {
			if stream {
				w.Header().Set("Content-Type", "text/event-stream")
				io := func(s string) { w.Write([]byte(s)) }
				io("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
				io("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"cut\"}}\n\n")
				io("event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
				delta := `{"type":"message_delta","delta":{},"usage":{"output_tokens":1}}`
				if anthropicStop != "" {
					delta = `{"type":"message_delta","delta":{"stop_reason":"` + anthropicStop + `"},"usage":{"output_tokens":1}}`
				}
				io("event: message_delta\ndata: " + delta + "\n\n")
				io("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
				return
			}
			w.Header().Set("Content-Type", "application/json")
			stop := ""
			if anthropicStop != "" {
				stop = `,"stop_reason":"` + anthropicStop + `"`
			}
			w.Write([]byte(`{"content":[{"type":"text","text":"cut"}]` + stop + `,"usage":{"input_tokens":1,"output_tokens":1}}`))
			return
		}
		if stream {
			w.Header().Set("Content-Type", "text/event-stream")
			finish := "null"
			if openaiFinish != "" {
				finish = `"` + openaiFinish + `"`
			}
			w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"cut\"}}]}\n\n"))
			w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":" + finish + "}]}\n\n"))
			w.Write([]byte("data: [DONE]\n\n"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		finish := ""
		if openaiFinish != "" {
			finish = `,"finish_reason":"` + openaiFinish + `"`
		}
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"cut"}` + finish + `}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestStopReason(t *testing.T) {
	cases := []struct {
		openai, anthropic string
		want              llm.StopReason
	}{
		{"stop", "end_turn", llm.StopEnd},
		{"tool_calls", "tool_use", llm.StopTool},
		{"length", "max_tokens", llm.StopLength},
		{"content_filter", "refusal", llm.StopOther},
		{"", "", llm.StopOther}, // absent maps to other, never to end
	}
	for _, tc := range cases {
		for _, w := range wireCases() {
			name := w.name + "/" + string(tc.want)
			if tc.openai == "" {
				name += "/absent"
			}
			t.Run(name, func(t *testing.T) {
				stream := strings.HasSuffix(w.name, "_stream")
				srv := stopServer(t, tc.openai, tc.anthropic, stream)
				var resp *llm.Response
				var err error
				switch w.name {
				case "openai_plain":
					resp, err = llm.NewOpenAI(llm.Config{APIKey: "k", BaseURL: srv.URL, Model: "m"}).Complete(context.Background(), goldenHistory())
				case "openai_stream":
					resp, err = llm.NewOpenAI(llm.Config{APIKey: "k", BaseURL: srv.URL, Model: "m"}).CompleteStream(context.Background(), goldenHistory(), func(llm.Delta) {})
				case "anthropic_plain":
					resp, err = llm.NewAnthropic(llm.Config{APIKey: "k", BaseURL: srv.URL, Model: "m"}).Complete(context.Background(), goldenHistory())
				case "anthropic_stream":
					resp, err = llm.NewAnthropic(llm.Config{APIKey: "k", BaseURL: srv.URL, Model: "m"}).CompleteStream(context.Background(), goldenHistory(), func(llm.Delta) {})
				}
				if err != nil {
					t.Fatal(err)
				}
				if resp.StopReason != tc.want {
					t.Fatalf("StopReason=%q, want %q", resp.StopReason, tc.want)
				}
			})
		}
	}
}

// MaxOutputTokens zero keeps today's bytes (the goldens already prove that); non-zero
// reaches each wire in its own spelling, and Anthropic never sends less than the room
// the thinking budget needs.
func TestMaxOutputTokens(t *testing.T) {
	for _, w := range wireCases() {
		for _, c := range []struct {
			effort llm.Effort
			max    int
			want   int // Anthropic max_tokens
		}{
			{llm.EffortDefault, 0, 8192},
			{llm.EffortDefault, 2048, 2048},
			{llm.EffortDefault, 100000, 100000},
			{llm.EffortHigh, 0, 24576},
			{llm.EffortHigh, 4096, 24576},   // the floor wins: the thinking budget must fit
			{llm.EffortHigh, 50000, 50000},  // a larger ceiling wins
			{llm.EffortMedium, 1000, 8192},  // under the 4096 budget the provider would refuse it
			{llm.EffortLow, 500, 8192},
		} {
			t.Run(w.name+"/"+string(c.effort), func(t *testing.T) {
				req := goldenHistory()
				req.Effort, req.MaxOutputTokens = c.effort, c.max
				body := sentBody(t, w, req)
				if !w.anthropic {
					raw, has := body["max_completion_tokens"]
					if c.max == 0 {
						if has {
							t.Fatalf("max_completion_tokens sent with no limit: %s", raw)
						}
						return
					}
					var got int
					if err := json.Unmarshal(raw, &got); err != nil || got != c.max {
						t.Fatalf("max_completion_tokens=%s want %d", raw, c.max)
					}
					return
				}
				var got int
				if err := json.Unmarshal(body["max_tokens"], &got); err != nil || got != c.want {
					t.Fatalf("max_tokens=%s want %d", body["max_tokens"], c.want)
				}
			})
		}
	}
}

// Kind is set once, from the status, and Retryable is derived from it, so the two can
// never disagree.
func TestErrorKind(t *testing.T) {
	cases := []struct {
		status    int
		kind      llm.Kind
		retryable bool
	}{
		{0, llm.KindTransport, true},
		{401, llm.KindAuth, false},
		{403, llm.KindAuth, false},
		{429, llm.KindRateLimited, true},
		{500, llm.KindServer, true},
		{503, llm.KindServer, true},
		{400, llm.KindRejected, false},
		{404, llm.KindRejected, false},
		{422, llm.KindRejected, false},
	}
	for _, tc := range cases {
		e := llm.NewHTTPError(tc.status, "provider error: x")
		if e.Kind != tc.kind {
			t.Fatalf("status %d: Kind=%v want %v", tc.status, e.Kind, tc.kind)
		}
		if e.Retryable != tc.retryable {
			t.Fatalf("status %d: Retryable=%v want %v", tc.status, e.Retryable, tc.retryable)
		}
	}
}

// Every error a real adapter returns carries a Kind, and its Retryable agrees with it.
func TestErrorKindOnEveryAdapterPath(t *testing.T) {
	statuses := map[int]llm.Kind{401: llm.KindAuth, 403: llm.KindAuth, 429: llm.KindRateLimited, 500: llm.KindServer, 400: llm.KindRejected}
	for status, kind := range statuses {
		for _, w := range wireCases() {
			t.Run(w.name, func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
					rw.WriteHeader(status)
					rw.Write([]byte(`{"error":{"message":"detail from the provider"}}`))
				}))
				defer srv.Close()
				err := w.call(srv, goldenHistory())
				var he *llm.HTTPError
				if !errors.As(err, &he) {
					t.Fatalf("status %d: not an HTTPError: %v", status, err)
				}
				if he.Kind != kind || he.Retryable != llm.KindRetryable(kind) {
					t.Fatalf("status %d: Kind=%v Retryable=%v, want %v/%v", status, he.Kind, he.Retryable, kind, llm.KindRetryable(kind))
				}
				if !strings.Contains(he.Message, "detail from the provider") {
					t.Fatalf("the provider's message must stay verbatim: %q", he.Message)
				}
			})
		}
	}
}

func TestErrorKindForEmptyKeyAndTransport(t *testing.T) {
	for _, w := range wireCases() {
		t.Run(w.name, func(t *testing.T) {
			// no key: an auth failure, found before any request
			var err error
			switch w.name {
			case "openai_plain":
				_, err = llm.NewOpenAI(llm.Config{}).Complete(context.Background(), goldenHistory())
			case "openai_stream":
				_, err = llm.NewOpenAI(llm.Config{}).CompleteStream(context.Background(), goldenHistory(), nil)
			case "anthropic_plain":
				_, err = llm.NewAnthropic(llm.Config{}).Complete(context.Background(), goldenHistory())
			case "anthropic_stream":
				_, err = llm.NewAnthropic(llm.Config{}).CompleteStream(context.Background(), goldenHistory(), nil)
			}
			var he *llm.HTTPError
			if !errors.As(err, &he) || he.Kind != llm.KindAuth || he.Retryable {
				t.Fatalf("missing key: %+v", he)
			}
			// nothing listening: a transport failure
			srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			url := srv.URL
			srv.Close()
			dead := wireCase{name: w.name, anthropic: w.anthropic, call: w.call}
			fake := httptest.NewUnstartedServer(nil)
			fake.Listener.Close()
			_ = fake
			_ = dead
			err = rawCall(w.name, url)
			if !errors.As(err, &he) || he.Kind != llm.KindTransport || !he.Retryable {
				t.Fatalf("transport: %+v (%v)", he, err)
			}
		})
	}
}

func rawCall(name, url string) error {
	cfg := llm.Config{APIKey: "k", BaseURL: url, Model: "m"}
	switch name {
	case "openai_plain":
		_, err := llm.NewOpenAI(cfg).Complete(context.Background(), goldenHistory())
		return err
	case "openai_stream":
		_, err := llm.NewOpenAI(cfg).CompleteStream(context.Background(), goldenHistory(), func(llm.Delta) {})
		return err
	case "anthropic_plain":
		_, err := llm.NewAnthropic(cfg).Complete(context.Background(), goldenHistory())
		return err
	}
	_, err := llm.NewAnthropic(cfg).CompleteStream(context.Background(), goldenHistory(), func(llm.Delta) {})
	return err
}

func TestKindCodeMapping(t *testing.T) {
	cases := map[llm.Kind]string{
		llm.KindTransport:   "provider",
		llm.KindServer:      "provider",
		llm.KindRateLimited: "provider_busy",
		llm.KindRejected:    "request_rejected",
		llm.KindAuth:        "auth",
	}
	for kind, code := range cases {
		if got := llm.KindCode(kind); got != code {
			t.Fatalf("Kind %v -> %q, want %q", kind, got, code)
		}
	}
}
