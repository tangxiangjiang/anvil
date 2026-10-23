// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/anvil-dev/anvil/internal/llm"
)

// Reply is one scripted answer, optionally asserting on the request it answers.
type Reply struct {
	Message llm.Message
	Expect  func(llm.Request) error
}

// Replies keeps fixtures that need no predicates concise.
func Replies(messages []llm.Message) []Reply {
	out := make([]Reply, len(messages))
	for i, message := range messages {
		out[i].Message = message
	}
	return out
}

// ScriptCompleter checks requests before supplying its scripted answers.
// A single script is sequential; use separate scripts for independent children.
type ScriptCompleter struct {
	mu      sync.Mutex
	replies []Reply
	n       int
	err     error
}

func NewScript(replies []Reply) *ScriptCompleter {
	s := &ScriptCompleter{replies: make([]Reply, len(replies))}
	for i, reply := range replies {
		body, err := json.Marshal(reply.Message)
		if err == nil {
			err = json.Unmarshal(body, &s.replies[i].Message)
		}
		if err != nil {
			s.err = fmt.Errorf("script reply %d: %w", i+1, err)
			break
		}
		s.replies[i].Expect = reply.Expect
	}
	return s
}

func (s *ScriptCompleter) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	if err := ctx.Err(); err != nil {
		s.err = err
		return nil, err
	}
	if s.n >= len(s.replies) {
		// Preserve the original six fixtures' exhaustion behaviour.
		return &llm.Response{Message: llm.Message{Role: llm.RoleAssistant, Content: "end"}}, nil
	}
	reply := s.replies[s.n]
	s.n++
	snapshot, err := snapshotRequest(req)
	if err != nil {
		s.err = err
		return nil, err
	}
	if reply.Expect != nil {
		// The predicate gets its own copy, not the runtime's history slice.
		input, _ := snapshotRequest(snapshot)
		if err := reply.Expect(input); err != nil {
			body, _ := json.MarshalIndent(snapshot, "", "  ")
			s.err = fmt.Errorf("script reply %d: %w\nrequest=%s", s.n, err, body)
			return nil, s.err
		}
	}
	body, err := json.Marshal(llm.Response{Message: reply.Message})
	if err != nil {
		s.err = err
		return nil, err
	}
	var response llm.Response
	if err := json.Unmarshal(body, &response); err != nil {
		s.err = err
		return nil, err
	}
	return &response, nil
}

func (s *ScriptCompleter) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func snapshotRequest(req llm.Request) (llm.Request, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return llm.Request{}, fmt.Errorf("capture request: %w", err)
	}
	var out llm.Request
	if err := json.Unmarshal(body, &out); err != nil {
		return llm.Request{}, err
	}
	return out, nil
}
