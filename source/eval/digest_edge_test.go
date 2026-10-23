// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package eval

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/anvil-dev/anvil/internal/llm"
)

func TestDigestTemporaryRootsArePortable(t *testing.T) {
	request := func(root string) llm.Request {
		return llm.Request{Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: systemIntro + root + "\n"},
			{Role: llm.RoleTool, Name: "read_file", Content: root + `\a.txt`},
		}}
	}
	a := request(`C:\Users\Alice\AppData\Local\Temp\fixture`)
	b := request(`C:\Users\Bob\AppData\Local\Temp\fixture`)
	if RequestDigest(a) != RequestDigest(b) {
		t.Fatal("standard temporary roots depend on the recording machine")
	}
	for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(key, `C:\Users\Alice\AppData\Local\Temp`)
	}
	before := RequestDigest(a)
	for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(key, `C:\Users\Bob\AppData\Local\Temp`)
	}
	if before != RequestDigest(a) {
		t.Fatal("loading an unchanged request depends on local temp configuration")
	}
}

func TestDigestAdjacentWorkspaceSubpathsRemainSensitive(t *testing.T) {
	request := func(root string) llm.Request {
		return llm.Request{Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: systemIntro + root + "\n"},
			{Role: llm.RoleTool, Name: "read_file", Content: root + root + "/file"},
		}}
	}
	if RequestDigest(request("/tmp/a")) == RequestDigest(request("/tmp/b")) {
		t.Fatal("workspace-relative tmp/a and tmp/b paths collapsed")
	}
}

func TestDigestFailedChildMetadata(t *testing.T) {
	for _, content := range []string{
		"agent-1111111111111111 failed: maximum steps reached",
		"agent-1111111111111111 was cancelled: the run stopped",
		"workflow w: failed (3 steps, 1 failed, 1 skipped)\na  failed     agent-1111111111111111  error\nb  skipped    —  dependency a failed\nc  finished   agent-2222222222222222  done\n",
	} {
		name := "spawn_agent"
		if strings.HasPrefix(content, "workflow ") {
			name = "run_workflow"
		}
		a := llm.Request{Messages: []llm.Message{{Role: llm.RoleTool, Name: name, Content: content}}}
		b := recordingCopy(t, a)
		b.Messages[0].Content = strings.NewReplacer("1111111111111111", "3333333333333333", "2222222222222222", "4444444444444444").Replace(content)
		if RequestDigest(a) != RequestDigest(b) {
			t.Fatalf("child metadata remained volatile: %s", content)
		}
		b.Messages[0].Content += " changed reason"
		if RequestDigest(a) == RequestDigest(b) {
			t.Fatal("child failure reason was normalized away")
		}
	}
}

func TestDigestToolNameRecoveryUsesPriorInvocation(t *testing.T) {
	a := llm.Request{Messages: []llm.Message{
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "x", Name: "read_file"}}},
		{Role: llm.RoleTool, ToolCallID: "x", Content: "ok\tpkg\t0.001s"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "x", Name: "run_tests"}}},
		{Role: llm.RoleTool, ToolCallID: "x", Content: "ok\tpkg\t0.004s"},
	}}
	b := recordingCopy(t, a)
	b.Messages[1].Content = "ok\tpkg\t0.002s"
	if RequestDigest(a) == RequestDigest(b) {
		t.Fatal("later invocation changed the meaning of earlier file content")
	}
	b = recordingCopy(t, a)
	b.Messages[3].Content = "ok\tpkg\t0.008s"
	if RequestDigest(a) != RequestDigest(b) {
		t.Fatal("the later run_tests duration remained volatile")
	}
}

func TestDigestGoFailedPackageListDuration(t *testing.T) {
	request := func(name, pkg, seconds string) llm.Request {
		return llm.Request{Messages: []llm.Message{{Role: llm.RoleTool, Name: name,
			Content: "tests: failed exit=1\nfailed:\n  - " + pkg + "\t" + seconds + "\nlogTail:\nFAIL\t" + pkg + "\t" + seconds}}}
	}
	a, b := request("run_tests", "pkg", "0.001s"), request("run_tests", "pkg", "0.002s")
	if RequestDigest(a) != RequestDigest(b) {
		t.Fatal("duplicated Go package duration remained volatile")
	}
	if RequestDigest(a) == RequestDigest(request("run_tests", "other", "0.001s")) {
		t.Fatal("failed package identity was normalized away")
	}
	if RequestDigest(request("read_file", "pkg", "0.001s")) == RequestDigest(request("read_file", "pkg", "0.002s")) {
		t.Fatal("file text was mistaken for a generated failure list")
	}
	b = recordingCopy(t, a)
	b.Messages[0].Content = strings.Replace(b.Messages[0].Content, "  - pkg\t0.001s", "  - TestMeaningful\t0.002s", 1)
	if RequestDigest(a) == RequestDigest(b) {
		t.Fatal("a named test failure was normalized away")
	}
}

func TestRecorderAcceptsIdenticalConcurrentResponses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identical.live.jsonl")
	recorder, err := NewRecorder(path, recordingCompleterFunc(func(context.Context, llm.Request) (*llm.Response, error) {
		response := recordingResponse()
		return &response, nil
	}), "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	defer recorder.Close()
	req := llm.Request{Messages: []llm.Message{{Role: llm.RoleUser, Content: "same"}}}
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Go(func() {
			if _, err := recorder.Complete(t.Context(), req); err != nil {
				t.Errorf("capture: %v", err)
			}
		})
	}
	wg.Wait()
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	replay, err := LoadReplay(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := replay.Complete(t.Context(), req); err != nil {
			t.Fatal(err)
		}
	}
	if err := replay.Verify(); err != nil {
		t.Fatal(err)
	}
}

func TestRecorderRejectsConflictingConcurrentResponses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conflict.live.jsonl")
	var calls atomic.Int32
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	upstream := recordingCompleterFunc(func(context.Context, llm.Request) (*llm.Response, error) {
		n := calls.Add(1)
		entered <- struct{}{}
		<-release
		return &llm.Response{Message: llm.Message{Role: llm.RoleAssistant, Content: fmt.Sprint(n)}}, nil
	})
	recorder, err := NewRecorder(path, upstream, "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	defer recorder.Close()
	req := llm.Request{Messages: []llm.Message{{Role: llm.RoleUser, Content: "same"}}}
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Go(func() {
			_, err := recorder.Complete(t.Context(), req)
			results <- err
		})
	}
	<-entered
	<-entered
	close(release)
	wg.Wait()
	close(results)
	failures := 0
	for err := range results {
		if err != nil {
			failures++
			if !strings.Contains(err.Error(), "conflicting") {
				t.Fatalf("unexpected rejection: %v", err)
			}
		}
	}
	if failures != 1 || recorder.Err() == nil || recorder.Close() == nil {
		t.Fatalf("ambiguous capture succeeded: failures=%d sticky=%v", failures, recorder.Err())
	}
	replay, err := LoadReplay(path)
	if err != nil {
		t.Fatalf("accepted portion is not replayable: %v", err)
	}
	if _, err := replay.Complete(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	if err := replay.Verify(); err != nil {
		t.Fatal(err)
	}
}
