// Package eval holds offline regression fixtures for the agent tool loop.
// Live gateway smoke tests belong in cmd/anvil or examples — not here.
//
// Run: go test ./eval
package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anvil-dev/anvil/internal/llm"
	"github.com/anvil-dev/anvil/internal/runtime"
	"github.com/anvil-dev/anvil/internal/tools"
)

// Task is an offline eval fixture driven by a scripted Completer.
type Task struct {
	Name     string
	UserMsg  string
	Setup    func(dir string) error
	Script   []llm.Message
	MaxSteps int // 0 = 8 for normal fixtures
	Check    func(dir string, res runtime.Result) error
}

type scriptCompleter struct {
	replies []llm.Message
	n       int
}

func (s *scriptCompleter) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	if s.n >= len(s.replies) {
		return &llm.Response{Message: llm.Message{Role: llm.RoleAssistant, Content: "end"}}, nil
	}
	m := s.replies[s.n]
	s.n++
	return &llm.Response{Message: m}, nil
}

// Run executes a task in an isolated temp directory.
func Run(t *testing.T, task Task) {
	t.Helper()
	dir := t.TempDir()
	if task.Setup != nil {
		if err := task.Setup(dir); err != nil {
			t.Fatalf("setup: %v", err)
		}
	}
	reg := tools.NewRegistry(dir, false)
	maxSteps := task.MaxSteps
	if maxSteps <= 0 {
		maxSteps = 8
	}
	res := runtime.Loop(context.Background(), &scriptCompleter{replies: task.Script}, reg, runtime.Config{
		CWD:      dir,
		Model:    "eval",
		UserMsg:  task.UserMsg,
		MaxSteps: maxSteps,
	}, nil)
	if task.Check != nil {
		if err := task.Check(dir, res); err != nil {
			t.Fatalf("%s: %v\nresult=%+v", task.Name, err, res)
		}
	}
}

func TestFixtures(t *testing.T) {
	for _, task := range fixtures() {
		t.Run(task.Name, func(t *testing.T) { Run(t, task) })
	}
}

func fixtures() []Task {
	return []Task{
		{
			Name:    "edit_file_unique",
			UserMsg: "rename hello",
			Setup: func(dir string) error {
				return os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello world"), 0o644)
			},
			Script: []llm.Message{
				{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{
					ID: "1", Name: "edit_file",
					Args: json.RawMessage(`{"path":"a.txt","old_string":"world","new_string":"anvil"}`),
				}}},
				{Role: llm.RoleAssistant, Content: "done"},
			},
			Check: func(dir string, res runtime.Result) error {
				if res.Status != "finished" {
					return fmt.Errorf("status=%s", res.Status)
				}
				data, err := os.ReadFile(filepath.Join(dir, "a.txt"))
				if err != nil {
					return err
				}
				if string(data) != "hello anvil" {
					return fmt.Errorf("content=%q", data)
				}
				return nil
			},
		},
		{
			Name:    "sandbox_escape_denied",
			UserMsg: "read outside",
			Script: []llm.Message{
				{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{
					ID: "1", Name: "read_file",
					Args: json.RawMessage(`{"path":"../secret.txt"}`),
				}}},
				{Role: llm.RoleAssistant, Content: "blocked"},
			},
			Check: func(dir string, res runtime.Result) error {
				if res.Status != "finished" {
					return fmt.Errorf("status=%s", res.Status)
				}
				found := false
				for _, m := range res.Messages {
					if m.Role == llm.RoleTool && strings.Contains(m.Content, "escapes") {
						found = true
					}
				}
				if !found {
					return fmt.Errorf("expected sandbox escape error in tool result")
				}
				return nil
			},
		},
		{
			Name:    "apply_patch_add",
			UserMsg: "add file",
			Script: []llm.Message{
				{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{
					ID: "1", Name: "apply_patch",
					Args: mustJSON(map[string]string{
						"patch": "*** Begin Patch\n*** Add File: n.txt\n+hi\n*** End Patch\n",
					}),
				}}},
				{Role: llm.RoleAssistant, Content: "added"},
			},
			Check: func(dir string, res runtime.Result) error {
				data, err := os.ReadFile(filepath.Join(dir, "n.txt"))
				if err != nil {
					return err
				}
				if !strings.Contains(string(data), "hi") {
					return fmt.Errorf("%q", data)
				}
				return nil
			},
		},
		{
			Name:    "multi_file_edit_patch",
			UserMsg: "update two files",
			Setup: func(dir string) error {
				if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("alpha"), 0o644); err != nil {
					return err
				}
				return os.WriteFile(filepath.Join(dir, "b.txt"), []byte("beta"), 0o644)
			},
			Script: []llm.Message{
				{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{
					ID: "1", Name: "edit_file",
					Args: json.RawMessage(`{"path":"a.txt","old_string":"alpha","new_string":"ALPHA"}`),
				}}},
				{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{
					ID: "2", Name: "apply_patch",
					Args: mustJSON(map[string]string{
						"patch": "*** Begin Patch\n*** Update File: b.txt\n@@\n-beta\n+BETA\n*** End Patch\n",
					}),
				}}},
				{Role: llm.RoleAssistant, Content: "both updated"},
			},
			Check: func(dir string, res runtime.Result) error {
				if res.Status != "finished" {
					return fmt.Errorf("status=%s err=%s", res.Status, res.Err)
				}
				a, err := os.ReadFile(filepath.Join(dir, "a.txt"))
				if err != nil {
					return err
				}
				b, err := os.ReadFile(filepath.Join(dir, "b.txt"))
				if err != nil {
					return err
				}
				if string(a) != "ALPHA" {
					return fmt.Errorf("a=%q", a)
				}
				if !strings.Contains(string(b), "BETA") {
					return fmt.Errorf("b=%q", b)
				}
				return nil
			},
		},
		{
			Name:     "max_steps_exceeded",
			UserMsg:  "never finish",
			MaxSteps: 2,
			Setup: func(dir string) error {
				return os.WriteFile(filepath.Join(dir, "note.txt"), []byte("x"), 0o644)
			},
			Script: []llm.Message{
				{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{
					ID: "1", Name: "read_file",
					Args: json.RawMessage(`{"path":"note.txt"}`),
				}}},
				{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{
					ID: "2", Name: "read_file",
					Args: json.RawMessage(`{"path":"note.txt"}`),
				}}},
				{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{
					ID: "3", Name: "read_file",
					Args: json.RawMessage(`{"path":"note.txt"}`),
				}}},
			},
			Check: func(dir string, res runtime.Result) error {
				_ = dir
				if res.Status != "error" {
					return fmt.Errorf("status=%s", res.Status)
				}
				if !runtime.IsMaxStepsExceeded(res.Err) {
					return fmt.Errorf("expected max steps, got %q", res.Err)
				}
				return nil
			},
		},
	}
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
