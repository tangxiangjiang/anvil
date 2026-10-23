//go:build anvileval

// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	anvil "github.com/anvil-dev/anvil"
	"github.com/anvil-dev/anvil/internal/llm"
	"github.com/anvil-dev/anvil/internal/runtime"
)

type fixtureCompleter func(context.Context, llm.Request) (*llm.Response, error)

func (f fixtureCompleter) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	return f(ctx, req)
}

type agentFixture struct {
	task     Task
	options  anvil.Options
	answer   fixtureCompleter
	steps    int
	refused  int
	loopStop int
}

func textAnswer(text string) *llm.Response {
	return &llm.Response{Message: llm.Message{Role: llm.RoleAssistant, Content: text}}
}

func toolAnswer(id, name, args string) *llm.Response {
	return &llm.Response{Message: llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{
		ID: id, Name: name, Args: []byte(args),
	}}}}
}

func userAndTools(req llm.Request) (string, int) {
	tools := 0
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == llm.RoleUser {
			return req.Messages[i].Content, tools
		}
		if req.Messages[i].Role == llm.RoleTool {
			tools++
		}
	}
	return "", tools
}

func agentFixtures() []agentFixture {
	finished := func(_ string, result runtime.Result) error {
		if result.Status != "finished" {
			return fmt.Errorf("status=%s error=%s", result.Status, result.Err)
		}
		return nil
	}
	writeFiles := func(dir string, files map[string]string) error {
		for name, content := range files {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
				return err
			}
		}
		return nil
	}
	return []agentFixture{
		{
			task: Task{Name: "two_file_edit_test", UserMsg: "update both functions and run tests", Setup: func(dir string) error {
				return writeFiles(dir, map[string]string{
					"go.mod":         "module evalfixture\n\ngo 1.26.4\n",
					"a.go":           "package fixture\nfunc A() int { return 1 }\n",
					"b.go":           "package fixture\nfunc B() int { return 2 }\n",
					"values_test.go": "package fixture\nimport \"testing\"\nfunc TestValues(t *testing.T) { if A()!=3 || B()!=4 { t.Fatal(A(), B()) } }\n",
				})
			}, Check: func(dir string, result runtime.Result) error {
				if err := finished(dir, result); err != nil {
					return err
				}
				for _, message := range result.Messages {
					if message.Role == llm.RoleTool && message.Name == "run_tests" && strings.Contains(message.Content, "exit=0") {
						return nil
					}
				}
				return fmt.Errorf("configured test command did not pass: %+v", result.Messages)
			}}, options: anvil.Options{BuiltinTools: []string{"edit_file", "run_tests"},
				Test: anvil.TestOptions{Command: []string{"fixture-test"}, Timeout: 20 * time.Second}},
			answer: func(_ context.Context, req llm.Request) (*llm.Response, error) {
				_, round := userAndTools(req)
				switch round {
				case 0:
					return toolAnswer("edit-a", "edit_file", `{"path":"a.go","old_string":"return 1","new_string":"return 3"}`), nil
				case 1:
					return toolAnswer("edit-b", "edit_file", `{"path":"b.go","old_string":"return 2","new_string":"return 4"}`), nil
				case 2:
					return toolAnswer("test", "run_tests", `{}`), nil
				default:
					return textAnswer("updated and tested"), nil
				}
			}, steps: 4,
		},
		{
			task: Task{Name: "spawn_read_report", UserMsg: "delegate a read", Setup: func(dir string) error {
				return writeFiles(dir, map[string]string{"note.txt": "child marker"})
			}, Check: func(dir string, result runtime.Result) error {
				if err := finished(dir, result); err != nil {
					return err
				}
				for _, message := range result.Messages {
					if message.Name == "spawn_agent" && strings.Contains(message.Content, "child marker") {
						return nil
					}
				}
				return fmt.Errorf("child did not read and report")
			}}, options: anvil.Options{BuiltinTools: []string{"read", "spawn_agent"}, Subagents: anvil.SubagentOptions{Enabled: true}},
			answer: func(_ context.Context, req llm.Request) (*llm.Response, error) {
				user, round := userAndTools(req)
				if user == "read note.txt and report" {
					if round == 0 {
						return toolAnswer("child-read", "read_file", `{"path":"note.txt"}`), nil
					}
					return textAnswer("child marker"), nil
				}
				if round == 0 {
					return toolAnswer("spawn", "spawn_agent", `{"prompt":"read note.txt and report","description":"Reader","tools":["read"]}`), nil
				}
				return textAnswer("child reported"), nil
			}, steps: 2,
		},
		{
			task: Task{Name: "workflow_two_steps", UserMsg: "Use the run_workflow tool with max_parallel 1 and exactly two steps. Step one lists the file names in the workspace root with a glob pattern; step two depends on it and summarizes that list. Do not read directories with read_file.", Check: func(dir string, result runtime.Result) error {
				if err := finished(dir, result); err != nil {
					return err
				}
				for _, message := range result.Messages {
					if message.Name == "run_workflow" && strings.Contains(message.Content, ": finished (2 steps)") {
						return nil
					}
				}
				return fmt.Errorf("workflow did not finish both steps")
			}}, options: anvil.Options{BuiltinTools: []string{"read", "run_workflow"}, Subagents: anvil.SubagentOptions{Enabled: true}},
			answer: func(_ context.Context, req llm.Request) (*llm.Response, error) {
				user, round := userAndTools(req)
				switch user {
				case "scan":
					return textAnswer("found input"), nil
				case "check found input":
					return textAnswer("checked"), nil
				}
				if round == 0 {
					return toolAnswer("workflow", "run_workflow", `{"name":"pair","max_parallel":1,"steps":[{"id":"scan","prompt":"scan","tools":["read"]},{"id":"check","depends_on":["scan"],"prompt":"check {{scan.result}}","tools":["read"]}]}`), nil
				}
				return textAnswer("workflow done"), nil
			}, steps: 2,
		},
		{
			task: Task{Name: "permission_deny_no_callback", UserMsg: "attempt a denied write", Check: func(dir string, result runtime.Result) error {
				if err := finished(dir, result); err != nil {
					return err
				}
				if _, err := os.Stat(filepath.Join(dir, "blocked.txt")); !os.IsNotExist(err) {
					return fmt.Errorf("denied write created a file: %v", err)
				}
				return nil
			}}, options: anvil.Options{BuiltinTools: []string{"write_file"},
				PermissionRules: []anvil.PermissionRule{{Effect: "deny", Tool: "write_file", Pattern: "*"}}},
			answer: func(_ context.Context, req llm.Request) (*llm.Response, error) {
				_, round := userAndTools(req)
				if round == 0 {
					return toolAnswer("write", "write_file", `{"path":"blocked.txt","content":"blocked"}`), nil
				}
				return textAnswer("denied"), nil
			}, steps: 2, refused: 1,
		},
		{
			task: Task{Name: "loop_guard", UserMsg: "Read the file missing.txt six times in a row. Every call must use exactly the same arguments; do not change the path, do not try other tools, and do not stop early.", MaxSteps: 8,
				Check: func(_ string, result runtime.Result) error {
					if result.Code != runtime.CodeLoopDetected {
						return fmt.Errorf("expected loop guard, got %s: %s", result.Code, result.Err)
					}
					return nil
				}}, options: anvil.Options{BuiltinTools: []string{"read_file"}},
			answer: func(_ context.Context, _ llm.Request) (*llm.Response, error) {
				return toolAnswer("repeat", "read_file", `{"path":"missing.txt"}`), nil
			}, steps: 4, loopStop: 1,
		},
	}
}

// prepareFixtureTest builds a controlled verification executable before the
// agent runs. It reads the edited files at execution time, and needs no build
// cache variables in the SDK's deliberately scrubbed command environment.
func prepareFixtureTest(t *testing.T) string {
	t.Helper()
	// Not t.TempDir: Windows can hold a just-executed binary briefly (scanners),
	// and a failed cleanup would fail a test whose behaviour passed.
	dir, err := os.MkdirTemp("", "anvil-eval-check-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for attempt := 0; attempt < 20; attempt++ {
			if os.RemoveAll(dir) == nil {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Logf("could not remove %s", dir)
	})
	source := filepath.Join(dir, "check_test.go")
	body := `package check
import ("os"; "strings"; "testing")
func TestEditedFiles(t *testing.T) {
	for name, want := range map[string]string{"a.go":"return 3", "b.go":"return 4"} {
		data, err := os.ReadFile(name)
		if err != nil || !strings.Contains(string(data), want) { t.Fatalf("%s: %s, %v", name, data, err) }
	}
}
`
	if err := os.WriteFile(source, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "check.test.exe")
	cmd := exec.CommandContext(t.Context(), "go", "test", "-c", "-o", binary, source)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fixture test: %v\n%s", err, output)
	}
	return binary
}

// These records are generated by a synthetic completer, never a real provider.
// Record and replay use different workspaces to test actual request stability.
func TestReplayAgentScenarios(t *testing.T) {
	for _, fixture := range agentFixtures() {
		t.Run(fixture.task.Name, func(t *testing.T) {
			if len(fixture.options.Test.Command) != 0 {
				fixture.options.Test.Command = []string{prepareFixtureTest(t), "-test.count=1"}
			}
			path := filepath.Join(t.TempDir(), fixture.task.Name+".live.jsonl")
			recorder, err := NewRecorder(path, fixture.answer, "synthetic")
			if err != nil {
				t.Fatal(err)
			}
			original, runErr := ExecuteAgent(t.Context(), t.TempDir(), fixture.task, recorder, fixture.options)
			closeErr := recorder.Close()
			if runErr != nil || closeErr != nil {
				t.Fatalf("synthetic capture: %v close=%v score=%+v", runErr, closeErr, original.Score)
			}
			task := fixture.task
			task.Recording = path
			replayed, err := RunRecording(t.Context(), t.TempDir(), task, fixture.options)
			if err != nil {
				t.Fatalf("replay: %v score=%+v", err, replayed.Score)
			}
			if !replayed.Score.Completed || replayed.Score.Steps != fixture.steps ||
				replayed.Score.Refused != fixture.refused || replayed.Score.LoopStops != fixture.loopStop {
				t.Fatalf("unexpected score=%+v", replayed.Score)
			}
			// Request bytes retain real workspace/ID lengths and are a cost proxy.
			a, b := original.Score, replayed.Score
			a.RequestBytes, b.RequestBytes = 0, 0
			if a != b || replayed.Score.RequestBytes == 0 {
				t.Fatalf("capture=%+v replay=%+v", original.Score, replayed.Score)
			}
			t.Logf("%s score=%+v (synthetic replay)", task.Name, replayed.Score)
		})
	}
}

func TestReplayAgentAskCallbackRefusal(t *testing.T) {
	calls := 0
	opts := anvil.Options{BuiltinTools: []string{"write_file"}, Permission: func(context.Context, anvil.PermissionRequest) (anvil.PermissionDecision, error) {
		calls++
		return anvil.PermissionDecision{Allow: false, Message: CallbackRefusal}, nil
	}}
	task := Task{Name: "ask", UserMsg: "write"}
	script := NewScript(Replies([]llm.Message{
		toolAnswer("write", "write_file", `{"path":"no.txt","content":"no"}`).Message,
		textAnswer("refused").Message,
	}))
	out, err := ExecuteAgent(t.Context(), t.TempDir(), task, script, opts)
	if err != nil || calls != 1 || out.Score.Refused != 1 || out.Score.ToolErrors != 1 {
		t.Fatalf("calls=%d score=%+v error=%v", calls, out.Score, err)
	}
}

// TestReplayProviderRecordings replays committed recordings made against a real
// provider (recordings/<task>.jsonl) through the public Agent, in a fresh
// workspace. A task with no committed recording is skipped, not faked.
func TestReplayProviderRecordings(t *testing.T) {
	for _, fixture := range agentFixtures() {
		path := filepath.Join("recordings", fixture.task.Name+".jsonl")
		t.Run(fixture.task.Name, func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Skip("no genuine provider recording committed yet")
			}
			// A recording whose prompts embed the fixed workspace path replays only where
			// that literal path exists (see evalRoot).
			escaped := strings.ReplaceAll(evalRoot(), `\`, `\\`)
			if strings.Contains(string(data), "anvil-eval") && !strings.Contains(string(data), escaped) {
				t.Skip("recording embeds the workspace path of another OS")
			}
			task := fixture.task
			task.Recording = path
			if len(fixture.options.Test.Command) != 0 {
				// Recorded through the live tier's fixed verifier, so replay uses the same program.
				verifier, err := BuildVerifier(t.Context(), t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				fixture.options.Test.Command = []string{verifier}
			}
			// PromptCache is part of the request, and which value a recording has depends on
			// the provider it was made with. Read it from the recording rather than guess.
			cache := recordedPromptCache(t, data)
			fixture.options.Context.PromptCache = &cache
			out, err := RunRecording(t.Context(), evalWorkdir(t, task.Name), task, fixture.options)
			if err != nil {
				t.Fatalf("replay: %v score=%+v", err, out.Score)
			}
			t.Logf("%s score=%+v (provider recording)", task.Name, out.Score)
		})
	}
}

func recordedPromptCache(t *testing.T, data []byte) bool {
	t.Helper()
	first, _, _ := strings.Cut(string(data), "\n")
	var record struct {
		Request struct{ PromptCache bool }
	}
	if err := json.Unmarshal([]byte(first), &record); err != nil {
		t.Fatalf("read recording: %v", err)
	}
	return record.Request.PromptCache
}
