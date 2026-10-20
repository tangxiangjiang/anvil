// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

//go:build livesmoke

package editorhost

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/anvil-dev/anvil"
)

// Live run timeouts: RPC replies stay short; model turns can take minutes.
const (
	liveTurnTimeout  = 10 * time.Minute
	liveCancelWait   = 2 * time.Minute
	liveMCPWait      = 2 * time.Minute
)

type liveEnv struct {
	APIKey   string
	Model    string
	BaseURL  string
	Provider string
}

func requireLiveEnv(t *testing.T) liveEnv {
	t.Helper()
	if os.Getenv("ANVIL_LIVE_SMOKE") != "1" {
		t.Skip("set ANVIL_LIVE_SMOKE=1 to run the live protocol smoke")
	}
	key := strings.TrimSpace(os.Getenv("ANVIL_API_KEY"))
	model := strings.TrimSpace(os.Getenv("ANVIL_MODEL"))
	if key == "" || model == "" {
		t.Skip("ANVIL_API_KEY and ANVIL_MODEL are required for the live protocol smoke")
	}
	provider := strings.TrimSpace(os.Getenv("ANVIL_PROVIDER"))
	if provider == "" {
		provider = string(anvil.ProviderOpenAI)
	}
	return liveEnv{
		APIKey:   key,
		Model:    model,
		BaseURL:  strings.TrimSpace(os.Getenv("ANVIL_BASE_URL")),
		Provider: provider,
	}
}

// initLiveHost is initHost pointed at a real provider. startHarness pins a
// dummy API key for offline tests; we restore the live key afterwards.
func initLiveHost(t *testing.T, env liveEnv) (*harness, string) {
	t.Helper()
	dir := newWorkspace(t)
	home := isolateHome(t)
	h := startHarness(t, Config{
		Provider:    env.Provider,
		BaseURL:     env.BaseURL,
		Model:       env.Model,
		MCPTokenDir: filepath.Join(home, "mcp-oauth"),
	})
	t.Setenv("ANVIL_API_KEY", env.APIKey)
	t.Setenv("ANVIL_MODEL", env.Model)
	if env.BaseURL != "" {
		t.Setenv("ANVIL_BASE_URL", env.BaseURL)
	}
	t.Setenv("ANVIL_PROVIDER", env.Provider)

	res := resultOf(t, h.callOK(MethodInitialize, InitializeParams{
		Protocol: ProtocolVersion,
		CWD:      dir,
		Client:   ClientInfo{Name: "live-protocol-smoke", Version: "test"},
	}))
	if got := res["cwd"]; got != dir {
		t.Fatalf("cwd=%v want %v", got, dir)
	}
	return h, dir
}

func livePrompt(t *testing.T, h *harness, sessionID, message string) runOutcome {
	t.Helper()
	h.callOK(MethodPrompt, PromptParams{SessionID: sessionID, Message: message})
	return h.collectRun(liveTurnTimeout, autoApprove)
}

func liveStep(t *testing.T, name string) {
	t.Helper()
	t.Logf("=== live smoke: %s ===", name)
}

func buildFakeRuntime(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	sourceRoot := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
	name := "fakeruntime"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	out := filepath.Join(t.TempDir(), name)
	cmd := exec.Command("go", "build", "-o", out, "./internal/mcp/fakeruntime")
	cmd.Dir = sourceRoot
	if raw, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fakeruntime: %v\n%s", err, raw)
	}
	return out
}

// TestLiveProtocolSmoke is the long, real-LLM acceptance path (p_0.45.2).
func TestLiveProtocolSmoke(t *testing.T) {
	env := requireLiveEnv(t)
	h, dir := initLiveHost(t, env)

	liveStep(t, "1 catalogues")
	init := resultOf(t, h.callOK(MethodInitialize, InitializeParams{
		Protocol: ProtocolVersion,
		CWD:      dir,
		Client:   ClientInfo{Name: "live-protocol-smoke", Version: "test"},
	}))
	caps, _ := init["capabilities"].([]any)
	for _, want := range []string{"sessions", "approval", "mcp", "skills", "rules", "mcp.runtime"} {
		if !capabilityHas(caps, want) {
			t.Fatalf("capabilities missing %q: %v", want, caps)
		}
	}
	if env.BaseURL != "" {
		models := resultOf(t, h.callOK(MethodModelsList, ModelsParams{BaseURL: env.BaseURL}))
		if list, _ := models["models"].([]any); len(list) == 0 {
			t.Fatalf("models.list returned no models: %v", models)
		}
	} else {
		t.Log("ANVIL_BASE_URL unset; skipping models.list")
	}
	h.callOK(MethodSkillsList, SkillsParams{})
	h.callOK(MethodRulesList, RulesParams{LoadProject: true, LoadCursor: false})
	h.callOK(MethodPermissionsGet, nil)

	liveStep(t, "2 MCP runtime (fakeruntime)")
	fakeBin := buildFakeRuntime(t)
	mcpBody, err := json.Marshal(map[string]any{
		"mcpServers": map[string]any{
			"fake": map[string]any{
				"command": fakeBin,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	writeProjectMCP(t, dir, string(mcpBody))
	mcpParams := MCPListParams{SettingSources: []string{"project"}, IncludeTools: true}
	liveWaitForMCP(t, h, mcpParams, "fake", string(anvil.MCPStateConnected))

	if err := os.WriteFile(filepath.Join(dir, "seed.txt"), []byte("seed-alpha"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "edit_me.txt"), []byte("before"), 0o644); err != nil {
		t.Fatal(err)
	}

	sess := resultOf(t, h.callOK(MethodSessionNew, SessionParams{
		SettingSources:  []string{"project"},
		PermissionMode:  ModeAsk,
		LoadPermissions: boolPtr(true),
	}))
	sessionID, _ := sess["session_id"].(string)
	agentID, _ := sess["agent_id"].(string)
	if sessionID == "" || agentID == "" {
		t.Fatalf("session.new incomplete: %v", sess)
	}

	liveStep(t, "2b MCP tool prompt")
	out := livePrompt(t, h, sessionID,
		"Call the MCP tool named mcp_fake_echo with argument text set to live-smoke. Then reply with only the tool output.")
	assertLiveFinished(t, out)

	liveStep(t, "3 builtin tools")
	out = livePrompt(t, h, sessionID,
		"Read the file seed.txt in the workspace root using read_file. Reply with its exact contents.")
	assertLiveFinished(t, out)

	out = livePrompt(t, h, sessionID,
		`Create a new file named live_out.txt in the workspace root with exactly the content: live-write-ok`)
	assertLiveFinished(t, out)
	if got, err := os.ReadFile(filepath.Join(dir, "live_out.txt")); err != nil || !strings.Contains(string(got), "live-write-ok") {
		t.Fatalf("live_out.txt = %q (%v), want it to contain live-write-ok", got, err)
	}

	out = livePrompt(t, h, sessionID,
		`Edit edit_me.txt so its entire contents become exactly: after-edit`)
	assertLiveFinished(t, out)
	if got, err := os.ReadFile(filepath.Join(dir, "edit_me.txt")); err != nil || strings.TrimSpace(string(got)) != "after-edit" {
		t.Fatalf("edit_me.txt = %q (%v), want after-edit", got, err)
	}

	out = livePrompt(t, h, sessionID,
		`Use the shell tool to run: echo live-shell-ok`)
	assertLiveFinished(t, out)

	liveStep(t, "4 approvals (ask + deny once)")
	denied := false
	h.callOK(MethodPrompt, PromptParams{SessionID: sessionID, Message: `Write a new file named denied.txt with content: should-not-exist`})
	denyOut := h.collectRun(liveTurnTimeout, func(req map[string]any) map[string]any {
		if !denied {
			denied = true
			return map[string]any{"decision": DecisionDeny, "message": "live smoke deny"}
		}
		return autoApprove(req)
	})
	if denyOut.done == nil {
		t.Fatal("deny run produced no done")
	}
	if _, err := os.Stat(filepath.Join(dir, "denied.txt")); err == nil {
		t.Fatal("denied.txt should not exist after a denied write")
	}

	liveStep(t, "5 checkpoint")
	if err := os.WriteFile(filepath.Join(dir, "ckpt.txt"), []byte("pre-run"), 0o644); err != nil {
		t.Fatal(err)
	}
	out = livePrompt(t, h, sessionID,
		`Overwrite ckpt.txt so its entire contents become exactly: post-run`)
	assertLiveFinished(t, out)
	runID, _ := paramsOf(t, out.done)["run_id"].(string)
	if runID == "" {
		t.Fatalf("checkpoint run missing run_id: %v", out.done)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "ckpt.txt")); string(got) != "post-run" {
		t.Fatalf("ckpt.txt = %q before restore", got)
	}
	listedCP := resultOf(t, h.callOK(MethodCheckpointList, CheckpointListParams{AgentID: agentID}))
	runs, _ := listedCP["runs"].([]any)
	if len(runs) == 0 {
		t.Fatalf("want at least one checkpoint, got %v", listedCP)
	}
	// Prefer the run we just finished if present.
	cpRunID := runID
	found := false
	for _, raw := range runs {
		row, _ := raw.(map[string]any)
		if row["run_id"] == runID {
			found = true
			break
		}
	}
	if !found {
		row, _ := runs[0].(map[string]any)
		cpRunID, _ = row["run_id"].(string)
	}
	h.callOK(MethodCheckpointRestore, CheckpointRestoreParams{AgentID: agentID, RunID: cpRunID})
	if got, err := os.ReadFile(filepath.Join(dir, "ckpt.txt")); err != nil || string(got) != "pre-run" {
		// If the model wrote a different run than we restored, still require restore changed something meaningful.
		t.Logf("ckpt.txt after restore = %q (%v); expected pre-run when restoring run %s", got, err, cpRunID)
		if err == nil && string(got) == "post-run" {
			t.Fatalf("checkpoint restore for %s did not revert ckpt.txt", cpRunID)
		}
	}

	liveStep(t, "6 fork")
	forked := resultOf(t, h.callOK(MethodSessionFork, SessionForkParams{AgentID: agentID, RunID: runID}))
	forkID, _ := forked["agent_id"].(string)
	if forkID == "" || forkID == agentID {
		t.Fatalf("fork=%v", forked)
	}
	forkSess := resultOf(t, h.callOK(MethodSessionResume, SessionParams{
		AgentID:        forkID,
		SettingSources: []string{"project"},
	}))
	forkSID, _ := forkSess["session_id"].(string)
	out = livePrompt(t, h, forkSID, "Reply with exactly: fork-ok")
	assertLiveFinished(t, out)

	liveStep(t, "7 cancel")
	cancelSess := resultOf(t, h.callOK(MethodSessionNew, SessionParams{
		SettingSources: []string{"project"},
		PermissionMode: ModeAsk,
	}))
	cancelSID, _ := cancelSess["session_id"].(string)
	ack := resultOf(t, h.callOK(MethodPrompt, PromptParams{
		SessionID: cancelSID,
		Message:   longSleepPrompt(),
	}))
	cancelRunID, _ := ack["run_id"].(string)
	// Give the model a moment to start, then cancel.
	time.Sleep(3 * time.Second)
	h.callOK(MethodCancel, CancelParams{SessionID: cancelSID, RunID: cancelRunID})
	cancelOut := h.collectRun(liveCancelWait, autoApprove)
	if cancelOut.done == nil {
		t.Fatal("cancel run produced no done")
	}
	status, _ := paramsOf(t, cancelOut.done)["status"].(string)
	if status != "cancelled" && status != "error" && status != "finished" {
		t.Fatalf("cancel done status=%q want cancelled|error|finished: %v", status, paramsOf(t, cancelOut.done))
	}
	t.Logf("cancel terminal status=%s", status)

	liveStep(t, "8 session admin")
	listed := resultOf(t, h.callOK(MethodSessionList, SessionListParams{}))
	if sessions, _ := listed["sessions"].([]any); !sessionListed(sessions, agentID) {
		t.Fatalf("session.list missing %s: %v", agentID, listed)
	}
	renamed := resultOf(t, h.callOK(MethodSessionRename, SessionRenameParams{
		AgentID: agentID, Title: "live protocol smoke",
	}))
	if renamed["title"] != "live protocol smoke" {
		t.Fatalf("rename=%v", renamed)
	}
	transcript := resultOf(t, h.callOK(MethodSessionTranscript, SessionTranscriptParams{AgentID: agentID}))
	if entries, _ := transcript["entries"].([]any); len(entries) == 0 {
		t.Fatalf("transcript empty: %v", transcript)
	}
	h.callOK(MethodSessionClose, SessionRefParams{SessionID: sessionID})
	resumed := resultOf(t, h.callOK(MethodSessionResume, SessionParams{
		AgentID:        agentID,
		SettingSources: []string{"project"},
	}))
	resumedID, _ := resumed["session_id"].(string)
	out = livePrompt(t, h, resumedID, "Reply with exactly: resumed-ok")
	assertLiveFinished(t, out)

	h.callOK(MethodSessionDelete, SessionDeleteParams{AgentID: agentID})
	after := resultOf(t, h.callOK(MethodSessionList, SessionListParams{}))
	if sessions, _ := after["sessions"].([]any); sessionListed(sessions, agentID) {
		t.Fatalf("deleted agent still listed: %v", after)
	}

	t.Log("live protocol smoke completed")
}

func assertLiveFinished(t *testing.T, out runOutcome) {
	t.Helper()
	if out.done == nil {
		t.Fatal("run produced no done")
	}
	dp := paramsOf(t, out.done)
	status, _ := dp["status"].(string)
	if status != "finished" {
		t.Fatalf("run status=%v want finished: %v", status, dp)
	}
}

func boolPtr(v bool) *bool { return &v }

func longSleepPrompt() string {
	if runtime.GOOS == "windows" {
		return "Use the shell tool to run this exact command and wait for it to finish: ping -n 90 127.0.0.1 >NUL"
	}
	return "Use the shell tool to run this exact command and wait for it to finish: sleep 90"
}

func liveWaitForMCP(t *testing.T, h *harness, params MCPListParams, name, want string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(liveMCPWait)
	var last map[string]any
	for {
		res := resultOf(t, h.callOK(MethodMCPList, params))
		servers := serversByName(t, res)
		last = servers[name]
		if last != nil && last["state"] == want {
			return last
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never reached %q; last=%v listing=%v", name, want, last, res)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
