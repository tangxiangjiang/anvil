//go:build anvileval

// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	anvil "github.com/anvil-dev/anvil"
	"github.com/anvil-dev/anvil/internal/llm"
	"github.com/anvil-dev/anvil/internal/runtime"
)

// The invariants under Multi and Complex (chapter 50 section 6). Each task proves the same
// capability as its Single twin, but the action is taken by a worker: a higher mode adds
// structure and never removes a capability.

// planTools is what the host's plan mode withholds, plus delegate: a plan is a document,
// not a child that edits.
var planTools = []string{"write", "edit", "shell", "verify", "delegate"}

// isWorker says whether a request is a Worker's, from its system prompt: the worker role's
// own sentence. The last user message is not a safe signal, because the loop guard injects
// its own user turn into a run that repeats itself.
func isWorker(req llm.Request) bool {
	return strings.Contains(systemText(req), "The brief below is your task")
}

func systemText(req llm.Request) string {
	for _, m := range req.Messages {
		if m.Role == llm.RoleSystem {
			return m.Content
		}
	}
	return ""
}

func delegateAnswer(files []string, tools string, worker func(round int) *llm.Response, final string) fixtureCompleter {
	brief := map[string]any{"goal": "do the work", "plan": []string{"act"}, "files": files, "acceptance": []string{"done"}}
	if tools != "" {
		brief["tools"] = []string{tools}
	}
	raw, _ := json.Marshal(brief)
	return func(_ context.Context, req llm.Request) (*llm.Response, error) {
		_, round := userAndTools(req)
		if isWorker(req) {
			if r := worker(round); r != nil {
				return r, nil
			}
			return textAnswer("[met] done"), nil
		}
		if round == 0 {
			return toolAnswer("d", "delegate", string(raw)), nil
		}
		return textAnswer(final), nil
	}
}

type roadmapItem struct {
	id, goal string
	files    []string
	deps     []string
}

func roadmapText(items ...roadmapItem) string {
	var list []map[string]any
	for _, it := range items {
		deps := it.deps
		if deps == nil {
			deps = []string{}
		}
		list = append(list, map[string]any{"id": it.id, "goal": it.goal, "plan": []string{"act"},
			"files": it.files, "acceptance": []string{"done"}, "depends_on": deps})
	}
	raw, _ := json.Marshal(map[string]any{"items": list})
	return string(raw)
}

// complexAnswer scripts the Decision and Analyze stages and hands each Worker to worker,
// which gets the item's goal (the worker's brief names it) and the worker's round.
func complexAnswer(roadmap string, worker func(goal string, round int) *llm.Response) fixtureCompleter {
	return func(_ context.Context, req llm.Request) (*llm.Response, error) {
		sys := systemText(req)
		_, round := userAndTools(req)
		switch {
		case strings.Contains(sys, "You are the decision stage"):
			return textAnswer(`{"summary":"act","options":[{"name":"A","rationale":"r"}],"chosen":"A"}`), nil
		case strings.Contains(sys, "You are the analysis stage"):
			return textAnswer(roadmap), nil
		case isWorker(req):
			goal := ""
			if at := strings.Index(sys, "Goal: "); at >= 0 {
				goal = sys[at+len("Goal: "):]
				if nl := strings.IndexByte(goal, '\n'); nl >= 0 {
					goal = goal[:nl]
				}
			}
			if r := worker(goal, round); r != nil {
				return r, nil
			}
			return textAnswer("[met] done"), nil
		}
		return textAnswer("unexpected request"), nil
	}
}

func multiOpts(o anvil.Options) anvil.Options {
	o.Mode = anvil.ModeMulti
	o.Subagents.Enabled = true
	return o
}

func complexOpts(o anvil.Options) anvil.Options {
	o.Mode = anvil.ModeComplex
	o.Subagents.Enabled = true
	return o
}

// named gives a case its baseline key, task@mode. The task inside keeps the plain name:
// the suffix is the baseline's, not the run's.
func named(mode anvil.Mode, name string, c evalCase) evalCase {
	c.name = name + "@" + modeName(mode)
	return c
}

func delegateResult(result runtime.Result) string {
	for _, m := range result.Messages {
		if m.Name == "delegate" {
			return m.Content
		}
	}
	return ""
}

// restoreRoot restores the root run of the tree and checks both files come back.
func restoreRoot(dir string, out Outcome) error {
	for name, want := range map[string]string{"a.txt": "ONE", "b.txt": "TWO"} {
		if data, _ := os.ReadFile(filepath.Join(dir, name)); string(data) != want {
			return fmt.Errorf("%s=%q before restore, want %q", name, data, want)
		}
	}
	runs, err := anvil.ListCheckpoints(dir, out.AgentID, anvil.CheckpointOptions{})
	if err != nil || len(runs) != 1 || len(runs[0].Files) != 2 {
		return fmt.Errorf("the root run should list the whole tree once: runs=%+v err=%v", runs, err)
	}
	restored, err := anvil.RestoreCheckpoint(dir, out.AgentID, runs[0].RunID, anvil.CheckpointOptions{})
	if err != nil || len(restored) != 2 {
		return fmt.Errorf("restored=%+v err=%v", restored, err)
	}
	for _, f := range restored {
		if f.Skipped {
			return fmt.Errorf("restore skipped %s: %s", f.Path, f.Reason)
		}
	}
	for name, want := range map[string]string{"a.txt": "one", "b.txt": "two"} {
		if data, _ := os.ReadFile(filepath.Join(dir, name)); string(data) != want {
			return fmt.Errorf("%s=%q after restore, want %q", name, data, want)
		}
	}
	return nil
}

func seedAB(dir string) error {
	for name, content := range map[string]string{"a.txt": "one", "b.txt": "two"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func editAB(name string) *llm.Response {
	if name == "a.txt" {
		return toolAnswer("ea", "edit_file", `{"path":"a.txt","old_string":"one","new_string":"ONE"}`)
	}
	return toolAnswer("eb", "edit_file", `{"path":"b.txt","old_string":"two","new_string":"TWO"}`)
}

func multiInvariants() []evalCase {
	m := anvil.ModeMulti
	asked := 0
	loopRead := func(int) *llm.Response { return toolAnswer("r", "read_file", `{"path":"missing.txt"}`) }
	var sawDelegateTool bool
	return []evalCase{
		named(m, "inv_permission_deny", invariantCase("inv_permission_deny", Task{UserMsg: "delegate the denied write", Check: finishedOnly},
			multiOpts(anvil.Options{BuiltinTools: []string{"delegate"},
				PermissionRules: []anvil.PermissionRule{{Effect: "deny", Tool: "write_file", Pattern: "*"}}}),
			delegateAnswer([]string{"denied.txt"}, "write", func(round int) *llm.Response {
				if round == 0 {
					return toolAnswer("w", "write_file", `{"path":"denied.txt","content":"x"}`)
				}
				return nil
			}, "done"),
			func(dir string, _ Outcome) error { return absent(dir, "denied.txt") })),
		named(m, "inv_permission_ask", invariantCase("inv_permission_ask", Task{UserMsg: "delegate the asked write", Check: finishedOnly},
			multiOpts(anvil.Options{BuiltinTools: []string{"delegate"}, Permission: func(context.Context, anvil.PermissionRequest) (anvil.PermissionDecision, error) {
				asked++
				return anvil.PermissionDecision{Allow: false, Message: CallbackRefusal}, nil
			}}),
			delegateAnswer([]string{"asked.txt"}, "write", func(round int) *llm.Response {
				if round == 0 {
					return toolAnswer("w", "write_file", `{"path":"asked.txt","content":"x"}`)
				}
				return nil
			}, "done"),
			func(dir string, _ Outcome) error {
				if asked != 1 {
					return fmt.Errorf("the permission callback was called %d times for a worker's write, want 1", asked)
				}
				return absent(dir, "asked.txt")
			})),
		// A worker that loops ends with loop_detected; its parent reads the failure and
		// carries on to its own answer.
		named(m, "inv_loop_guard", invariantCase("inv_loop_guard", Task{UserMsg: "delegate a loop", Check: func(dir string, result runtime.Result) error {
			if err := finishedOnly(dir, result); err != nil {
				return err
			}
			if got := delegateResult(result); !strings.Contains(got, "failed") {
				return fmt.Errorf("the parent did not read the worker's failure: %q", got)
			}
			return nil
		}}, multiOpts(anvil.Options{BuiltinTools: []string{"delegate"}}),
			delegateAnswer([]string{"out.txt"}, "read", loopRead, "the worker failed"), nil)),
		named(m, "inv_checkpoint_restore", invariantCase("inv_checkpoint_restore", Task{UserMsg: "delegate the edits", Check: finishedOnly, Setup: seedAB},
			multiOpts(anvil.Options{BuiltinTools: []string{"delegate"}, Checkpoints: &anvil.CheckpointOptions{}}),
			func() fixtureCompleter {
				step := 0
				inner := delegateAnswer([]string{"a.txt", "b.txt"}, "edit", func(round int) *llm.Response {
					switch round {
					case 0:
						return editAB("a.txt")
					case 1:
						return editAB("b.txt")
					}
					return nil
				}, "edited")
				_ = step
				return inner
			}(), restoreRoot)),
		named(m, "inv_sandbox_escape", invariantCase("inv_sandbox_escape", Task{UserMsg: "delegate a read outside", Check: finishedOnly},
			multiOpts(anvil.Options{BuiltinTools: []string{"delegate"}}),
			delegateAnswer([]string{"out.txt"}, "read", func(round int) *llm.Response {
				if round == 0 {
					return toolAnswer("e", "read_file", `{"path":"../secret.txt"}`)
				}
				return nil
			}, "blocked"), nil)),
		// Plan mode removes delegate with the write tools: a plan is a document, not a
		// child that edits.
		named(m, "inv_plan_readonly", invariantCase("inv_plan_readonly", Task{UserMsg: "write in a read-only tool set", Check: func(dir string, result runtime.Result) error {
			if err := finishedOnly(dir, result); err != nil {
				return err
			}
			for _, message := range result.Messages {
				if message.Role == llm.RoleTool && strings.Contains(message.Content, "unknown tool: write_file") {
					return nil
				}
			}
			return fmt.Errorf("write_file was not reported as an unknown tool")
		}}, multiOpts(anvil.Options{DisallowedTools: planTools}),
			func(_ context.Context, req llm.Request) (*llm.Response, error) {
				for _, def := range req.Tools {
					if def.Name == "delegate" {
						sawDelegateTool = true
					}
				}
				if _, round := userAndTools(req); round == 0 {
					return toolAnswer("w", "write_file", `{"path":"plan.txt","content":"x"}`), nil
				}
				return textAnswer("done"), nil
			}, func(dir string, _ Outcome) error {
				if sawDelegateTool {
					return fmt.Errorf("delegate was offered to the model in plan mode")
				}
				return absent(dir, "plan.txt")
			})),
		named(m, "inv_child_never_widens", invariantCase("inv_child_never_widens", Task{UserMsg: "delegate a write to a read-only worker", Check: finishedOnly},
			multiOpts(anvil.Options{BuiltinTools: []string{"delegate"}}),
			delegateAnswer([]string{"widened.txt"}, "read", func(round int) *llm.Response {
				if round == 0 {
					return toolAnswer("w", "write_file", `{"path":"widened.txt","content":"x"}`)
				}
				return nil
			}, "done"),
			func(dir string, _ Outcome) error { return absent(dir, "widened.txt") })),
		named(m, "inv_child_result_only", invariantCase("inv_child_result_only", Task{UserMsg: "delegate a read", Check: func(dir string, result runtime.Result) error {
			if err := finishedOnly(dir, result); err != nil {
				return err
			}
			for _, message := range result.Messages {
				if strings.Contains(message.Content, "CHILD-INTERNAL") {
					return fmt.Errorf("a worker's internal message reached the parent history: %+v", message)
				}
			}
			if !strings.Contains(delegateResult(result), "summary only") {
				return fmt.Errorf("the worker's final result did not reach the parent")
			}
			return nil
		}, Setup: func(dir string) error {
			return os.WriteFile(filepath.Join(dir, "inner.txt"), []byte("CHILD-INTERNAL"), 0o644)
		}}, multiOpts(anvil.Options{BuiltinTools: []string{"delegate"}}),
			delegateAnswer([]string{"out.txt"}, "read", func(round int) *llm.Response {
				if round == 0 {
					return toolAnswer("r", "read_file", `{"path":"inner.txt"}`)
				}
				return textAnswer("summary only")
			}, "done"), nil)),
	}
}

func complexInvariants() []evalCase {
	m := anvil.ModeComplex
	asked := 0
	one := func(files ...string) string { return roadmapText(roadmapItem{id: "a", goal: "act a", files: files}) }
	return []evalCase{
		named(m, "inv_permission_deny", invariantCase("inv_permission_deny", Task{UserMsg: "the denied write", Check: finishedOnly},
			complexOpts(anvil.Options{PermissionRules: []anvil.PermissionRule{{Effect: "deny", Tool: "write_file", Pattern: "*"}}}),
			complexAnswer(one("denied.txt"), func(_ string, round int) *llm.Response {
				if round == 0 {
					return toolAnswer("w", "write_file", `{"path":"denied.txt","content":"x"}`)
				}
				return nil
			}),
			func(dir string, _ Outcome) error { return absent(dir, "denied.txt") })),
		named(m, "inv_permission_ask", invariantCase("inv_permission_ask", Task{UserMsg: "the asked write", Check: finishedOnly},
			complexOpts(anvil.Options{Permission: func(context.Context, anvil.PermissionRequest) (anvil.PermissionDecision, error) {
				asked++
				return anvil.PermissionDecision{Allow: false, Message: CallbackRefusal}, nil
			}}),
			complexAnswer(one("asked.txt"), func(_ string, round int) *llm.Response {
				if round == 0 {
					return toolAnswer("w", "write_file", `{"path":"asked.txt","content":"x"}`)
				}
				return nil
			}),
			func(dir string, _ Outcome) error {
				if asked != 1 {
					return fmt.Errorf("the permission callback was called %d times for a Worker's write, want 1", asked)
				}
				return absent(dir, "asked.txt")
			})),
		// A looping Worker fails its item; the other item still finishes, and the run
		// reports the failure rather than hiding it.
		named(m, "inv_loop_guard", invariantCase("inv_loop_guard", Task{UserMsg: "two items, one loops", Check: func(_ string, result runtime.Result) error {
			if result.Status != "error" {
				return fmt.Errorf("a run with a failed item should end in error, got %s: %s", result.Status, result.Result)
			}
			if !strings.Contains(result.Result, "item b finished") || !strings.Contains(result.Result, "loop") {
				return fmt.Errorf("the other item did not finish, or the loop is not reported:\n%s", result.Result)
			}
			return nil
		}}, complexOpts(anvil.Options{}),
			complexAnswer(roadmapText(roadmapItem{id: "a", goal: "loop a", files: []string{"out.txt"}},
				roadmapItem{id: "b", goal: "finish b", files: []string{"out.txt"}}),
				func(goal string, _ int) *llm.Response {
					if goal == "loop a" {
						return toolAnswer("r", "read_file", `{"path":"missing.txt"}`)
					}
					return textAnswer("[met] done\nitem b finished")
				}), nil)),
		named(m, "inv_checkpoint_restore", invariantCase("inv_checkpoint_restore", Task{UserMsg: "edit both files", Check: finishedOnly, Setup: seedAB},
			complexOpts(anvil.Options{Checkpoints: &anvil.CheckpointOptions{}}),
			complexAnswer(roadmapText(roadmapItem{id: "a", goal: "edit a", files: []string{"a.txt"}},
				roadmapItem{id: "b", goal: "edit b", files: []string{"b.txt"}, deps: []string{"a"}}),
				func(goal string, round int) *llm.Response {
					if round > 0 {
						return nil
					}
					return editAB(map[string]string{"edit a": "a.txt", "edit b": "b.txt"}[goal])
				}), restoreRoot)),
		named(m, "inv_sandbox_escape", invariantCase("inv_sandbox_escape", Task{UserMsg: "read outside", Check: finishedOnly},
			complexOpts(anvil.Options{}),
			complexAnswer(one("out.txt"), func(_ string, round int) *llm.Response {
				if round == 0 {
					return toolAnswer("e", "read_file", `{"path":"../secret.txt"}`)
				}
				return nil
			}), nil)),
		complexPlanReadonly(),
		named(m, "inv_child_never_widens", invariantCase("inv_child_never_widens", Task{UserMsg: "a write by a read-only Worker", Check: finishedOnly},
			complexOpts(anvil.Options{Complex: anvil.ComplexOptions{Worker: anvil.Stage{Tools: []string{"read"}}}}),
			complexAnswer(one("widened.txt"), func(_ string, round int) *llm.Response {
				if round == 0 {
					return toolAnswer("w", "write_file", `{"path":"widened.txt","content":"x"}`)
				}
				return nil
			}),
			func(dir string, _ Outcome) error { return absent(dir, "widened.txt") })),
		// The stages' internal messages stay in the stages: the root's history is the
		// message and the rendered result, and the result carries each stage's final
		// answer only.
		named(m, "inv_child_result_only", invariantCase("inv_child_result_only", Task{UserMsg: "a read", Check: func(dir string, result runtime.Result) error {
			if err := finishedOnly(dir, result); err != nil {
				return err
			}
			for _, message := range result.Messages {
				if strings.Contains(message.Content, "CHILD-INTERNAL") {
					return fmt.Errorf("a Worker's internal message reached the root history: %+v", message)
				}
			}
			if !strings.Contains(result.Result, "summary only") {
				return fmt.Errorf("the Worker's final result is not in the run's result:\n%s", result.Result)
			}
			return nil
		}, Setup: func(dir string) error {
			return os.WriteFile(filepath.Join(dir, "inner.txt"), []byte("CHILD-INTERNAL"), 0o644)
		}}, complexOpts(anvil.Options{}),
			complexAnswer(one("out.txt"), func(_ string, round int) *llm.Response {
				if round == 0 {
					return toolAnswer("r", "read_file", `{"path":"inner.txt"}`)
				}
				return textAnswer("summary only")
			}), nil)),
	}
}

// complexPlanReadonly is the one invariant that is not a run: a Worker stage edits by
// definition, so Complex in plan mode is refused before anything is created.
func complexPlanReadonly() evalCase {
	return evalCase{name: "inv_plan_readonly@complex", invariant: true, gated: invariantGate, run: func(t *testing.T) (Score, error) {
		dir := t.TempDir()
		requests := 0
		completer := fixtureCompleter(func(context.Context, llm.Request) (*llm.Response, error) {
			requests++
			return textAnswer("unexpected"), nil
		})
		allowShell := false
		opts := complexOpts(anvil.Options{DisallowedTools: planTools, Model: anvil.Model{ID: "eval"}, APIKey: "eval-offline-unused-key",
			Local: anvil.LocalOptions{CWD: dir, Store: "jsonl", AllowShell: &allowShell}})
		agent, err := anvil.CreateForEval(t.Context(), opts, completer)
		if err != nil {
			return Score{}, err
		}
		defer agent.Close()
		_, sendErr := agent.Send(t.Context(), "plan only")
		if sendErr == nil || !strings.Contains(sendErr.Error(), "plan mode") {
			return Score{}, fmt.Errorf("Send in plan mode: %v, want a plan-mode refusal", sendErr)
		}
		if requests != 0 {
			return Score{}, fmt.Errorf("%d model requests for a refused run, want none", requests)
		}
		return Score{Completed: true}, nil
	}}
}
