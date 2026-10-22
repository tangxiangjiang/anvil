// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// permRecorder records every gated call and answers with a fixed decision.
type permRecorder struct {
	mu    sync.Mutex
	calls []PermissionRequest
	dec   PermissionDecision
	err   error
}

func (r *permRecorder) fn(_ context.Context, req PermissionRequest) (PermissionDecision, error) {
	r.mu.Lock()
	r.calls = append(r.calls, req)
	r.mu.Unlock()
	if r.err != nil {
		return PermissionDecision{}, r.err
	}
	return r.dec, nil
}

func (r *permRecorder) names() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.calls))
	for _, c := range r.calls {
		out = append(out, c.ToolName)
	}
	return out
}

func (r *permRecorder) last() (PermissionRequest, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.calls) == 0 {
		return PermissionRequest{}, false
	}
	return r.calls[len(r.calls)-1], true
}

func (r *permRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func gatedAgent(t *testing.T, dir string, opts Options) (*Agent, *permRecorder) {
	t.Helper()
	rec := &permRecorder{dec: PermissionDecision{Allow: true}}
	opts.APIKey = "k"
	opts.Model = Model{ID: "m"}
	opts.Local.CWD = dir
	opts.Permission = rec.fn
	a, err := Create(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a, rec
}

func execTool(a *Agent, name string, args map[string]any) toolExecResult {
	var raw json.RawMessage
	if args == nil {
		raw = json.RawMessage(`{}`)
	} else {
		b, _ := json.Marshal(args)
		raw = b
	}
	res := a.registry.Execute(context.Background(), name, raw)
	return toolExecResult{
		Content:   res.Content,
		IsError:   res.IsError,
		Rule:      permissionRuleMatch(res.Rule),
		Protected: res.Protected,
	}
}

type toolExecResult struct {
	Content string
	IsError bool
	// Rule is the workspace rule that decided the call, when one did — the same
	// thing the tool_result event carries.
	Rule *PermissionRuleMatch
	// Protected are the paths the call touched that an allow rule may not cover,
	// also carried on the tool_result event (0.46+).
	Protected []string
}

// --- baseline: no groups means today's behaviour, unchanged -----------------

func TestPermissionGroupsUnsetKeepsMutatingOnly(t *testing.T) {
	dir := t.TempDir()
	script := writeVerifyScript(t, dir, "ok", 0, "ok")
	a, rec := gatedAgent(t, dir, Options{
		Test: TestOptions{Command: []string{script}, Timeout: 5 * time.Second},
	})

	if res := execTool(a, "read_file", map[string]any{"path": "missing.txt"}); rec.count() != 0 {
		t.Fatalf("read_file must not be gated, got %v (res=%+v)", rec.names(), res)
	}
	execTool(a, "run_tests", nil)
	if got := rec.names(); len(got) != 0 {
		t.Fatalf("run_tests must stay ungated when PermissionGroups is empty, got %v", got)
	}
	execTool(a, "write_file", map[string]any{"path": "a.txt", "content": "hi"})
	if got := rec.names(); len(got) != 1 || got[0] != "write_file" {
		t.Fatalf("write_file must be gated, got %v", got)
	}
}

// --- widening the gate by group ---------------------------------------------

func TestPermissionGroupsWidensGate(t *testing.T) {
	dir := t.TempDir()
	script := writeVerifyScript(t, dir, "ok", 0, "ok")
	a, rec := gatedAgent(t, dir, Options{
		PermissionGroups: []string{"write", "verify"},
		Test:             TestOptions{Command: []string{script}, Timeout: 5 * time.Second},
	})

	execTool(a, "read_file", map[string]any{"path": "missing.txt"})
	execTool(a, "grep", map[string]any{"pattern": "zzz"})
	if got := rec.names(); len(got) != 0 {
		t.Fatalf("ungrouped tools must stay ungated, got %v", got)
	}

	execTool(a, "write_file", map[string]any{"path": "a.txt", "content": "hi"})
	execTool(a, "run_tests", nil)
	got := rec.names()
	if len(got) != 2 || got[0] != "write_file" || got[1] != "run_tests" {
		t.Fatalf("want write_file then run_tests gated, got %v", got)
	}
}

// A selector that names no group is treated as the tool's own name, so a
// caller can gate a single tool without pulling in its whole group.
func TestPermissionGroupsAcceptsLiteralToolName(t *testing.T) {
	dir := t.TempDir()
	a, rec := gatedAgent(t, dir, Options{PermissionGroups: []string{"git_status"}})
	execTool(a, "git_status", nil)
	execTool(a, "shell", map[string]any{"command": "echo hi"})
	execTool(a, "write_file", map[string]any{"path": "a.txt", "content": "hi"})
	if got := rec.names(); len(got) != 1 || got[0] != "git_status" {
		t.Fatalf("want only git_status gated, got %v", got)
	}
}

// --- external tools: MCP, plugin, caller-registered --------------------------

func TestExternalToolsGatedWhenGroupsSet(t *testing.T) {
	dir := t.TempDir()
	bin := buildFakeMCP(t)

	newAgent := func(groups []string) (*Agent, *permRecorder) {
		return gatedAgent(t, dir, Options{
			PermissionGroups: groups,
			MCPServers:       map[string]MCPServerConfig{"fake": {Command: bin}},
			Tools: []ToolSpec{{
				Name:       "caller_tool",
				Parameters: json.RawMessage(`{"type":"object","properties":{}}`),
				Handler: func(context.Context, json.RawMessage) (string, error) {
					return "ok", nil
				},
			}},
		})
	}

	// With no groups, MCP and caller tools stay ungated — the historical gap.
	a, rec := newAgent(nil)
	if res := execTool(a, "caller_tool", nil); res.IsError {
		t.Fatalf("caller tool: %+v", res)
	}
	execTool(a, "mcp_fake_echo", map[string]any{"text": "hi"})
	if got := rec.names(); len(got) != 0 {
		t.Fatalf("no groups: external tools must stay ungated, got %v", got)
	}

	// With groups set, both are gated even though neither names a group that
	// matches them.
	a2, rec2 := newAgent([]string{"write"})
	execTool(a2, "caller_tool", nil)
	execTool(a2, "mcp_fake_echo", map[string]any{"text": "hi"})
	got := rec2.names()
	if len(got) != 2 {
		t.Fatalf("external tools must be gated once groups are set, got %v", got)
	}
}

// --- egress ------------------------------------------------------------------

// Egress is gated by the "web" group like any other tool. The provider is a
// stand-in: if the gate failed to fire the search would succeed and the request
// counter would move.
func TestEgressIsGatedByTheWebGroup(t *testing.T) {
	var hits int32
	var mu sync.Mutex
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]string{{"title": "T", "url": "https://example.com", "snippet": "S"}},
		})
	}))
	defer provider.Close()

	dir := t.TempDir()
	a, rec := gatedAgent(t, dir, Options{
		PermissionGroups: []string{"web"},
		WebSearch:        WebSearchOptions{Enabled: true, BaseURL: provider.URL, APIKey: "tok"},
	})
	execTool(a, "web_search", map[string]any{"query": "anvil"})
	if got := rec.names(); len(got) != 1 || got[0] != "web_search" {
		t.Fatalf("web_search must be gated by the web group, got %v", got)
	}

	mu.Lock()
	allowed := hits
	mu.Unlock()

	rec.dec = PermissionDecision{Allow: false, Message: "no egress"}
	if res := execTool(a, "web_search", map[string]any{"query": "anvil"}); !res.IsError {
		t.Fatalf("a denied search must be an error: %+v", res)
	}
	mu.Lock()
	defer mu.Unlock()
	if hits != allowed {
		t.Fatalf("the provider was called %d more times despite the deny", hits-allowed)
	}
}

// --- decisions --------------------------------------------------------------

func TestDenyReturnsToolErrorAndSkipsTheCall(t *testing.T) {
	dir := t.TempDir()
	a, rec := gatedAgent(t, dir, Options{PermissionGroups: []string{"write"}})
	rec.dec = PermissionDecision{Allow: false, Message: "user said no"}

	res := execTool(a, "write_file", map[string]any{"path": "a.txt", "content": "hi"})
	if !res.IsError || !strings.Contains(res.Content, "user said no") {
		t.Fatalf("deny must surface the decision message as a tool error, got %+v", res)
	}
	if _, err := os.Stat(filepath.Join(dir, "a.txt")); !os.IsNotExist(err) {
		t.Fatal("denied write must not touch the workspace")
	}
}

func TestDenyWithoutMessageGetsADefault(t *testing.T) {
	dir := t.TempDir()
	a, rec := gatedAgent(t, dir, Options{PermissionGroups: []string{"write"}})
	rec.dec = PermissionDecision{Allow: false}

	res := execTool(a, "write_file", map[string]any{"path": "a.txt", "content": "hi"})
	if !res.IsError || !strings.Contains(res.Content, "permission denied for tool write_file") {
		t.Fatalf("got %+v", res)
	}
}

func TestArgsRewriteIsHonoured(t *testing.T) {
	dir := t.TempDir()
	a, rec := gatedAgent(t, dir, Options{PermissionGroups: []string{"write"}})
	rewritten, _ := json.Marshal(map[string]any{"path": "b.txt", "content": "rewritten"})
	rec.dec = PermissionDecision{Allow: true, Args: rewritten}

	if res := execTool(a, "write_file", map[string]any{"path": "a.txt", "content": "original"}); res.IsError {
		t.Fatalf("%+v", res)
	}
	if _, err := os.Stat(filepath.Join(dir, "a.txt")); !os.IsNotExist(err) {
		t.Fatal("the original path must not be written when args are rewritten")
	}
	data, err := os.ReadFile(filepath.Join(dir, "b.txt"))
	if err != nil || string(data) != "rewritten" {
		t.Fatalf("rewritten args were not applied: %q %v", data, err)
	}
}

func TestGroupsAloneOpenNoGate(t *testing.T) {
	dir := t.TempDir()
	// PermissionGroups without a Permission hook: nobody to ask, so the gate
	// stays open rather than silently denying everything.
	a, err := Create(context.Background(), Options{
		APIKey: "k", Model: Model{ID: "m"},
		Local:            LocalOptions{CWD: dir},
		PermissionGroups: []string{"write", "shell"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if res := execTool(a, "write_file", map[string]any{"path": "a.txt", "content": "hi"}); res.IsError {
		t.Fatalf("%+v", res)
	}
	if _, err := os.Stat(filepath.Join(dir, "a.txt")); err != nil {
		t.Fatal("write should have gone through with no permission hook installed")
	}
}

// --- previews ---------------------------------------------------------------

func TestPreviewForWriteFileCarriesADiff(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a, rec := gatedAgent(t, dir, Options{PermissionGroups: []string{"write"}})
	execTool(a, "write_file", map[string]any{"path": "a.txt", "content": "one\nTWO\nthree\n"})

	req, ok := rec.last()
	if !ok {
		t.Fatal("write_file was not gated")
	}
	p := req.Preview
	if p.Kind != PreviewDiff || p.Path != "a.txt" {
		t.Fatalf("unexpected preview: %+v", p)
	}
	for _, want := range []string{"--- a/a.txt", "+++ b/a.txt", "-two", "+TWO", "+three"} {
		if !strings.Contains(p.Diff, want) {
			t.Fatalf("diff missing %q:\n%s", want, p.Diff)
		}
	}
	if strings.Contains(p.Diff, "-one") {
		t.Fatalf("unchanged context must not appear as a deletion:\n%s", p.Diff)
	}
}

func TestPreviewForEditFileReflectsTheReplacement(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a, rec := gatedAgent(t, dir, Options{PermissionGroups: []string{"edit"}})
	execTool(a, "edit_file", map[string]any{"path": "a.txt", "old_string": "beta", "new_string": "gamma"})

	req, _ := rec.last()
	if req.Preview.Kind != PreviewDiff || !strings.Contains(req.Preview.Diff, "-beta") || !strings.Contains(req.Preview.Diff, "+gamma") {
		t.Fatalf("unexpected preview: %+v", req.Preview)
	}
}

func TestPreviewForEditFileReportsAFailingMatch(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a, rec := gatedAgent(t, dir, Options{PermissionGroups: []string{"edit"}})
	execTool(a, "edit_file", map[string]any{"path": "a.txt", "old_string": "nope", "new_string": "x"})

	req, _ := rec.last()
	if req.Preview.Kind != PreviewSummary || !strings.Contains(req.Preview.Summary, "will fail") {
		t.Fatalf("unexpected preview: %+v", req.Preview)
	}
}

func TestPreviewForApplyPatchDryRuns(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a, rec := gatedAgent(t, dir, Options{PermissionGroups: []string{"edit"}})
	// Deny, so the only thing that could have touched the file is the preview.
	rec.dec = PermissionDecision{Allow: false, Message: "preview only"}
	patch := "*** Begin Patch\n*** Update File: a.txt\n@@\n one\n-two\n+TWO\n*** End Patch\n"
	execTool(a, "apply_patch", map[string]any{"patch": patch})

	req, _ := rec.last()
	if req.Preview.Kind != PreviewDiff || req.Preview.Path != "a.txt" {
		t.Fatalf("unexpected preview: %+v", req.Preview)
	}
	if !strings.Contains(req.Preview.Diff, "-two") || !strings.Contains(req.Preview.Diff, "+TWO") {
		t.Fatalf("diff:\n%s", req.Preview.Diff)
	}
	// The dry run must not have touched the file.
	data, _ := os.ReadFile(filepath.Join(dir, "a.txt"))
	if string(data) != "one\ntwo\nthree\n" {
		t.Fatalf("preview mutated the workspace: %q", data)
	}
}

func TestPreviewForApplyPatchReportsABadHunk(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a, rec := gatedAgent(t, dir, Options{PermissionGroups: []string{"edit"}})
	patch := "*** Begin Patch\n*** Update File: a.txt\n@@\n missing\n-x\n+y\n*** End Patch\n"
	execTool(a, "apply_patch", map[string]any{"patch": patch})

	req, _ := rec.last()
	if req.Preview.Kind != PreviewSummary || !strings.Contains(req.Preview.Summary, "hunk 1") {
		t.Fatalf("unexpected preview: %+v", req.Preview)
	}
}

func TestPreviewForCommandsCarriesArgv(t *testing.T) {
	dir := t.TempDir()
	script := writeVerifyScript(t, dir, "ok", 0, "ok")
	a, rec := gatedAgent(t, dir, Options{
		PermissionGroups: []string{"verify", "shell"},
		Test:             TestOptions{Command: []string{script, "-run", "X"}, Timeout: 5 * time.Second},
	})
	execTool(a, "run_tests", nil)
	req, _ := rec.last()
	if req.Preview.Kind != PreviewCommand || len(req.Preview.Command) != 3 {
		t.Fatalf("unexpected preview: %+v", req.Preview)
	}

	execTool(a, "shell", map[string]any{"command": "echo hi"})
	req, _ = rec.last()
	if req.Preview.Kind != PreviewCommand || len(req.Preview.Command) != 1 || req.Preview.Command[0] != "echo hi" {
		t.Fatalf("unexpected preview: %+v", req.Preview)
	}
}

func TestPreviewForUnknownToolIsASummary(t *testing.T) {
	dir := t.TempDir()
	a, rec := gatedAgent(t, dir, Options{
		PermissionGroups: []string{"custom_thing"},
		Tools: []ToolSpec{{
			Name:       "custom_thing",
			Parameters: json.RawMessage(`{"type":"object","properties":{"x":{"type":"string"}}}`),
			Handler:    func(context.Context, json.RawMessage) (string, error) { return "ok", nil },
		}},
	})
	execTool(a, "custom_thing", map[string]any{"x": "value"})
	req, _ := rec.last()
	if req.Preview.Kind != PreviewSummary || !strings.Contains(req.Preview.Summary, "value") {
		t.Fatalf("unexpected preview: %+v", req.Preview)
	}
}

// --- preview bodies (diff review) -------------------------------------------

// A client cannot render a real diff editor from a unified diff without
// re-implementing the patch algorithm, so the preview carries both bodies.
func TestPreviewCarriesBothBodies(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a, rec := gatedAgent(t, dir, Options{PermissionGroups: []string{"write"}})
	execTool(a, "write_file", map[string]any{"path": "a.txt", "content": "one\nTWO\n"})

	req, _ := rec.last()
	if req.Preview.Before != "one\ntwo\n" || req.Preview.After != "one\nTWO\n" {
		t.Fatalf("bodies: before=%q after=%q", req.Preview.Before, req.Preview.After)
	}
	if req.Preview.Truncated {
		t.Fatal("a two-line preview must not be reported as truncated")
	}
}

// A file that does not exist yet previews as an empty pre-image, which is what
// a diff editor needs to show the whole body as an addition.
func TestPreviewCreateHasAnEmptyPreImage(t *testing.T) {
	dir := t.TempDir()
	a, rec := gatedAgent(t, dir, Options{PermissionGroups: []string{"write"}})
	execTool(a, "write_file", map[string]any{"path": "new.txt", "content": "hello\n"})

	req, _ := rec.last()
	if req.Preview.Before != "" || req.Preview.After != "hello\n" {
		t.Fatalf("bodies: before=%q after=%q", req.Preview.Before, req.Preview.After)
	}
}

func TestPreviewDeleteHasAnEmptyPostImage(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gone.txt"), []byte("bye\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a, rec := gatedAgent(t, dir, Options{PermissionGroups: []string{"edit"}})
	patch := "*** Begin Patch\n*** Delete File: gone.txt\n*** End Patch\n"
	execTool(a, "apply_patch", map[string]any{"patch": patch})

	req, _ := rec.last()
	// apply_patch is deliberately body-less: it may span several files, so a
	// single pre/post image would be a lie about which file it describes.
	if req.Preview.Kind != PreviewDiff || req.Preview.Before != "" || req.Preview.After != "" {
		t.Fatalf("unexpected preview: %+v", req.Preview)
	}
	if !strings.Contains(req.Preview.Diff, "-bye") {
		t.Fatalf("diff does not describe the deletion:\n%s", req.Preview.Diff)
	}
}

// Past the shared cap both bodies go together. Keeping whichever fits would let
// a client render a clipped post-image as if it were the whole file — the
// failure mode this rule exists to prevent.
func TestPreviewBodiesAreDroppedTogetherWhenTooLarge(t *testing.T) {
	dir := t.TempDir()
	big := strings.Repeat("x", maxPreviewBody) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "big.txt"), []byte("small\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a, rec := gatedAgent(t, dir, Options{PermissionGroups: []string{"write"}})
	execTool(a, "write_file", map[string]any{"path": "big.txt", "content": big})

	req, _ := rec.last()
	if req.Preview.Kind != PreviewDiff {
		t.Fatalf("unexpected preview kind: %+v", req.Preview)
	}
	if req.Preview.Before != "" || req.Preview.After != "" {
		t.Fatalf("oversized bodies must both be dropped, got %d and %d bytes",
			len(req.Preview.Before), len(req.Preview.After))
	}
	if !req.Preview.Truncated {
		t.Fatal("dropping the bodies must be reported as truncated")
	}
	if req.Preview.Diff == "" {
		t.Fatal("the diff is the fallback and must survive the cap")
	}
}

// --- the group table itself -------------------------------------------------

func TestExpandToolSelectorsIsStable(t *testing.T) {
	cases := []struct {
		selector string
		want     []string
	}{
		{"shell", []string{"shell"}},
		{"write", []string{"write_file"}},
		{"edit", []string{"edit_file", "apply_patch"}},
		{"verify", []string{"run_tests", "build"}},
		{"test", []string{"run_tests", "build"}},
		{"web", []string{"web_search", "fetch_url"}},
		{"mcp", []string{"__mcp__"}},
		{"write_file", []string{"write_file"}},
	}
	for _, tc := range cases {
		got := expandToolSelectors([]string{tc.selector})
		if len(got) != len(tc.want) {
			t.Fatalf("%s: got %v want %v", tc.selector, got, tc.want)
		}
		for _, name := range tc.want {
			if !got[name] {
				t.Fatalf("%s: missing %s in %v", tc.selector, name, got)
			}
		}
	}
	if got := expandToolSelectors(nil); len(got) != 0 {
		t.Fatalf("empty selectors must expand to nothing, got %v", got)
	}
	if got := expandToolSelectors([]string{"  Write  "}); !got["write_file"] {
		t.Fatalf("selectors must be trimmed and lower-cased, got %v", got)
	}
}

func TestToolAllowedMatchesMCPByPrefix(t *testing.T) {
	set := expandToolSelectors([]string{"mcp"})
	if !toolAllowed("mcp_fake_echo", set) {
		t.Fatal("the mcp selector must cover mcp_* names")
	}
	if toolAllowed("mcpish", set) {
		t.Fatal("prefix matching must require the underscore")
	}
}
