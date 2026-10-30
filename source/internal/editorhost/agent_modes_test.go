// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package editorhost

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/anvil-dev/anvil"
)

func TestHostVersionMatchesAgent(t *testing.T) {
	if Version != anvil.Version {
		t.Fatalf("host version %q != Agent version %q", Version, anvil.Version)
	}
}

func TestAgentModeProtocolMapping(t *testing.T) {
	dir := newWorkspace(t)
	srv := New(Config{CWD: dir, Model: "test-model"}, strings.NewReader(""), &bytes.Buffer{})
	sess := newSession("sess-mode", dir, ModeAsk, false)
	disabled := false
	enabled := true

	tests := []struct {
		name    string
		params  SessionParams
		want    anvil.Mode
		wantErr string
	}{
		{name: "absent is single", want: anvil.ModeSingle},
		{name: "explicit single", params: SessionParams{AgentMode: AgentModeSingle}, want: anvil.ModeSingle},
		{name: "single may keep child tools", params: SessionParams{AgentMode: AgentModeSingle, Subagents: &enabled}, want: anvil.ModeSingle},
		{name: "multi", params: SessionParams{AgentMode: AgentModeMulti}, want: anvil.ModeMulti},
		{name: "complex", params: SessionParams{AgentMode: AgentModeComplex}, want: anvil.ModeComplex},
		{name: "unknown", params: SessionParams{AgentMode: "automatic"}, wantErr: "unknown agent_mode"},
		{name: "multi needs children", params: SessionParams{AgentMode: AgentModeMulti, Subagents: &disabled}, wantErr: "requires subagents"},
		{name: "complex needs children", params: SessionParams{AgentMode: AgentModeComplex, Subagents: &disabled}, wantErr: "requires subagents"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts, rpcErr := srv.buildOptions(context.Background(), tt.params, sess)
			if tt.wantErr != "" {
				if rpcErr == nil || !strings.Contains(rpcErr.Message, tt.wantErr) {
					t.Fatalf("error=%v, want %q", rpcErr, tt.wantErr)
				}
				return
			}
			if rpcErr != nil {
				t.Fatal(rpcErr.Message)
			}
			if opts.Mode != tt.want {
				t.Fatalf("Mode=%q, want %q", opts.Mode, tt.want)
			}
		})
	}
}

func TestAgentModeWireRoundTrip(t *testing.T) {
	for _, want := range []string{"", AgentModeSingle, AgentModeMulti, AgentModeComplex, "unknown"} {
		raw, err := json.Marshal(SessionParams{AgentMode: want})
		if err != nil {
			t.Fatal(err)
		}
		var got SessionParams
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		if got.AgentMode != want {
			t.Fatalf("round trip AgentMode=%q, want %q", got.AgentMode, want)
		}
	}
}

func TestAgentModeNewAndResume(t *testing.T) {
	llm := newStubLLM(t)
	h, _ := initHost(t, llm.baseURL())

	for _, tt := range []struct {
		name   string
		params SessionParams
		want   string
	}{
		{name: "legacy absent", params: SessionParams{}, want: AgentModeSingle},
		{name: "single", params: SessionParams{AgentMode: AgentModeSingle}, want: AgentModeSingle},
		{name: "multi", params: SessionParams{AgentMode: AgentModeMulti}, want: AgentModeMulti},
		{name: "complex", params: SessionParams{AgentMode: AgentModeComplex}, want: AgentModeComplex},
	} {
		t.Run(tt.name, func(t *testing.T) {
			created := resultOf(t, h.callOK(MethodSessionNew, tt.params))
			if created["agent_mode"] != tt.want {
				t.Fatalf("new agent_mode=%v, want %s", created["agent_mode"], tt.want)
			}
			sessionID, _ := created["session_id"].(string)
			agentID, _ := created["agent_id"].(string)
			h.callOK(MethodSessionClose, SessionRefParams{SessionID: sessionID})

			resumeParams := tt.params
			resumeParams.AgentID = agentID
			resumed := resultOf(t, h.callOK(MethodSessionResume, resumeParams))
			if resumed["agent_mode"] != tt.want {
				t.Fatalf("resume agent_mode=%v, want %s", resumed["agent_mode"], tt.want)
			}
			resumedID, _ := resumed["session_id"].(string)
			h.callOK(MethodSessionClose, SessionRefParams{SessionID: resumedID})
		})
	}
}

func TestAgentModeRejectsUnknownAndIncompatibleRequests(t *testing.T) {
	llm := newStubLLM(t)
	h, _ := initHost(t, llm.baseURL())
	disabled := false

	for _, params := range []SessionParams{
		{AgentMode: "automatic"},
		{AgentMode: AgentModeMulti, Subagents: &disabled},
		{AgentMode: AgentModeComplex, Subagents: &disabled},
	} {
		msg := h.call(MethodSessionNew, params)
		rpcErr, ok := rpcErrorOf(msg)
		if !ok || rpcErr.Code != CodeInvalidParams {
			t.Fatalf("params=%+v accepted: %v", params, msg)
		}
	}
}

func TestAgentModeReachesSDKToolRegistration(t *testing.T) {
	llm := newStubLLM(t, textTurn("single"), textTurn("multi"))
	h, _ := initHost(t, llm.baseURL())

	single := resultOf(t, h.callOK(MethodSessionNew, SessionParams{AgentMode: AgentModeSingle}))
	singleID, _ := single["session_id"].(string)
	promptRun(t, h, singleID, "one", autoApprove)
	if body := llm.lastRequestBody(t); strings.Contains(body, `"name":"delegate"`) {
		t.Fatalf("single advertised delegate:\n%s", body)
	}
	h.callOK(MethodSessionClose, SessionRefParams{SessionID: singleID})

	multi := resultOf(t, h.callOK(MethodSessionNew, SessionParams{AgentMode: AgentModeMulti}))
	multiID, _ := multi["session_id"].(string)
	promptRun(t, h, multiID, "two", autoApprove)
	if body := llm.lastRequestBody(t); !strings.Contains(body, `"name":"delegate"`) {
		t.Fatalf("multi did not advertise delegate:\n%s", body)
	}
}

func TestAgentModeCannotChangeOnActiveSession(t *testing.T) {
	llm := newStubLLM(t)
	llm.blockUntilCancel = true
	h, _ := initHost(t, llm.baseURL())

	session := resultOf(t, h.callOK(MethodSessionNew, SessionParams{AgentMode: AgentModeSingle}))
	sessionID, _ := session["session_id"].(string)
	agentID, _ := session["agent_id"].(string)
	ack := resultOf(t, h.callOK(MethodPrompt, PromptParams{SessionID: sessionID, Message: "wait"}))
	runID, _ := ack["run_id"].(string)

	msg := h.call(MethodSessionResume, SessionParams{AgentID: agentID, AgentMode: AgentModeMulti})
	if _, ok := rpcErrorOf(msg); !ok {
		t.Fatalf("active agent was reopened with another mode: %v", msg)
	}

	h.callOK(MethodCancel, CancelParams{SessionID: sessionID, RunID: runID})
	h.collectRunOK(15 * time.Second)
}

func TestStageEventLiveAndReplayWireMatch(t *testing.T) {
	legacy, ok := projectEvent("sess-1", "run-1", anvil.Event{Type: anvil.EventAssistant, Text: "unchanged"})
	if !ok {
		t.Fatal("legacy assistant event was dropped")
	}
	legacyRaw, _ := json.Marshal(legacy)
	if bytes.Contains(legacyRaw, []byte(`"stage"`)) {
		t.Fatalf("legacy event gained stage metadata: %s", legacyRaw)
	}

	llm := newStubLLM(t)
	llm.setRoute(func(body string) (turn, bool) {
		switch {
		case strings.Contains(body, "The brief below is your task"):
			return textTurn("[met] tests pass"), true
		case strings.Contains(body, `"role":"tool"`):
			return textTurn("parent done"), true
		default:
			return toolTurn("delegate-live", "delegate",
				`{"goal":"edit b","plan":["edit"],"files":["b.go"],"acceptance":["tests pass"]}`), true
		}
	})
	h, _ := initHost(t, llm.baseURL())
	session := resultOf(t, h.callOK(MethodSessionNew, SessionParams{AgentMode: AgentModeMulti}))
	sessionID, _ := session["session_id"].(string)
	agentID, _ := session["agent_id"].(string)
	h.callOK(MethodPrompt, PromptParams{SessionID: sessionID, Message: "delegate"})
	out := h.collectRunOK(15 * time.Second)
	var liveStages []any
	for _, message := range out.events {
		params := paramsOf(t, message)
		if params["type"] == string(anvil.EventStage) {
			liveStages = append(liveStages, stageWireFields(params))
		}
	}
	if len(liveStages) != 2 {
		t.Fatalf("live stages=%v", liveStages)
	}
	h.callOK(MethodSessionClose, SessionRefParams{SessionID: sessionID})

	replayed := resultOf(t, h.callOK(MethodSessionTranscript, SessionTranscriptParams{AgentID: agentID}))
	entries, _ := replayed["entries"].([]any)
	var replayStages []any
	for _, rawEntry := range entries {
		entry, _ := rawEntry.(map[string]any)
		if entry["kind"] == anvil.TranscriptStage {
			replayStages = append(replayStages, stageWireFields(entry))
		}
	}
	liveJSON, _ := json.Marshal(liveStages)
	replayJSON, _ := json.Marshal(replayStages)
	if string(replayJSON) != string(liveJSON) {
		t.Fatalf("replay=%s, live=%s", replayJSON, liveJSON)
	}
}

func stageWireFields(value map[string]any) map[string]any {
	out := map[string]any{}
	for _, key := range []string{
		"node_id", "parent_node_id", "depends_on", "agent_id", "agent_role",
		"stage", "brief", "acceptance",
	} {
		if field, ok := value[key]; ok {
			out[key] = field
		}
	}
	return out
}
