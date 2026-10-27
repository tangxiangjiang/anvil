//go:build anvileval

// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package eval

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	anvil "github.com/anvil-dev/anvil"
	"github.com/anvil-dev/anvil/internal/llm"
	"github.com/anvil-dev/anvil/internal/runtime"
)

var updateBaseline = flag.Bool("update", false, "record the current scores in baseline.json (refused under CI=1)")

const baselinePath = "baseline.json"

var behaviourGate = []string{"completed", "steps"}
var invariantGate = []string{"completed", "refused", "loop_stops"}

// evalCase is one baseline entry: it runs itself and reports a Score.
type evalCase struct {
	name      string
	invariant bool
	gated     []string
	run       func(t *testing.T) (Score, error)
}

func behaviourCases() []evalCase {
	var cases []evalCase
	for _, task := range fixtures() {
		gated := behaviourGate
		if task.Name == "effort_rejected" {
			// A rejected request is a clean error, never retried: zero retries exactly.
			gated = append(append([]string(nil), behaviourGate...), "retries=")
		}
		if task.Name == "retry_429_then_ok" {
			// One 429 then a 200 is exactly one retry: more means a duplicate, fewer a lost one.
			gated = append(append([]string(nil), behaviourGate...), "retries=")
		}
		cases = append(cases, evalCase{name: task.Name, gated: gated, run: func(t *testing.T) (Score, error) {
			out, err := ExecuteScript(t.Context(), t.TempDir(), task)
			return out.Score, err
		}})
	}
	for _, fixture := range agentFixtures() {
		gated := behaviourGate
		if fixture.refused != 0 || fixture.loopStop != 0 {
			gated = append(append([]string(nil), behaviourGate...), "refused", "loop_stops")
		}
		if fixture.task.Name == "complex_two_items" {
			// Decide, analyze and two workers: four child agents exactly. A fifth is a stray
			// child, a third a Worker that never ran.
			gated = append(append([]string(nil), gated...), "agents=")
		}
		cases = append(cases, evalCase{name: fixture.task.Name, gated: gated, run: func(t *testing.T) (Score, error) {
			if len(fixture.options.Test.Command) != 0 {
				fixture.options.Test.Command = []string{prepareFixtureTest(t), "-test.count=1"}
			}
			out, err := ExecuteAgent(t.Context(), t.TempDir(), fixture.task, fixture.answer, fixture.options)
			return out.Score, err
		}})
	}
	cases = append(cases, providerCases()...)
	return cases
}

// providerCases replays each committed real-provider recording through the public
// Agent. A task with no committed recording has no baseline entry, so it is neither
// scored nor faked.
func providerCases() []evalCase {
	var cases []evalCase
	for _, fixture := range agentFixtures() {
		path := filepath.Join("recordings", fixture.task.Name+".jsonl")
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		gated := behaviourGate
		if fixture.refused != 0 {
			gated = append(append([]string(nil), behaviourGate...), "refused")
		}
		cases = append(cases, evalCase{name: "provider_" + fixture.task.Name, gated: gated, run: func(t *testing.T) (Score, error) {
			task := fixture.task
			task.Recording = path
			options := fixture.options
			cache := recordedPromptCache(t, data)
			options.Context.PromptCache = &cache
			if len(options.Test.Command) != 0 {
				verifier, err := BuildVerifier(t.Context(), t.TempDir())
				if err != nil {
					return Score{}, err
				}
				options.Test.Command = []string{verifier}
			}
			out, err := RunRecording(t.Context(), evalWorkdir(t, task.Name), task, options)
			return out.Score, err
		}})
	}
	return cases
}

// invariantCase runs a public-Agent task and then an extra check on what the
// run left behind. Each case guards one capability Anvil has that Codex lacks.
func invariantCase(name string, task Task, opts anvil.Options, answer fixtureCompleter, post func(dir string, out Outcome) error) evalCase {
	task.Name = name
	return evalCase{name: name, invariant: true, gated: invariantGate, run: func(t *testing.T) (Score, error) {
		dir := t.TempDir()
		out, err := ExecuteAgent(t.Context(), dir, task, answer, opts)
		if err == nil && post != nil {
			err = post(dir, out)
		}
		return out.Score, err
	}}
}

// modes are the three ways a session divides its work. Every invariant is proven under
// each (chapter 50 section 6): a higher mode adds structure, it never removes a capability.
var modes = []anvil.Mode{anvil.ModeSingle, anvil.ModeMulti, anvil.ModeComplex}

func modeName(m anvil.Mode) string {
	if m == anvil.ModeSingle {
		return "single"
	}
	return string(m)
}

func finishedOnly(_ string, result runtime.Result) error {
	if result.Status != "finished" {
		return fmt.Errorf("status=%s error=%s", result.Status, result.Err)
	}
	return nil
}

func absent(dir, name string) error {
	if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
		return fmt.Errorf("%s exists (stat error %v)", name, err)
	}
	return nil
}

func invariantCases() []evalCase {
	write := func(name string) fixtureCompleter {
		return func(_ context.Context, req llm.Request) (*llm.Response, error) {
			if _, round := userAndTools(req); round == 0 {
				return toolAnswer("w", "write_file", `{"path":"`+name+`","content":"x"}`), nil
			}
			return textAnswer("done"), nil
		}
	}
	askCalls := 0
	childAnswer := func(childRound0 func() *llm.Response) fixtureCompleter {
		return func(_ context.Context, req llm.Request) (*llm.Response, error) {
			user, round := userAndTools(req)
			if user == "child task" {
				if round == 0 {
					return childRound0(), nil
				}
				return textAnswer("summary only"), nil
			}
			if round == 0 {
				return toolAnswer("s", "spawn_agent", `{"prompt":"child task","description":"Child","tools":["read"]}`), nil
			}
			return textAnswer("parent done"), nil
		}
	}
	return []evalCase{
		invariantCase("inv_permission_deny", Task{UserMsg: "write the denied file", Check: finishedOnly},
			anvil.Options{BuiltinTools: []string{"write_file"},
				PermissionRules: []anvil.PermissionRule{{Effect: "deny", Tool: "write_file", Pattern: "*"}}},
			write("denied.txt"), func(dir string, _ Outcome) error { return absent(dir, "denied.txt") }),
		invariantCase("inv_permission_ask", Task{UserMsg: "write the asked file", Check: finishedOnly},
			anvil.Options{BuiltinTools: []string{"write_file"}, Permission: func(context.Context, anvil.PermissionRequest) (anvil.PermissionDecision, error) {
				askCalls++
				return anvil.PermissionDecision{Allow: false, Message: CallbackRefusal}, nil
			}},
			write("asked.txt"), func(dir string, _ Outcome) error {
				if askCalls != 1 {
					return fmt.Errorf("permission callback called %d times, want 1", askCalls)
				}
				return absent(dir, "asked.txt")
			}),
		invariantCase("inv_loop_guard", Task{UserMsg: "repeat a call", Check: func(_ string, result runtime.Result) error {
			if result.Code != runtime.CodeLoopDetected {
				return fmt.Errorf("expected loop guard, got %s: %s", result.Code, result.Err)
			}
			return nil
		}}, anvil.Options{BuiltinTools: []string{"read_file"}},
			func(context.Context, llm.Request) (*llm.Response, error) {
				return toolAnswer("r", "read_file", `{"path":"missing.txt"}`), nil
			}, nil),
		invariantCase("inv_checkpoint_restore", Task{UserMsg: "edit both files", Check: finishedOnly,
			Setup: func(dir string) error {
				for name, content := range map[string]string{"a.txt": "one", "b.txt": "two"} {
					if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
						return err
					}
				}
				return nil
			}},
			anvil.Options{BuiltinTools: []string{"edit_file"}, Checkpoints: &anvil.CheckpointOptions{}},
			func(_ context.Context, req llm.Request) (*llm.Response, error) {
				switch _, round := userAndTools(req); round {
				case 0:
					return toolAnswer("a", "edit_file", `{"path":"a.txt","old_string":"one","new_string":"ONE"}`), nil
				case 1:
					return toolAnswer("b", "edit_file", `{"path":"b.txt","old_string":"two","new_string":"TWO"}`), nil
				}
				return textAnswer("edited"), nil
			}, func(dir string, out Outcome) error {
				for name, want := range map[string]string{"a.txt": "ONE", "b.txt": "TWO"} {
					if data, _ := os.ReadFile(filepath.Join(dir, name)); string(data) != want {
						return fmt.Errorf("%s=%q before restore, want %q", name, data, want)
					}
				}
				runs, err := anvil.ListCheckpoints(dir, out.AgentID, anvil.CheckpointOptions{})
				if err != nil || len(runs) != 1 {
					return fmt.Errorf("checkpoint runs=%d err=%v", len(runs), err)
				}
				restored, err := anvil.RestoreCheckpoint(dir, out.AgentID, runs[0].RunID, anvil.CheckpointOptions{})
				if err != nil || len(restored) != 2 {
					return fmt.Errorf("restored=%+v err=%v", restored, err)
				}
				for _, file := range restored {
					if file.Skipped {
						return fmt.Errorf("restore skipped %s: %s", file.Path, file.Reason)
					}
				}
				for name, want := range map[string]string{"a.txt": "one", "b.txt": "two"} {
					if data, _ := os.ReadFile(filepath.Join(dir, name)); string(data) != want {
						return fmt.Errorf("%s=%q after restore, want %q", name, data, want)
					}
				}
				return nil
			}),
		invariantCase("inv_sandbox_escape", Task{UserMsg: "read outside", Check: finishedOnly},
			anvil.Options{BuiltinTools: []string{"read_file"}},
			func(_ context.Context, req llm.Request) (*llm.Response, error) {
				if _, round := userAndTools(req); round == 0 {
					return toolAnswer("e", "read_file", `{"path":"../secret.txt"}`), nil
				}
				return textAnswer("blocked"), nil
			}, nil),
		invariantCase("inv_plan_readonly", Task{UserMsg: "write in a read-only tool set", Check: func(dir string, result runtime.Result) error {
			if err := finishedOnly(dir, result); err != nil {
				return err
			}
			for _, message := range result.Messages {
				if message.Role == llm.RoleTool && strings.Contains(message.Content, "unknown tool: write_file") {
					return nil
				}
			}
			return fmt.Errorf("write_file was not reported as an unknown tool")
		}}, anvil.Options{BuiltinTools: []string{"read_file"}}, write("plan.txt"),
			func(dir string, _ Outcome) error { return absent(dir, "plan.txt") }),
		invariantCase("inv_child_never_widens", Task{UserMsg: "delegate a write", Check: finishedOnly},
			anvil.Options{BuiltinTools: []string{"read", "write_file", "spawn_agent"}, Subagents: anvil.SubagentOptions{Enabled: true}},
			childAnswer(func() *llm.Response {
				return toolAnswer("cw", "write_file", `{"path":"widened.txt","content":"x"}`)
			}), func(dir string, _ Outcome) error { return absent(dir, "widened.txt") }),
		invariantCase("inv_child_result_only", Task{UserMsg: "delegate a read", Check: func(dir string, result runtime.Result) error {
			if err := finishedOnly(dir, result); err != nil {
				return err
			}
			sawResult := false
			for _, message := range result.Messages {
				if strings.Contains(message.Content, "CHILD-INTERNAL") {
					return fmt.Errorf("a child message reached the parent history: %+v", message)
				}
				if message.Name == "spawn_agent" && strings.Contains(message.Content, "summary only") {
					sawResult = true
				}
			}
			if !sawResult {
				return fmt.Errorf("the child's final result did not reach the parent")
			}
			return nil
		}, Setup: func(dir string) error {
			return os.WriteFile(filepath.Join(dir, "inner.txt"), []byte("CHILD-INTERNAL"), 0o644)
		}}, anvil.Options{BuiltinTools: []string{"read", "spawn_agent"}, Subagents: anvil.SubagentOptions{Enabled: true}},
			childAnswer(func() *llm.Response {
				return toolAnswer("cr", "read_file", `{"path":"inner.txt"}`)
			}), nil),
	}
}

var baselineMu sync.Mutex

// compareCases checks every case against the baseline, or merges its scores
// into the file under -update. Merging keeps a partial run (-run
// TestInvariants) from dropping other entries.
func compareCases(t *testing.T, cases []evalCase, full bool) {
	t.Helper()
	baselineMu.Lock()
	defer baselineMu.Unlock()
	if *updateBaseline && os.Getenv("CI") == "1" {
		t.Fatal("-update is refused under CI=1: a baseline changes only by a deliberate local act")
	}
	base, err := LoadBaseline(baselinePath)
	if err != nil {
		if !*updateBaseline {
			t.Fatal(err)
		}
		base = Baseline{}
	}
	var names []string
	for _, c := range cases {
		names = append(names, c.name)
		t.Run(c.name, func(t *testing.T) {
			score, err := c.run(t)
			if err != nil {
				t.Fatalf("score=%+v: %v", score, err)
			}
			t.Logf("%s score=%+v", c.name, score)
			if *updateBaseline {
				base[c.name] = BaselineEntry{Gated: c.gated, Score: score}
				return
			}
			notes, err := CheckTask(base, c.name, c.gated, score)
			for _, note := range notes {
				t.Log("note:", note)
			}
			if err != nil {
				t.Error(err)
			}
		})
	}
	if full {
		if err := Stale(base, names); err != nil {
			if *updateBaseline {
				for name := range base {
					if !contains(names, name) {
						delete(base, name)
					}
				}
			} else {
				t.Error(err)
			}
		}
	}
	if *updateBaseline && !t.Failed() {
		if err := WriteBaseline(baselinePath, base); err != nil {
			t.Fatal(err)
		}
	}
}

func contains(list []string, name string) bool {
	for _, item := range list {
		if item == name {
			return true
		}
	}
	return false
}

// TestBaseline compares every behaviour and invariant task with baseline.json.
func TestBaseline(t *testing.T) {
	// Every invariant in every mode: a baseline entry with no task is stale, and the
	// mode-keyed entries are tasks like the rest. A partial --mode run is a TestInvariants
	// run, which does not check for stale entries.
	t.Setenv("ANVIL_EVAL_MODES", "")
	compareCases(t, append(behaviourCases(), invariantCasesForModes(t, invariantCases())...), true)
}

// TestInvariants is the suite every later phase's gate runs; it must stay at
// 100% with refused and loop_stops equal to the baseline.
func TestInvariants(t *testing.T) {
	single := invariantCases()
	if len(single) != 8 {
		t.Fatalf("invariants suite has %d tasks, want the eight of chapter 47 section 6.1", len(single))
	}
	cases := invariantCasesForModes(t, single)
	compareCases(t, cases, false)
}

// invariantModes reads ANVIL_EVAL_MODES (set by scripts/eval.sh --mode), a comma list of
// single, multi and complex. Unset means all three.
func invariantModes(t *testing.T) []anvil.Mode {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("ANVIL_EVAL_MODES"))
	if raw == "" {
		return modes
	}
	var out []anvil.Mode
	for _, name := range strings.Split(raw, ",") {
		switch strings.TrimSpace(name) {
		case "single":
			out = append(out, anvil.ModeSingle)
		case "multi":
			out = append(out, anvil.ModeMulti)
		case "complex":
			out = append(out, anvil.ModeComplex)
		default:
			t.Fatalf("unknown mode %q in ANVIL_EVAL_MODES (single, multi, complex)", name)
		}
	}
	return out
}

// invariantCasesForModes is the eight invariants once per selected mode: 24 in all. A
// Single case keeps the name it has always had in the baseline; the others are
// task@mode.
func invariantCasesForModes(t *testing.T, single []evalCase) []evalCase {
	t.Helper()
	var out []evalCase
	for _, mode := range invariantModes(t) {
		switch mode {
		case anvil.ModeSingle:
			out = append(out, single...)
		case anvil.ModeMulti:
			out = append(out, multiInvariants()...)
		case anvil.ModeComplex:
			out = append(out, complexInvariants()...)
		}
	}
	return out
}
