// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package editorhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anvil-dev/anvil"
)

// writeRules plants a workspace rule file, creating .anvil/ if it is not there.
func writeRules(t *testing.T, dir, body string) string {
	t.Helper()
	path := anvil.PermissionRulesPath(dir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func readRules(t *testing.T, dir string) string {
	t.Helper()
	data, err := os.ReadFile(anvil.PermissionRulesPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// ruleOf digs one rule out of a rule list on the wire.
func ruleOf(t *testing.T, obj map[string]any) map[string]any {
	t.Helper()
	r, ok := obj["rule"].(map[string]any)
	if !ok {
		t.Fatalf("no rule object: %v", obj)
	}
	return r
}

func rememberKinds(t *testing.T, req map[string]any) map[string]string {
	t.Helper()
	out := map[string]string{}
	items, _ := req["remember"].([]any)
	for _, raw := range items {
		opt, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("bad remember option: %v", raw)
		}
		kind, _ := opt["kind"].(string)
		rule, _ := opt["rule"].(map[string]any)
		out[kind], _ = rule["pattern"].(string)
	}
	return out
}

// --- reading the file --------------------------------------------------------

// A workspace that has written down no policy has a rule file that is not there,
// and that is a state a client has to be able to render — not an error.
func TestPermissionsGetOnAWorkspaceWithNoFile(t *testing.T) {
	llm := newStubLLM(t)
	h, dir := initHost(t, llm.baseURL())

	res := resultOf(t, h.callOK(MethodPermissionsGet, nil))
	if got, want := res["path"], anvil.PermissionRulesPath(dir); got != want {
		t.Errorf("path = %v, want %v", got, want)
	}
	if exists, _ := res["exists"].(bool); exists {
		t.Errorf("exists = true for a workspace with no rule file")
	}
	if problem, ok := res["problem"]; ok && problem != nil {
		t.Errorf("a missing file is not a problem: %v", problem)
	}
	if rules, _ := res["rules"].([]any); len(rules) != 0 {
		t.Errorf("rules = %v, want none", rules)
	}
}

func TestPermissionsGetReadsTheFile(t *testing.T) {
	llm := newStubLLM(t)
	h, dir := initHost(t, llm.baseURL())
	writeRules(t, dir, `{"version":1,"rules":[
		{"effect":"allow","tool":"shell","pattern":"readonly","reason":"只读命令"},
		{"effect":"deny","tool":"shell","pattern":"rm -rf:*","reason":"危险删除"}
	]}`)

	res := resultOf(t, h.callOK(MethodPermissionsGet, nil))
	if exists, _ := res["exists"].(bool); !exists {
		t.Fatalf("exists = false for a file that is there: %v", res)
	}
	rules, _ := res["rules"].([]any)
	if len(rules) != 2 {
		t.Fatalf("rules = %v, want 2", rules)
	}
	first, _ := rules[0].(map[string]any)
	// Position is what a rule's identity is: the number a denial quotes has to be
	// the number a reader counts in the file, and it is one-based.
	if got, _ := first["index"].(float64); int(got) != 1 {
		t.Errorf("first rule index = %v, want 1", first["index"])
	}
	if first["pattern"] != "readonly" || first["reason"] != "只读命令" {
		t.Errorf("first rule = %v", first)
	}
	// The description is the SDK's own wording, so a rules list and a denial name
	// the same rule the same way.
	if d, _ := first["describe"].(string); !strings.Contains(d, "permissions.json rule 1") || !strings.Contains(d, "只读命令") {
		t.Errorf("describe = %q", d)
	}
	second, _ := rules[1].(map[string]any)
	if d, _ := second["describe"].(string); !strings.Contains(d, `deny shell "rm -rf:*"`) {
		t.Errorf("describe = %q", d)
	}
}

// A file somebody is halfway through editing is reported with its place, and it
// does not take the host down: the client's job with it is to send the user to
// the line.
func TestPermissionsGetReportsAMalformedFileWithoutFailing(t *testing.T) {
	llm := newStubLLM(t)
	h, dir := initHost(t, llm.baseURL())
	writeRules(t, dir, "{\n  \"version\": 1,\n  \"rules\": [\n    {\"effect\": \"allow\", \"tool\": \"shell\",}\n  ]\n}\n")

	res := resultOf(t, h.callOK(MethodPermissionsGet, nil))
	if exists, _ := res["exists"].(bool); !exists {
		t.Errorf("exists = false for a file that is there")
	}
	problem, ok := res["problem"].(map[string]any)
	if !ok {
		t.Fatalf("a file that will not compile is reported: %v", res)
	}
	if line, _ := problem["line"].(float64); int(line) != 4 {
		t.Errorf("problem line = %v, want 4 (%v)", problem["line"], problem)
	}
	if msg, _ := problem["message"].(string); msg == "" {
		t.Errorf("problem carries no message: %v", problem)
	}

	// The host is still answering, and a fixed file reads cleanly.
	writeRules(t, dir, `{"version":1,"rules":[{"effect":"allow","tool":"shell","pattern":"readonly"}]}`)
	res = resultOf(t, h.callOK(MethodPermissionsGet, nil))
	if _, ok := res["problem"]; ok {
		t.Errorf("the fixed file still reports a problem: %v", res)
	}
	if rules, _ := res["rules"].([]any); len(rules) != 1 {
		t.Errorf("rules = %v, want the one just written", rules)
	}
}

// --- amending it from a card -------------------------------------------------

// The promise of "always allow" is that it covers the call in front of you. So
// the host evaluates the file it just wrote against that very call and answers
// the pending approval itself — the run moves on with no approval.response.
func TestPermissionsAddWritesARuleAndAnswersThePendingApproval(t *testing.T) {
	llm := newStubLLM(t,
		toolTurn("call_1", "shell", `{"command":"git status"}`),
		textTurn("done"),
	)
	h, dir := initHost(t, llm.baseURL())
	sessionID := startSession(t, h, SessionParams{})

	var added map[string]any
	out := promptRun(t, h, sessionID, "look around", func(req map[string]any) map[string]any {
		added = resultOf(t, h.callOK(MethodPermissionsAdd, PermissionsAddParams{
			Rule:      RuleSpec{Tool: "shell", Pattern: "git status:*"},
			RequestID: req["request_id"].(string),
		}))
		// Deliberately no approval.response: the host is expected to settle it.
		return nil
	})

	if approved, _ := added["approved"].(bool); !approved {
		t.Fatalf("the host did not answer the approval: %v", added)
	}
	if got, _ := added["index"].(float64); int(got) != 1 {
		t.Errorf("index = %v, want 1", added["index"])
	}
	decided, _ := added["decided_by"].(map[string]any)
	if decided["effect"] != "allow" || decided["pattern"] != "git status:*" {
		t.Errorf("decided_by = %v", decided)
	}
	// The rule is in the file, in a shape the SDK loads.
	if body := readRules(t, dir); !strings.Contains(body, `"git status:*"`) {
		t.Fatalf("the rule is not in the file:\n%s", body)
	}
	if rules, err := anvil.LoadPermissionRules(dir); err != nil || len(rules) != 1 {
		t.Fatalf("the file the host wrote does not load: %v %v", rules, err)
	}
	// And the call it was written for ran.
	if status := paramsOf(t, out.done)["status"]; status != "finished" {
		t.Fatalf("the run did not finish: %v", paramsOf(t, out.done))
	}
	results := toolResultsOf(t, out.events)
	if len(results) != 1 {
		t.Fatalf("want one tool result, got %v", results)
	}
	// This call carries no rule, and that is the honest answer: the rule was
	// written after the registry had already decided to ask, so what let it run
	// was the user's approval — not a rule. The next call is the one the rule
	// decides.
	if r, ok := results[0]["rule"]; ok {
		t.Errorf("the approved call was decided by the user, not by the rule: %v", r)
	}
}

// The next call is not asked about either: the rule is read again per call, so
// "always allow" does not mean "always allow, after you reload".
func TestAWrittenRuleCoversTheNextCallToo(t *testing.T) {
	llm := newStubLLM(t,
		toolTurn("call_1", "shell", `{"command":"git status"}`),
		toolTurn("call_2", "shell", `{"command":"git status --short"}`),
		textTurn("done"),
	)
	h, _ := initHost(t, llm.baseURL())
	sessionID := startSession(t, h, SessionParams{})

	var asked int
	out := promptRun(t, h, sessionID, "look around twice", func(req map[string]any) map[string]any {
		asked++
		h.callOK(MethodPermissionsAdd, PermissionsAddParams{
			Rule:      RuleSpec{Tool: "shell", Pattern: "git status:*"},
			RequestID: req["request_id"].(string),
		})
		return nil
	})
	if asked != 1 {
		t.Fatalf("the second call was asked too: %d approvals", asked)
	}
	if results := toolResultsOf(t, out.events); len(results) != 2 {
		t.Fatalf("want two tool results, got %v", results)
	}
}

// A rule that does not decide the call must not make the card disappear: the
// pending approval is left for the user, and the host says what stood in the
// way. An ask rule outranks a later allow, wherever in the file each one sits.
func TestARuleThatLosesToAnAskDoesNotSettleTheCall(t *testing.T) {
	llm := newStubLLM(t,
		toolTurn("call_1", "shell", `{"command":"git status"}`),
		textTurn("done"),
	)
	h, dir := initHost(t, llm.baseURL())
	writeRules(t, dir, `{"version":1,"rules":[{"effect":"ask","tool":"shell","pattern":"git status:*","reason":"问一下"}]}`)
	sessionID := startSession(t, h, SessionParams{})

	var added map[string]any
	out := promptRun(t, h, sessionID, "look around", func(req map[string]any) map[string]any {
		added = resultOf(t, h.callOK(MethodPermissionsAdd, PermissionsAddParams{
			Rule:      RuleSpec{Tool: "shell", Pattern: "git status"},
			RequestID: req["request_id"].(string),
		}))
		// The host did not settle it, so the user's answer is still needed.
		return map[string]any{"decision": DecisionAllow}
	})

	if approved, _ := added["approved"].(bool); approved {
		t.Fatalf("the host settled a call its policy still asks about: %v", added)
	}
	decided, _ := added["decided_by"].(map[string]any)
	if decided == nil || decided["effect"] != "ask" || decided["reason"] != "问一下" {
		t.Fatalf("decided_by = %v, want the ask rule", decided)
	}
	if len(out.approvals) != 1 {
		t.Fatalf("want the card to have stayed: %v", out.approvals)
	}
	// The rule was still written: the user asked for the file to change.
	if body := readRules(t, dir); !strings.Contains(body, `"git status"`) {
		t.Errorf("the rule was not written:\n%s", body)
	}
}

// A session that was created with load_permissions off does not read the file,
// so a rule written into it would change nothing about the call being approved.
// The host refuses rather than answering a question with a promise it cannot
// keep — and offers no remember buttons in the first place.
func TestASessionThatIgnoresTheFileOffersNoRememberOptions(t *testing.T) {
	llm := newStubLLM(t,
		toolTurn("call_1", "shell", `{"command":"git status"}`),
		textTurn("done"),
	)
	h, dir := initHost(t, llm.baseURL())
	off := false
	sessionID := startSession(t, h, SessionParams{LoadPermissions: &off})

	var addErr *RPCError
	promptRun(t, h, sessionID, "look around", func(req map[string]any) map[string]any {
		if _, ok := req["remember"]; ok {
			t.Errorf("a session that ignores the rule file offers a remember option: %v", req["remember"])
		}
		m := h.call(MethodPermissionsAdd, PermissionsAddParams{
			Rule:      RuleSpec{Tool: "shell", Pattern: "git status:*"},
			RequestID: req["request_id"].(string),
		})
		addErr, _ = rpcErrorOf(m)
		return map[string]any{"decision": DecisionDeny}
	})

	if addErr == nil {
		t.Fatal("permissions.add must be refused when the session ignores the file")
	}
	if _, err := os.Stat(anvil.PermissionRulesPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("a refused add must not create the file: %v", err)
	}
}

// Appending to a file that cannot be read is the one thing an append must never
// do: it would replace policy nobody could see.
func TestPermissionsAddRefusesAMalformedFile(t *testing.T) {
	llm := newStubLLM(t)
	h, dir := initHost(t, llm.baseURL())
	broken := `{"version":1,"rules":[{"effect":"allow","tool":"shell",}]}`
	writeRules(t, dir, broken)

	m := h.call(MethodPermissionsAdd, PermissionsAddParams{Rule: RuleSpec{Tool: "shell", Pattern: "ls:*"}})
	rpcErr, ok := rpcErrorOf(m)
	if !ok {
		t.Fatalf("appending to a broken file must fail: %v", m)
	}
	if !strings.Contains(rpcErr.Message, "permissions.json") || !strings.Contains(rpcErr.Message, "line ") {
		t.Errorf("the error must name the file and the place: %q", rpcErr.Message)
	}
	if got := readRules(t, dir); got != broken {
		t.Fatalf("the file must be untouched:\n%s", got)
	}
}

// A rule can also be written with no card behind it, which is what a rules-file
// editor does: nothing is settled, and the result says nothing was.
func TestPermissionsAddWithoutAPendingApproval(t *testing.T) {
	llm := newStubLLM(t)
	h, dir := initHost(t, llm.baseURL())

	res := resultOf(t, h.callOK(MethodPermissionsAdd, PermissionsAddParams{
		Rule: RuleSpec{Effect: "deny", Tool: "shell", Pattern: "rm -rf:*", Reason: "危险删除"},
	}))
	if _, ok := res["approved"]; ok {
		t.Errorf("nothing was pending, so nothing was approved: %v", res)
	}
	if _, ok := res["decided_by"]; ok {
		t.Errorf("nothing was pending, so no call was decided: %v", res)
	}
	if got, want := res["path"], anvil.PermissionRulesPath(dir); got != want {
		t.Errorf("path = %v, want %v", got, want)
	}
	rules, err := anvil.LoadPermissionRules(dir)
	if err != nil || len(rules) != 1 || rules[0].Effect != "deny" || rules[0].Reason != "危险删除" {
		t.Fatalf("the file does not hold the rule that was asked for: %v %v", rules, err)
	}
}

// A request id for a card nobody is looking at must not settle anything, and
// must not be confused with a malformed request.
func TestPermissionsAddWithAStaleRequestID(t *testing.T) {
	llm := newStubLLM(t)
	h, _ := initHost(t, llm.baseURL())

	m := h.call(MethodPermissionsAdd, PermissionsAddParams{
		Rule:      RuleSpec{Tool: "shell", Pattern: "ls:*"},
		RequestID: "appr-nobody",
	})
	rpcErr, ok := rpcErrorOf(m)
	if !ok || rpcErr.Code != CodeNoApproval {
		t.Fatalf("want CodeNoApproval, got %v", m)
	}
}

// --- what the card is told ---------------------------------------------------

// The card is offered exactly the choices that would decide this call, each one
// proven by the SDK against the call itself.
func TestApprovalCarriesTheChoicesThatWouldDecideTheCall(t *testing.T) {
	llm := newStubLLM(t,
		toolTurn("call_1", "shell", `{"command":"git status -s"}`),
		textTurn("done"),
	)
	h, _ := initHost(t, llm.baseURL())
	sessionID := startSession(t, h, SessionParams{})

	var kinds map[string]string
	promptRun(t, h, sessionID, "look around", func(req map[string]any) map[string]any {
		kinds = rememberKinds(t, req)
		if _, ok := req["rule"]; ok {
			t.Errorf("the mode gate asked, not a rule: %v", req["rule"])
		}
		return map[string]any{"decision": DecisionAllow}
	})

	want := map[string]string{"exact": "git status -s", "prefix": "git status:*", "readonly": "readonly"}
	if len(kinds) != len(want) {
		t.Fatalf("remember options = %v, want %v", kinds, want)
	}
	for kind, pattern := range want {
		if kinds[kind] != pattern {
			t.Errorf("kind %q = %q, want %q (all: %v)", kind, kinds[kind], pattern, kinds)
		}
	}
}

// A call no rule shape can honestly describe is asked about every time and
// offered nothing: a button that writes a rule which does nothing teaches the
// user that rules do not work.
func TestACompoundCommandIsOfferedNothingToRemember(t *testing.T) {
	llm := newStubLLM(t,
		toolTurn("call_1", "shell", `{"command":"git status && rm -rf build"}`),
		textTurn("done"),
	)
	h, _ := initHost(t, llm.baseURL())
	sessionID := startSession(t, h, SessionParams{})

	promptRun(t, h, sessionID, "look around", func(req map[string]any) map[string]any {
		if kinds := rememberKinds(t, req); len(kinds) != 0 {
			t.Errorf("a line of two commands cannot be remembered as one: %v", kinds)
		}
		return map[string]any{"decision": DecisionAllow}
	})
}

// The other direction of the same fact: a call that ran without being asked says
// which rule allowed it, so "it just ran" is never mistaken for "nobody was
// watching".
func TestAToolResultNamesTheRuleThatAllowedIt(t *testing.T) {
	llm := newStubLLM(t,
		toolTurn("call_1", "shell", `{"command":"git status"}`),
		textTurn("done"),
	)
	h, dir := initHost(t, llm.baseURL())
	writeRules(t, dir, `{"version":1,"rules":[{"effect":"allow","tool":"shell","pattern":"readonly","reason":"只读命令"}]}`)
	sessionID := startSession(t, h, SessionParams{})

	out := promptRun(t, h, sessionID, "look around", autoApprove)
	if len(out.approvals) != 0 {
		t.Fatalf("a rule allowed it, so nobody should have been asked: %v", out.approvals)
	}
	results := toolResultsOf(t, out.events)
	if len(results) != 1 {
		t.Fatalf("want one tool result, got %v", results)
	}
	rule, _ := results[0]["rule"].(map[string]any)
	if rule == nil || rule["effect"] != "allow" || rule["pattern"] != "readonly" {
		t.Fatalf("rule = %v", rule)
	}
	if d, _ := rule["describe"].(string); !strings.Contains(d, "permissions.json rule 1") {
		t.Errorf("describe = %q", d)
	}
	// A call no rule decided carries no rule: the client must not invent one.
	llm2 := newStubLLM(t, toolTurn("call_1", "shell", `{"command":"rm -rf build"}`), textTurn("done"))
	h2, _ := initHost(t, llm2.baseURL())
	sid2 := startSession(t, h2, SessionParams{})
	out2 := promptRun(t, h2, sid2, "clean", autoApprove)
	results2 := toolResultsOf(t, out2.events)
	if len(results2) != 1 {
		t.Fatalf("want one tool result, got %v", results2)
	}
	if r, ok := results2[0]["rule"]; ok {
		t.Errorf("no rule decided rm -rf build, got %v", r)
	}
}

// A prompt forced by a rule says which rule, so a card can explain why it
// appeared at all.
func TestAnApprovalNamesTheRuleThatAsked(t *testing.T) {
	llm := newStubLLM(t,
		toolTurn("call_1", "write_file", `{"path":"a.txt","content":"hi"}`),
		textTurn("done"),
	)
	h, dir := initHost(t, llm.baseURL())
	writeRules(t, dir, `{"version":1,"rules":[{"effect":"ask","tool":"write_file","reason":"问一下"}]}`)
	sessionID := startSession(t, h, SessionParams{})

	var rule map[string]any
	promptRun(t, h, sessionID, "write", func(req map[string]any) map[string]any {
		rule, _ = req["rule"].(map[string]any)
		return map[string]any{"decision": DecisionAllow}
	})

	if rule == nil || rule["effect"] != "ask" || rule["reason"] != "问一下" {
		t.Fatalf("rule = %v", rule)
	}
	if d, _ := rule["describe"].(string); !strings.Contains(d, "permissions.json rule 1") {
		t.Errorf("describe = %q", d)
	}
}

// A client that does not know about rules must still be able to tell which host
// it is talking to.
func TestCapabilitiesAnnouncePermissions(t *testing.T) {
	llm := newStubLLM(t)
	dir := newWorkspace(t)
	h := startHarness(t, Config{Provider: string(anvil.ProviderOpenAI), BaseURL: llm.baseURL(), Model: "stub-model"})
	res := resultOf(t, h.callOK(MethodInitialize, InitializeParams{Protocol: ProtocolVersion, CWD: dir}))
	caps, _ := res["capabilities"].([]any)
	for _, raw := range caps {
		if s, _ := raw.(string); s == "permissions" {
			return
		}
	}
	t.Fatalf("capabilities = %v, want permissions", caps)
}

// json.RawMessage in, wire shape out: the args a remember option is proven
// against are the args the card showed.
func TestRememberOptionsFollowTheArgsNotThePreview(t *testing.T) {
	llm := newStubLLM(t,
		toolTurn("call_1", "write_file", `{"path":"src/a.go","content":"package a\n"}`),
		textTurn("done"),
	)
	h, _ := initHost(t, llm.baseURL())
	sessionID := startSession(t, h, SessionParams{})

	var kinds map[string]string
	promptRun(t, h, sessionID, "write", func(req map[string]any) map[string]any {
		kinds = rememberKinds(t, req)
		args, ok := req["args"].(map[string]any)
		if !ok || args["path"] != "src/a.go" {
			t.Errorf("the args on the card are not the ones the option was proven against: %v", req["args"])
		}
		return map[string]any{"decision": DecisionAllow}
	})
	if kinds["path"] != "src/a.go" {
		t.Fatalf("remember options = %v, want the path the call touches", kinds)
	}
}
