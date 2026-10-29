// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package eval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anvil-dev/anvil/internal/llm"
	"github.com/anvil-dev/anvil/internal/prompt"
)

type recordingCompleterFunc func(context.Context, llm.Request) (*llm.Response, error)

func (f recordingCompleterFunc) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	return f(ctx, req)
}

func recordingRequest(cwd string) llm.Request {
	return llm.Request{
		Model: "recorded-model",
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: prompt.System(cwd) + "\n\n# Session notes\n- opened at 2026-03-01T12:01:02Z\n"},
			{Role: llm.RoleUser, Content: "inspect a.txt"},
			{Role: llm.RoleTool, Name: "read_file", Content: fmt.Sprintf("read %s/a.txt", cwd)},
			{Role: llm.RoleTool, Name: "spawn_agent", Content: "agent-0123456789abcdef finished in 2 steps.\nok"},
			{Role: llm.RoleTool, Name: "run_workflow", Content: "workflow pair: finished (2 steps)\nfirst  finished   agent-fedcba9876543210  done\nsecond  failed     agent-0123456789abcdef  error"},
		},
		Tools: []llm.ToolDef{
			{Name: "z_tool", Description: "inspect", Parameters: json.RawMessage(`{"type":"object","properties":{"file":{"type":"string"}}}`)},
			{Name: "a_tool", Description: "edit", Parameters: json.RawMessage(`{"type":"object"}`)},
		},
		PromptCache: true,
	}
}

func recordingCopy(t *testing.T, req llm.Request) llm.Request {
	t.Helper()
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var copied llm.Request
	if err := json.Unmarshal(b, &copied); err != nil {
		t.Fatal(err)
	}
	return copied
}

func recordingResponse() llm.Response {
	return llm.Response{
		Message: llm.Message{Role: llm.RoleAssistant, Content: "done\nnext line", Parts: []llm.ContentPart{{Type: "image", Data: []byte{1, 2, 3}}}, ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "read", Args: json.RawMessage(`{"file":"a.txt"}`)}}},
		Usage:   &llm.Usage{InputTokens: 7, OutputTokens: 3, TotalTokens: 10, CachedTokens: 2, CacheWriteTokens: 1},
	}
}

func recordingLine(t *testing.T, req llm.Request, response llm.Response) string {
	t.Helper()
	b, err := json.Marshal(Record{Version: RecordVersion, Provenance: "synthetic", Digest: RequestDigest(req), Request: req, Response: response})
	if err != nil {
		t.Fatal(err)
	}
	return string(b) + "\n"
}

func TestDigestNormalizationAndToolOrdering(t *testing.T) {
	first := recordingRequest(filepath.Join(os.TempDir(), "anvil-eval-one"))
	second := recordingRequest(filepath.Join(os.TempDir(), "anvil-eval-two"))
	second.Model = "another-model"
	second.Messages[0].Content = strings.ReplaceAll(second.Messages[0].Content, "2026-03-01T12:01:02Z", "2027-04-02T09:08:07.123+02:00")
	for i := range second.Messages {
		second.Messages[i].Content = strings.NewReplacer("0123456789abcdef", "1111111111111111", "fedcba9876543210", "2222222222222222").Replace(second.Messages[i].Content)
	}
	second.Tools[0], second.Tools[1] = second.Tools[1], second.Tools[0]
	second.Tools[1].Parameters = json.RawMessage(`{"properties":{"file":{"type":"string"}},"type":"object"}`)
	before, _ := json.Marshal(first)
	canonical, err := CanonicalRequest(first)
	if err != nil {
		t.Fatal(err)
	}
	if RequestDigest(first) == "" || RequestDigest(first) != RequestDigest(second) {
		t.Fatalf("generated metadata changed digest\n%s\n%s", canonical, mustCanonicalRecording(t, second))
	}
	after, _ := json.Marshal(first)
	if string(before) != string(after) {
		t.Fatal("canonicalization mutated raw request")
	}
	text := string(canonical)
	for _, want := range []string{"<workspace>", "<timestamp>", "agent-<1>", "agent-<2>"} {
		// JSON's default escaping protects angle brackets.
		escaped, _ := json.Marshal(want)
		if !strings.Contains(text, strings.Trim(string(escaped), "\"")) {
			t.Fatalf("canonical JSON missing %q", want)
		}
	}
}

func mustCanonicalRecording(t *testing.T, req llm.Request) []byte {
	t.Helper()
	b, err := CanonicalRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestDigestMeaningfulChangesRemainSensitive(t *testing.T) {
	base := recordingRequest(filepath.Join(os.TempDir(), "anvil-eval-base"))
	cases := []struct {
		name   string
		change func(*llm.Request)
	}{
		{"user", func(r *llm.Request) { r.Messages[1].Content += " and b.txt" }},
		{"prompt", func(r *llm.Request) { r.Messages[0].Content += "\n# Project rules\nUse tabs" }},
		{"cache", func(r *llm.Request) { r.PromptCache = false }},
		{"tool description", func(r *llm.Request) { r.Tools[0].Description += " carefully" }},
		{"tool schema", func(r *llm.Request) { r.Tools[0].Parameters = json.RawMessage(`{"type":"array"}`) }},
		{"tool name", func(r *llm.Request) { r.Tools[0].Name = "other" }},
		{"file output", func(r *llm.Request) { r.Messages[2].Content += "\nfile changed" }},
		{"spawn body id", func(r *llm.Request) { r.Messages[3].Content += "\nagent-fedcba9876543210 finished in 2 steps." }},
		{"workflow body id", func(r *llm.Request) {
			r.Messages[4].Content += "\n\nscan:\nscan  finished   agent-fedcba9876543210  done"
		}},
		{"ordinary date", func(r *llm.Request) { r.Messages[1].Content += " 2026-03-01T12:01:02Z" }},
		{"tool arguments", func(r *llm.Request) {
			r.Messages[1].ToolCalls = []llm.ToolCall{{ID: "x", Name: "read", Args: json.RawMessage(`{"file":"b.txt"}`)}}
		}},
		{"image", func(r *llm.Request) { r.Messages[1].Parts = []llm.ContentPart{{Type: "image", Data: []byte{1}}} }},
		{"distinct agent identity", func(r *llm.Request) {
			r.Messages[4].Content = strings.ReplaceAll(r.Messages[4].Content, "fedcba9876543210", "0123456789abcdef")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			changed := recordingCopy(t, base)
			tc.change(&changed)
			if RequestDigest(base) == RequestDigest(changed) {
				t.Fatal("meaningful change was normalized away")
			}
		})
	}
}

func TestDigestNormalizationScope(t *testing.T) {
	cwd := filepath.Join(os.TempDir(), "anvil-eval-scope")
	base := recordingRequest(cwd)
	cases := []struct {
		name, a, b string
		role       llm.Role
	}{
		{"user path", cwd + "/a", cwd + "-other/a", llm.RoleUser},
		{"system path outside intro", "file " + cwd, "file " + cwd + "-other", llm.RoleSystem},
		{"fake cwd label", "Working directory: " + cwd, "Working directory: " + cwd + "-other", llm.RoleSystem},
		{"date outside notes", "2026-03-01T12:01:02Z", "2027-03-01T12:01:02Z", llm.RoleSystem},
		{"date after notes", "# Session notes\nhello\n# Project rules\n2026-03-01T12:01:02Z", "# Session notes\nhello\n# Project rules\n2027-03-01T12:01:02Z", llm.RoleSystem},
		{"non RFC3339 date", "# Session notes\n2026-03-01 12:01:02", "# Session notes\n2027-03-01 12:01:02", llm.RoleSystem},
		{"invalid RFC3339 date", "# Session notes\n2026-99-01T12:01:02Z", "# Session notes\n2027-99-01T12:01:02Z", llm.RoleSystem},
		{"ordinary generated-looking id", "file agent-0123456789abcdef", "file agent-fedcba9876543210", llm.RoleTool},
		{"user summary", "agent-0123456789abcdef finished in 2 steps.", "agent-fedcba9876543210 finished in 2 steps.", llm.RoleUser},
		{"file summary", "agent-0123456789abcdef finished in 2 steps.", "agent-fedcba9876543210 finished in 2 steps.", llm.RoleTool},
		{"file workflow row", "scan  finished   agent-0123456789abcdef  done", "scan  finished   agent-fedcba9876543210  done", llm.RoleTool},
		{"non-generated summary id", "agent-0123456789abcde finished in 2 steps.", "agent-fedcba987654321 finished in 2 steps.", llm.RoleTool},
		{"unrelated temp root", filepath.Join(os.TempDir(), "other-one") + "/a", filepath.Join(os.TempDir(), "other-two") + "/a", llm.RoleTool},
		{"root prefix collision", cwd + "-one/a", cwd + "-two/a", llm.RoleTool},
		{"embedded root", "prefix" + cwd + "/a", "other" + cwd + "/a", llm.RoleTool},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, b := recordingCopy(t, base), recordingCopy(t, base)
			a.Messages = append(a.Messages, llm.Message{Role: tc.role, Content: tc.a})
			b.Messages = append(b.Messages, llm.Message{Role: tc.role, Content: tc.b})
			if RequestDigest(a) == RequestDigest(b) {
				t.Fatal("normalized non-generated content")
			}
		})
	}
	// Non-temporary workspace paths in file output are meaningful, even though
	// the actual intro's cwd line is portable.
	a, b := recordingRequest("/projects/one"), recordingRequest("/projects/two")
	if RequestDigest(a) == RequestDigest(b) {
		t.Fatal("normalized non-temporary file paths")
	}
}

func TestDigestChildBodyIDsRemainSensitive(t *testing.T) {
	for _, name := range []string{"spawn_agent", "run_workflow"} {
		t.Run(name, func(t *testing.T) {
			a := recordingRequest(filepath.Join(os.TempDir(), "body-test"))
			body := "agent-0123456789abcdef finished in 2 steps."
			if name == "run_workflow" {
				body = "scan  finished   agent-0123456789abcdef  quoted"
			}
			a.Messages = append(a.Messages, llm.Message{Role: llm.RoleTool, Name: name,
				Content: "agent-1111111111111111 finished in 1 steps.\n" + body})
			if name == "run_workflow" {
				a.Messages[len(a.Messages)-1].Content = "workflow scan: finished (1 steps)\nscan  finished   agent-1111111111111111  result\n\nscan:\n" + body
			}
			b := recordingCopy(t, a)
			b.Messages[len(b.Messages)-1].Content = strings.ReplaceAll(b.Messages[len(b.Messages)-1].Content, "0123456789abcdef", "fedcba9876543210")
			if RequestDigest(a) == RequestDigest(b) {
				t.Fatal("child body content was mistaken for generated metadata")
			}
		})
	}
}

func TestDigestGoTestDurationScope(t *testing.T) {
	makeRequest := func(name, status, pkg, duration string) llm.Request {
		return llm.Request{Messages: []llm.Message{{Role: llm.RoleTool, Name: name, Content: status + "\t" + pkg + "\t" + duration + "\n"}}}
	}
	base := makeRequest("run_tests", "ok", "evalfixture", "0.001s")
	changed := makeRequest("run_tests", "ok", "evalfixture", "2.345s")
	if RequestDigest(base) != RequestDigest(changed) {
		t.Fatal("Go test duration remained volatile")
	}
	for _, changed := range []llm.Request{
		makeRequest("run_tests", "FAIL", "evalfixture", "0.001s"),
		makeRequest("run_tests", "ok", "other", "0.001s"),
		makeRequest("run_tests", "ok", "evalfixture", "(cached)"),
	} {
		if RequestDigest(base) == RequestDigest(changed) {
			t.Fatal("Go test status, package, or cache result normalized away")
		}
	}
	for _, name := range []string{"read_file", "", "shell"} {
		a, b := makeRequest(name, "ok", "evalfixture", "0.001s"), makeRequest(name, "ok", "evalfixture", "2.345s")
		if RequestDigest(a) == RequestDigest(b) {
			t.Fatalf("normalized duration outside run_tests: %q", name)
		}
	}
	a, b := makeRequest("run_tests", "ok", "evalfixture", "0.001s"), makeRequest("run_tests", "ok", "evalfixture", "2.345s")
	a.Messages[0].Role, b.Messages[0].Role = llm.RoleUser, llm.RoleUser
	if RequestDigest(a) == RequestDigest(b) {
		t.Fatal("normalized user text with a tool name")
	}
	a, b = makeRequest("", "ok", "evalfixture", "0.001s"), makeRequest("", "ok", "evalfixture", "2.345s")
	for _, r := range []*llm.Request{&a, &b} {
		r.Messages[0].ToolCallID = "test-call"
		r.Messages = append([]llm.Message{{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "test-call", Name: "run_tests", Args: json.RawMessage(`{}`)}}}}, r.Messages...)
	}
	if RequestDigest(a) != RequestDigest(b) {
		t.Fatal("matching tool-call name did not identify run_tests result")
	}
}

func TestDigestDeepCopyAndInvalidJSON(t *testing.T) {
	req := recordingRequest(filepath.Join(os.TempDir(), "anvil-eval-copy"))
	req.Messages[1].Parts = []llm.ContentPart{{Type: "text", Text: "user text"}, {Type: "image", Data: []byte{1, 2}}}
	req.Messages[1].ToolCalls = []llm.ToolCall{{ID: "call", Args: json.RawMessage(`{"x":9007199254740993}`)}}
	before, _ := json.Marshal(req)
	canonical := mustCanonicalRecording(t, req)
	after, _ := json.Marshal(req)
	if string(before) != string(after) || !strings.Contains(string(canonical), "9007199254740993") {
		t.Fatal("lost raw snapshot or integer precision")
	}
	req.Tools[0].Parameters = json.RawMessage(`{invalid`)
	if _, err := CanonicalRequest(req); err == nil || RequestDigest(req) != "" {
		t.Fatal("invalid request JSON accepted")
	}
}

func TestReplayDuplicatesCloningAndExhaustion(t *testing.T) {
	req, response := recordingRequest(filepath.Join(os.TempDir(), "anvil-eval-replay")), recordingResponse()
	line := recordingLine(t, req, response)
	replay, err := NewReplay(strings.NewReader(line + line))
	if err != nil {
		t.Fatal(err)
	}
	first, err := replay.Complete(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	first.Message.Content = "changed"
	first.Message.Parts[0].Data[0] = 99
	first.Message.ToolCalls[0].Args[0] = '['
	first.Usage.InputTokens = 99
	second, err := replay.Complete(context.Background(), req)
	if err != nil || second.Message.Content != response.Message.Content || second.Message.Parts[0].Data[0] != 1 || second.Message.ToolCalls[0].Args[0] != '{' || second.Usage.InputTokens != 7 {
		t.Fatalf("response alias escaped: %v, %#v", err, second)
	}
	if err := replay.Verify(); err != nil {
		t.Fatal(err)
	}
	if _, err := replay.Complete(context.Background(), req); err == nil || !strings.Contains(err.Error(), "exhausted") {
		t.Fatalf("finite duplicates were not exhausted: %v", err)
	}
	if replay.Err() == nil || replay.Verify() == nil {
		t.Fatal("exhaustion was not sticky")
	}
}

func TestReplayValidation(t *testing.T) {
	req, response := recordingRequest(filepath.Join(os.TempDir(), "anvil-eval-validation")), recordingResponse()
	line := recordingLine(t, req, response)
	var object map[string]any
	if err := json.Unmarshal([]byte(line), &object); err != nil {
		t.Fatal(err)
	}
	change := func(key string, value any, remove bool) string {
		var copied map[string]any
		_ = json.Unmarshal([]byte(line), &copied)
		if remove {
			delete(copied, key)
		} else {
			copied[key] = value
		}
		b, _ := json.Marshal(copied)
		return string(b) + "\n"
	}
	cases := []struct{ name, input, want string }{
		{"empty recording", "", "line 1"},
		{"empty line", line + " \t\n", "line 2"},
		{"malformed", line + "{\n", "line 2"},
		{"trailing JSON", strings.TrimSpace(line) + " {}\n", "line 1"},
		{"version", change("version", 2, false), "version"},
		{"digest", change("digest", "bad", false), "digest"},
		{"provenance", change("provenance", "", false), "provenance"},
		{"unknown provenance", change("provenance", "offline", false), "provenance"},
		{"missing provenance", change("provenance", nil, true), "required"},
		{"missing request", change("request", nil, true), "required"},
		{"null response", change("response", nil, false), "required"},
		{"unknown outer field", change("headers", map[string]string{"authorization": "secret"}, false), "unknown field"},
		{"oversized", strings.Repeat("x", maxRecordLine+10) + "\n", "line 1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if replay, err := NewReplay(strings.NewReader(tc.input)); err == nil || replay != nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("unexpected validation result: %v, %v", replay, err)
			}
		})
	}
	other := recordingResponse()
	other.Message.Content = "different"
	if _, err := NewReplay(strings.NewReader(line + recordingLine(t, req, other))); err == nil || !strings.Contains(err.Error(), "conflicting duplicate") {
		t.Fatalf("conflicting duplicates accepted: %v", err)
	}
	if _, err := NewReplay(nil); err == nil {
		t.Fatal("nil reader accepted")
	}
}

func TestReplayUnconsumedAndNearestDiff(t *testing.T) {
	one := llm.Request{Messages: []llm.Message{{Role: llm.RoleUser, Content: "recorded-one"}}}
	three := llm.Request{Messages: []llm.Message{{Role: llm.RoleSystem, Content: "s"}, {Role: llm.RoleUser, Content: "recorded-three"}, {Role: llm.RoleAssistant, Content: "a"}}}
	replay, err := NewReplay(strings.NewReader(recordingLine(t, one, recordingResponse()) + recordingLine(t, three, recordingResponse())))
	if err != nil {
		t.Fatal(err)
	}
	if err := replay.Verify(); err == nil || !strings.Contains(err.Error(), "2 unconsumed") {
		t.Fatalf("missing unconsumed counts: %v", err)
	}
	actual := recordingCopy(t, three)
	actual.Messages[1].Content = "actual-three"
	_, miss := replay.Complete(context.Background(), actual)
	if miss == nil {
		t.Fatal("miss accepted")
	}
	for _, want := range []string{"--- recorded\n+++ actual\n@@", `-      "Content": "recorded-three"`, `+      "Content": "actual-three"`} {
		if !strings.Contains(miss.Error(), want) {
			t.Fatalf("nearest unified diff missing %q: %v", want, miss)
		}
	}
	if _, err := replay.Complete(context.Background(), one); err != nil {
		t.Fatal(err)
	}
	if replay.Err() != miss || !strings.Contains(replay.Verify().Error(), "1 unconsumed") {
		t.Fatal("miss or unconsumed count not sticky")
	}
	// Message-count delta wins, then shared digest prefix, then file order.
	fake := &Replay{entries: []replayEntry{
		{record: Record{Digest: "ab00", Request: one, Provenance: "first"}},
		{record: Record{Digest: "abc0", Request: one, Provenance: "second"}},
		{record: Record{Digest: "abc1", Request: one, Provenance: "third"}},
		{record: Record{Digest: "abcde", Request: three, Provenance: "wrong-count"}},
	}}
	if got := fake.nearest(1, "abcf").record.Provenance; got != "second" {
		t.Fatalf("nearest tie order: %s", got)
	}
}

func TestReplayCancellationAndConcurrency(t *testing.T) {
	req := llm.Request{Messages: []llm.Message{{Role: llm.RoleUser, Content: "same"}}}
	const count = 32
	replay, err := NewReplay(strings.NewReader(strings.Repeat(recordingLine(t, req, recordingResponse()), count)))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := replay.Complete(ctx, req); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	var wg sync.WaitGroup
	failures := make(chan error, count)
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			response, err := replay.Complete(context.Background(), req)
			if err == nil {
				response.Usage.InputTokens++
				response.Message.Parts[0].Data[0]++
			}
			failures <- err
		}()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := replay.Verify(); !errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "unconsumed") {
		t.Fatalf("cancelled call consumed a record or error was cleared: %v", err)
	}
}

func TestRecorderSnapshotsBytesAndLoad(t *testing.T) {
	req := recordingRequest(filepath.Join(os.TempDir(), "anvil-eval-record"))
	req.Messages[1].Parts = []llm.ContentPart{{Type: "image", Data: []byte{4, 5}}}
	req.Messages[1].ToolCalls = []llm.ToolCall{{ID: "call", Args: json.RawMessage(`{"file":"a"}`)}}
	original, _ := json.Marshal(req)
	providerResponse := recordingResponse()
	responseBefore, _ := json.Marshal(providerResponse)
	upstream := recordingCompleterFunc(func(_ context.Context, received llm.Request) (*llm.Response, error) {
		received.Messages[0].Content = "mutated"
		received.Messages[1].Parts[0].Data[0] = 99
		received.Messages[1].ToolCalls[0].Args[0] = '['
		received.Tools[0].Parameters[0] = '['
		return &providerResponse, nil
	})
	path := filepath.Join(t.TempDir(), "capture.live.jsonl")
	recorder, err := NewRecorder(path, upstream, "provider")
	if err != nil {
		t.Fatal(err)
	}
	returned, err := recorder.Complete(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	providerResponse.Message.Parts[0].Data[0] = 99
	providerResponse.Usage.InputTokens = 99
	if returned.Message.Parts[0].Data[0] != 1 || returned.Usage.InputTokens != 7 {
		t.Fatal("returned response aliases provider")
	}
	returned.Message.ToolCalls[0].Args[0] = '['
	returned.Usage.InputTokens = 123
	after, _ := json.Marshal(req)
	if string(original) != string(after) || recorder.Bytes() != int64(len(original)) {
		t.Fatal("raw request was mutated or byte count is canonical instead of original")
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), "\n") != 1 {
		t.Fatal("JSONL record is not a complete single line")
	}
	var record Record
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	storedRequest, _ := json.Marshal(record.Request)
	storedResponse, _ := json.Marshal(record.Response)
	if record.Version != 1 || record.Provenance != "provider" || string(storedRequest) != string(original) || string(storedResponse) != string(responseBefore) {
		t.Fatal("recorded snapshots or provenance changed")
	}
	var outer map[string]json.RawMessage
	_ = json.Unmarshal(data, &outer)
	if len(outer) != 5 || outer["request"] == nil || outer["response"] == nil {
		t.Fatal("recorded something other than typed traffic")
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(path)
		if info.Mode().Perm() != 0600 {
			t.Fatalf("recording permissions: %v", info.Mode())
		}
	}
	replay, err := LoadReplay(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := replay.Complete(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if err := replay.Verify(); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRecorderExclusiveFilesAndValidation(t *testing.T) {
	upstream := recordingCompleterFunc(func(context.Context, llm.Request) (*llm.Response, error) {
		response := recordingResponse()
		return &response, nil
	})
	path := filepath.Join(t.TempDir(), "existing.live.jsonl")
	contents := []byte("precious existing contents\n")
	if err := os.WriteFile(path, contents, 0600); err != nil {
		t.Fatal(err)
	}
	if recorder, err := NewRecorder(path, upstream, "synthetic"); err == nil || recorder != nil || !errors.Is(err, os.ErrExist) {
		t.Fatalf("existing file overwritten: %v, %v", recorder, err)
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(contents) {
		t.Fatal("existing recording changed on create failure")
	}
	for _, tc := range []struct {
		path, provenance string
		upstream         llm.Completer
	}{
		{path + ".json", "provider", upstream},
		{path + "-new.live.jsonl", "", upstream},
		{path + "-new.live.jsonl", "invented", upstream},
		{path + "-new.live.jsonl", "provider", nil},
	} {
		if _, err := NewRecorder(tc.path, tc.upstream, tc.provenance); err == nil {
			t.Fatal("invalid recorder accepted")
		}
		if tc.path != path {
			if _, err := os.Stat(tc.path); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("validation created a file")
			}
		}
	}
}

func TestRecorderProviderCancellationAndNilErrors(t *testing.T) {
	req := llm.Request{Model: "raw", Messages: []llm.Message{{Role: llm.RoleUser, Content: "attempt"}}}
	b, _ := json.Marshal(req)
	providerErr := errors.New("provider failed")
	calls := 0
	upstream := recordingCompleterFunc(func(context.Context, llm.Request) (*llm.Response, error) {
		calls++
		if calls == 1 {
			return nil, providerErr
		}
		return nil, nil
	})
	path := filepath.Join(t.TempDir(), "errors.live.jsonl")
	recorder, err := NewRecorder(path, upstream, "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := recorder.Complete(context.Background(), req); !errors.Is(err, providerErr) {
		t.Fatalf("provider error lost: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := recorder.Complete(ctx, req); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	if _, err := recorder.Complete(context.Background(), req); err == nil || !strings.Contains(err.Error(), "nil response") {
		t.Fatalf("nil response accepted: %v", err)
	}
	if calls != 2 || recorder.Bytes() != 3*int64(len(b)) || !errors.Is(recorder.Err(), providerErr) || !errors.Is(recorder.Err(), context.Canceled) {
		t.Fatal("attempt accounting or sticky errors lost")
	}
	if err := recorder.Close(); !errors.Is(err, providerErr) || !errors.Is(err, context.Canceled) {
		t.Fatalf("close hid errors: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) != 0 {
		t.Fatal("failed provider attempts produced records or removed file")
	}
}

func TestRecorderWriteAndCloseErrorsPreserveFile(t *testing.T) {
	upstream := recordingCompleterFunc(func(context.Context, llm.Request) (*llm.Response, error) {
		response := recordingResponse()
		return &response, nil
	})
	path := filepath.Join(t.TempDir(), "write-error.live.jsonl")
	recorder, err := NewRecorder(path, upstream, "provider")
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.file.Close(); err != nil {
		t.Fatal(err)
	}
	if response, err := recorder.Complete(context.Background(), llm.Request{}); err == nil || response != nil || !errors.Is(err, os.ErrClosed) {
		t.Fatalf("write error was silent: %v, %v", response, err)
	}
	if recorder.Err() == nil {
		t.Fatal("write error not sticky")
	}
	if err := recorder.Close(); err == nil || !strings.Contains(err.Error(), "close recording") {
		t.Fatalf("close error hidden: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("failed close removed the file")
	}
	closePath := filepath.Join(t.TempDir(), "close-error.live.jsonl")
	closeRecorder, err := NewRecorder(closePath, upstream, "provider")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = closeRecorder.Complete(context.Background(), llm.Request{})
	before, _ := os.ReadFile(closePath)
	_ = closeRecorder.file.Close()
	if err := closeRecorder.Close(); err == nil {
		t.Fatal("file close failure not propagated")
	}
	after, err := os.ReadFile(closePath)
	if err != nil || string(before) != string(after) {
		t.Fatal("close failure changed recorded contents")
	}
}

func waitRecordingSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for concurrent operation")
	}
}

func TestRecorderCloseRacingCalls(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	upstream := recordingCompleterFunc(func(context.Context, llm.Request) (*llm.Response, error) {
		close(started)
		<-release
		response := recordingResponse()
		return &response, nil
	})
	recorder, err := NewRecorder(filepath.Join(t.TempDir(), "race.live.jsonl"), upstream, "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	request := llm.Request{Messages: []llm.Message{{Content: "concurrent"}}}
	complete := make(chan error, 1)
	go func() { _, err := recorder.Complete(context.Background(), request); complete <- err }()
	waitRecordingSignal(t, started)
	closed := make(chan error, 1)
	go func() { closed <- recorder.Close() }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		recorder.mu.Lock()
		closing := recorder.closing
		recorder.mu.Unlock()
		if closing {
			break
		}
		if time.Now().After(deadline) {
			close(release)
			t.Fatal("Close did not admit itself")
		}
		runtime.Gosched()
	}
	select {
	case err := <-closed:
		close(release)
		t.Fatalf("Close returned with an in-flight call: %v", err)
	default:
	}
	if _, err := recorder.Complete(context.Background(), request); err == nil || !strings.Contains(err.Error(), "closed") {
		close(release)
		t.Fatalf("new call admitted during Close: %v", err)
	}
	close(release)
	if err := <-complete; err != nil {
		t.Fatal(err)
	}
	if err := <-closed; err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("Close hid rejected-call sticky error: %v", err)
	}
	b, _ := json.Marshal(request)
	if recorder.Bytes() != 2*int64(len(b)) {
		t.Fatal("racing rejected call not counted")
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if recorder.Close() == nil {
				t.Error("concurrent Close lost sticky error")
			}
		}()
	}
	wg.Wait()
}

func TestRecorderConcurrentLinesAndCancellation(t *testing.T) {
	const count = 24
	started, release := make(chan struct{}, count), make(chan struct{})
	upstream := recordingCompleterFunc(func(ctx context.Context, req llm.Request) (*llm.Response, error) {
		started <- struct{}{}
		select {
		case <-release:
			return &llm.Response{Message: llm.Message{Role: llm.RoleAssistant, Content: req.Messages[0].Content}}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	path := filepath.Join(t.TempDir(), "parallel.live.jsonl")
	recorder, err := NewRecorder(path, upstream, "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	requests := make([]llm.Request, count)
	results := make(chan error, count)
	var wg sync.WaitGroup
	for i := range requests {
		requests[i] = llm.Request{Messages: []llm.Message{{Role: llm.RoleUser, Content: fmt.Sprintf("line %d\nembedded newline", i)}}}
		wg.Add(1)
		go func(req llm.Request) {
			defer wg.Done()
			_, err := recorder.Complete(context.Background(), req)
			results <- err
		}(requests[i])
	}
	for i := 0; i < count; i++ {
		waitRecordingSignal(t, started)
	}
	// A separate cancelled in-flight provider call must not corrupt others.
	ctx, cancel := context.WithCancel(context.Background())
	cancelled := make(chan error, 1)
	go func() {
		_, err := recorder.Complete(ctx, llm.Request{Messages: []llm.Message{{Content: "cancel"}}})
		cancelled <- err
	}()
	waitRecordingSignal(t, started)
	cancel()
	if err := <-cancelled; !errors.Is(err, context.Canceled) {
		t.Fatalf("in-flight cancellation lost: %v", err)
	}
	close(release)
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := recorder.Close(); !errors.Is(err, context.Canceled) {
		t.Fatalf("Close lost cancellation: %v", err)
	}
	replay, err := LoadReplay(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, req := range requests {
		response, err := replay.Complete(context.Background(), req)
		if err != nil || response.Message.Content != req.Messages[0].Content {
			t.Fatalf("interleaved or incorrect recording: %v", err)
		}
	}
	if err := replay.Verify(); err != nil {
		t.Fatal(err)
	}
}

// A delegate result opens with the same generated "agent-<id> finished in N steps."
// heading spawn_agent's does; only that heading is volatile, never the report under it.
func TestDigestDelegateHeadingIsVolatileButReportIsNot(t *testing.T) {
	result := func(id, report string) llm.Request {
		r := recordingRequest(filepath.Join(os.TempDir(), "delegate-test"))
		r.Messages = append(r.Messages, llm.Message{Role: llm.RoleTool, Name: "delegate",
			Content: "agent-" + id + " finished in 2 steps.\nacceptance:\n  [met]       " + report + "\nchanged: pkg/a.go\n"})
		return r
	}
	if RequestDigest(result("aaaaaaaaaaaaaaaa", "a is changed")) != RequestDigest(result("bbbbbbbbbbbbbbbb", "a is changed")) {
		t.Fatal("the generated agent id in a delegate heading made two runs differ")
	}
	if RequestDigest(result("aaaaaaaaaaaaaaaa", "a is changed")) == RequestDigest(result("aaaaaaaaaaaaaaaa", "b is changed")) {
		t.Fatal("the acceptance report under a delegate heading was normalized away")
	}
}
