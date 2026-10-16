// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package editorhost

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// scripted OpenAI-compatible SSE stub
// ---------------------------------------------------------------------------

// turn is one scripted model response. Exactly one of content / tool is used.
type turn struct {
	content string
	toolID  string
	tool    string
	args    string
}

func textTurn(content string) turn {
	return turn{content: content}
}

func toolTurn(id, name, args string) turn {
	return turn{toolID: id, tool: name, args: args}
}

// stubLLM serves an OpenAI-compatible streaming endpoint that walks a fixed
// script. Request n gets turns[n]; requests past the script get a generic
// closing answer so a misbehaving run cannot hang the test.
type stubLLM struct {
	srv   *httptest.Server
	mu    sync.Mutex
	turns []turn
	calls int
	// lastBody is the raw body of the most recent request, so a test can assert
	// on what actually reached the model (system text, prompt prefixes).
	lastBody string
	// blockUntilCancel makes every request hang until the client goes away,
	// which is how the cancel path is exercised.
	blockUntilCancel bool
}

func newStubLLM(t *testing.T, turns ...turn) *stubLLM {
	t.Helper()
	s := &stubLLM{turns: turns}
	s.srv = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *stubLLM) baseURL() string { return s.srv.URL + "/v1" }

func (s *stubLLM) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (s *stubLLM) lastRequestBody(t *testing.T) string {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.calls == 0 {
		t.Fatal("the stub has not been called yet")
	}
	return s.lastBody
}

func (s *stubLLM) serve(w http.ResponseWriter, r *http.Request) {
	if s.blockUntilCancel {
		<-r.Context().Done()
		return
	}
	body, _ := io.ReadAll(r.Body)

	s.mu.Lock()
	var current turn
	if s.calls < len(s.turns) {
		current = s.turns[s.calls]
	}
	s.calls++
	s.lastBody = string(body)
	s.mu.Unlock()

	w.Header().Set("Content-Type", "text/event-stream")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "no flusher", http.StatusInternalServerError)
		return
	}
	write := func(v any) {
		raw, _ := json.Marshal(v)
		fmt.Fprintf(w, "data: %s\n\n", raw)
		flusher.Flush()
	}

	if current.tool != "" {
		write(map[string]any{"choices": []any{map[string]any{
			"delta": map[string]any{"tool_calls": []any{map[string]any{
				"index": 0, "id": current.toolID, "type": "function",
				"function": map[string]any{"name": current.tool, "arguments": current.args},
			}}},
		}}})
	} else {
		// Split the text so the streaming path yields more than one delta.
		for _, chunk := range splitChunks(current.content, 4) {
			write(map[string]any{"choices": []any{map[string]any{
				"delta": map[string]any{"content": chunk},
			}}})
		}
	}
	write(map[string]any{
		"choices": []any{},
		"usage":   map[string]any{"prompt_tokens": 5, "completion_tokens": 3, "total_tokens": 8},
	})
	fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()
}

func splitChunks(s string, size int) []string {
	if s == "" {
		return nil
	}
	var out []string
	for len(s) > size {
		out = append(out, s[:size])
		s = s[size:]
	}
	return append(out, s)
}

// ---------------------------------------------------------------------------
// client harness
// ---------------------------------------------------------------------------

type harness struct {
	t       *testing.T
	srv     *Server
	in      *io.PipeWriter
	out     chan map[string]any
	pending []map[string]any
	nextID  int64
}

// startHarness wires a Server to in-memory pipes and drives it from a reader
// goroutine.
func startHarness(t *testing.T, cfg Config) *harness {
	t.Helper()
	// The host never carries credentials on the wire; it reads them from the
	// process environment the client controls. Pin that environment so tests do
	// not depend on the developer's shell.
	t.Setenv("ANVIL_API_KEY", "test-key")
	t.Setenv("ANVIL_MODEL", "")
	t.Setenv("ANVIL_BASE_URL", "")
	t.Setenv("ANVIL_PROVIDER", "")

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	cfg.Logf = t.Logf
	srv := New(cfg, inR, outW)

	h := &harness{t: t, srv: srv, in: inW, out: make(chan map[string]any, 512)}
	go func() {
		_ = srv.Serve(context.Background())
		_ = outW.Close()
	}()
	go func() {
		sc := bufio.NewScanner(outR)
		sc.Buffer(make([]byte, 0, 64*1024), maxLine)
		for sc.Scan() {
			var m map[string]any
			if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
				continue
			}
			h.out <- m
		}
		close(h.out)
	}()
	t.Cleanup(func() { _ = inW.Close(); _ = outR.Close() })
	return h
}

func (h *harness) write(v any) {
	h.t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		h.t.Fatalf("marshal: %v", err)
	}
	if _, err := h.in.Write(append(raw, '\n')); err != nil {
		h.t.Fatalf("write to host: %v", err)
	}
}

// call sends a request and returns its response, buffering any interleaved
// notifications for later next() calls.
func (h *harness) call(method string, params any) map[string]any {
	h.t.Helper()
	h.nextID++
	id := h.nextID
	h.write(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	deadline := time.After(15 * time.Second)
	for {
		select {
		case m, ok := <-h.out:
			if !ok {
				h.t.Fatalf("%s: host output closed before a reply", method)
			}
			if got, ok := m["id"].(float64); ok && int64(got) == id {
				return m
			}
			h.pending = append(h.pending, m)
		case <-deadline:
			h.t.Fatalf("%s: timeout waiting for reply", method)
		}
	}
}

// callOK sends a request and fails the test on an RPC error.
func (h *harness) callOK(method string, params any) map[string]any {
	h.t.Helper()
	m := h.call(method, params)
	if rpcErr, ok := rpcErrorOf(m); ok {
		h.t.Fatalf("%s: unexpected rpc error %d: %s", method, rpcErr.Code, rpcErr.Message)
	}
	return m
}

func (h *harness) next(timeout time.Duration) map[string]any {
	h.t.Helper()
	if len(h.pending) > 0 {
		m := h.pending[0]
		h.pending = h.pending[1:]
		return m
	}
	select {
	case m, ok := <-h.out:
		if !ok {
			h.t.Fatal("host output closed")
		}
		return m
	case <-time.After(timeout):
		h.t.Fatal("timeout waiting for a host notification")
	}
	return nil
}

// runOutcome is one run driven to its terminal done notification.
type runOutcome struct {
	events    []map[string]any
	approvals []map[string]any
	done      map[string]any
}

// autoApprove answers every approval.request with DecisionAllow.
func autoApprove(map[string]any) map[string]any {
	return map[string]any{"decision": DecisionAllow}
}

// collectRun drains notifications until the terminal done, answering approvals
// with the given policy. Return nil from answer to leave a request pending —
// which is how the timeout and cancel paths are exercised.
func (h *harness) collectRun(timeout time.Duration, answer func(req map[string]any) map[string]any) runOutcome {
	h.t.Helper()
	var out runOutcome
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		m := h.next(time.Until(deadline))
		switch m["method"] {
		case NotifyEvent:
			out.events = append(out.events, m)
		case NotifyApprovalRequest:
			out.approvals = append(out.approvals, m)
			if answer == nil {
				continue
			}
			resp := answer(paramsOf(h.t, m))
			if resp == nil {
				continue
			}
			params := map[string]any{"request_id": paramsOf(h.t, m)["request_id"]}
			for k, v := range resp {
				params[k] = v
			}
			h.callOK(MethodApprovalResponse, params)
		case NotifyDone:
			out.done = m
			return out
		}
	}
	h.t.Fatal("run never reached done")
	return out
}

// collectRunOK is collectRun with every approval allowed.
func (h *harness) collectRunOK(timeout time.Duration) runOutcome {
	h.t.Helper()
	return h.collectRun(timeout, autoApprove)
}

func rpcErrorOf(m map[string]any) (*RPCError, bool) {
	raw, ok := m["error"].(map[string]any)
	if !ok {
		return nil, false
	}
	code, _ := raw["code"].(float64)
	msg, _ := raw["message"].(string)
	return &RPCError{Code: int(code), Message: msg}, true
}

func paramsOf(t *testing.T, m map[string]any) map[string]any {
	t.Helper()
	p, ok := m["params"].(map[string]any)
	if !ok {
		t.Fatalf("notification has no params object: %v", m)
	}
	return p
}

func resultOf(t *testing.T, m map[string]any) map[string]any {
	t.Helper()
	r, ok := m["result"].(map[string]any)
	if !ok {
		t.Fatalf("response has no result object: %v", m)
	}
	return r
}

func eventTypes(events []map[string]any) []string {
	out := make([]string, 0, len(events))
	for _, e := range events {
		p, _ := e["params"].(map[string]any)
		typ, _ := p["type"].(string)
		out = append(out, typ)
	}
	return out
}

// newWorkspace returns a temp dir that looks like an editor workspace.
func newWorkspace(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if _, err := os.Stat(filepath.Join(dir, ".anvil")); err == nil {
		t.Fatalf("unexpected .anvil in fresh temp dir")
	}
	return dir
}
