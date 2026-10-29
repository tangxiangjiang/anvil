// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anvil-dev/anvil/internal/llm"
)

const stateSecret = "SIG-provider-state-xyz"

// stateResponse is an assistant turn that calls a tool and carries a provider's tool-loop state.
func stateResponse(id, name, args string) *llm.Response {
	r := callResponse(id, name, args)
	r.Message.ProviderState = []byte(`[{"type":"thinking","thinking":"t","signature":"` + stateSecret + `"}]`)
	return r
}

// ProviderState rides the assistant turn through the run's own tool loop, is gone from the
// next run's history, and never reaches the session file.
func TestProviderStateLivesForOneRunAndIsNeverPersisted(t *testing.T) {
	dir := t.TempDir()
	a, err := Create(context.Background(), subagentTestOptions(dir))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	hasState := func(req llm.Request) bool {
		for _, m := range req.Messages {
			if len(m.ProviderState) > 0 {
				return true
			}
		}
		return false
	}
	var seen []bool
	a.complete = completerFunc(func(_ context.Context, req llm.Request) (*llm.Response, error) {
		seen = append(seen, hasState(req))
		switch lastUserText(req) {
		case "first":
			if len(trailingToolResults(req)) == 0 {
				return stateResponse("c1", "list_skills", `{}`), nil
			}
		}
		return textResponse("done"), nil
	})
	send := func(text string) {
		run, err := a.Send(context.Background(), text)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := run.Wait(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	send("first")
	send("second")

	// first run: request 1 has none, request 2 (after the tool) has it; second run: none.
	if len(seen) != 3 || seen[0] || !seen[1] || seen[2] {
		t.Fatalf("ProviderState on requests = %v, want [false true false]", seen)
	}

	// Nothing on disk carries it.
	var found bool
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if data, rerr := os.ReadFile(path); rerr == nil && strings.Contains(string(data), stateSecret) {
			found = true
			t.Errorf("provider state reached %s", path)
		}
		return nil
	})
	if found {
		t.FailNow()
	}
}
