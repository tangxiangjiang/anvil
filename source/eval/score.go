// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/anvil-dev/anvil/internal/llm"
	"github.com/anvil-dev/anvil/internal/permissions"
	"github.com/anvil-dev/anvil/internal/runtime"
)

// Score is shared by all tiers. RequestBytes counts provider-neutral Request
// JSON, not HTTP bytes. Retries counts retry events (0.48+). Agents counts the distinct child agents that emitted an event (0.50+); a run with no children is 0.
type Score struct {
	Completed    bool  `json:"completed"`
	Steps        int   `json:"steps"`
	ToolErrors   int   `json:"tool_errors"`
	Refused      int   `json:"refused"`
	LoopStops    int   `json:"loop_stops"`
	Retries      int   `json:"retries"`
	RequestBytes int64 `json:"request_bytes"`
	Agents       int   `json:"agents"`
}

// CallbackRefusal is the eval-controlled callback denial marker. Arbitrary
// callback failures are not safely distinguishable from execution errors.
const CallbackRefusal = "eval: permission callback refused"

type scorer struct {
	mu             sync.Mutex
	retries        int
	toolErrors     int
	refused        int
	childLoopStops int
	agents         map[string]bool // distinct child agents that emitted an event
}

// noteAgent counts a child agent once, however many events it emits.
func (s *scorer) noteAgent(id string) {
	if id == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.agents == nil {
		s.agents = map[string]bool{}
	}
	s.agents[id] = true
}

func (s *scorer) observe(ev runtime.Event) {
	if ev.Type == "retry" {
		s.mu.Lock()
		s.retries++
		s.mu.Unlock()
		return
	}
	if ev.Type != "tool_result" || !ev.IsError {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.toolErrors++
	if refused(ev) {
		s.refused++
	}
}

func refused(ev runtime.Event) bool {
	if ev.Rule != nil && ev.Rule.Matched && ev.Rule.Effect == permissions.Deny {
		return true
	}
	if ev.Content == CallbackRefusal {
		return true
	}
	if ev.Rule != nil && ev.Rule.Matched && ev.Rule.Effect == permissions.Ask &&
		strings.HasSuffix(ev.Content, "asks for confirmation and no approval channel is installed") {
		return true
	}
	return strings.HasPrefix(ev.Content, "path escapes cwd: ") ||
		strings.HasPrefix(ev.Content, "cannot read the permission rules, so this call is refused until the rule file is fixed: ")
}

func (s *scorer) finish(result runtime.Result, completed bool, requestBytes int64) Score {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := Score{Completed: completed, Steps: result.Steps, ToolErrors: s.toolErrors,
		Refused: s.refused, RequestBytes: requestBytes, LoopStops: s.childLoopStops, Retries: s.retries, Agents: len(s.agents)}
	if result.Code == runtime.CodeLoopDetected {
		out.LoopStops++
	}
	return out
}

// measuredCompleter accounts for every request, including failed and child
// requests. It intentionally uses Complete, not transport-specific streaming.
type measuredCompleter struct {
	upstream llm.Completer
	mu       sync.Mutex
	bytes    int64
	err      error
}

func (m *measuredCompleter) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	body, err := json.Marshal(req)
	if err == nil {
		m.mu.Lock()
		m.bytes += int64(len(body))
		m.mu.Unlock()
		resp, callErr := m.upstream.Complete(ctx, req)
		if callErr == nil && resp == nil {
			callErr = fmt.Errorf("completer returned no response")
		}
		if callErr == nil {
			return resp, nil
		}
		err = callErr
	}
	m.mu.Lock()
	if m.err == nil {
		m.err = err
	}
	m.mu.Unlock()
	return nil, err
}

func (m *measuredCompleter) totals() (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.bytes, m.err
}
