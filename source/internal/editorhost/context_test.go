// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package editorhost

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 1x1 transparent PNG: small enough to inline, real enough for the vision path
// to decode. A made-up byte string would fail inside the SDK and turn this into
// a test of the image decoder instead of a test of the plumbing.
const tinyPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="

// lastUserText returns the text of the user turn the stub LLM was actually
// sent, decoded from the request body rather than grepped out of it: the point
// is what the model reads, and JSON escaping is noise on the way there.
func lastUserText(t *testing.T, body string) string {
	t.Helper()
	var parsed struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		t.Fatalf("request body is not a chat request: %v", err)
	}
	for i := len(parsed.Messages) - 1; i >= 0; i-- {
		if parsed.Messages[i].Role != "user" {
			continue
		}
		var text string
		if err := json.Unmarshal(parsed.Messages[i].Content, &text); err == nil {
			return text
		}
		// Multi-part content: keep the text parts and mark where images sit.
		var parts []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal(parsed.Messages[i].Content, &parts); err != nil {
			t.Fatalf("user content is neither text nor parts: %s", parsed.Messages[i].Content)
		}
		var b strings.Builder
		for _, p := range parts {
			if p.Type == "text" {
				b.WriteString(p.Text)
			} else {
				b.WriteString("[image]")
			}
		}
		return b.String()
	}
	t.Fatal("the request carried no user message")
	return ""
}

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// --- composition ------------------------------------------------------------

func TestPromptComposesMentionsBeforeTheUserMessage(t *testing.T) {
	llm := newStubLLM(t, textTurn("ok"))
	h, dir := initHost(t, llm.baseURL())
	writeFile(t, dir, "a.txt", "alpha\nbeta\n")
	writeFile(t, dir, "pkg/b.go", "package pkg\n")

	sessionID := startSession(t, h, SessionParams{})
	h.callOK(MethodPrompt, PromptParams{
		SessionID: sessionID,
		Message:   "what does a.txt do?",
		Context: &PromptContext{
			Mentions: []Mention{{Path: "a.txt"}, {Path: "pkg"}},
		},
	})
	h.collectRunOK(15 * time.Second)

	text := lastUserText(t, llm.lastRequestBody(t))
	if !strings.Contains(text, "alpha\nbeta") {
		t.Fatalf("the mentioned file's body did not reach the model:\n%s", text)
	}
	if !strings.Contains(text, "b.go") {
		t.Fatalf("a folder mention must list its entries:\n%s", text)
	}
	// The request comes last, immediately after the material it is about.
	if ask, mention := strings.Index(text, "what does a.txt do?"), strings.Index(text, "<mentions>"); ask < mention {
		t.Fatalf("the user message must follow the context block:\n%s", text)
	}
	if !strings.HasSuffix(strings.TrimSpace(text), "what does a.txt do?") {
		t.Fatalf("unexpected tail of the composed message:\n%s", text)
	}
}

// Plan mode's directive already prefixes the message; the context block goes
// between it and the request, which is the order the design fixes.
func TestPlanModeKeepsTheDirectiveFirst(t *testing.T) {
	llm := newStubLLM(t, textTurn("a plan"))
	h, dir := initHost(t, llm.baseURL())
	writeFile(t, dir, "a.txt", "alpha\n")
	sessionID := startSession(t, h, SessionParams{PermissionMode: ModePlan})

	h.callOK(MethodPrompt, PromptParams{
		SessionID: sessionID,
		Message:   "add a flag",
		Context:   &PromptContext{Mentions: []Mention{{Path: "a.txt"}}},
	})
	h.collectRunOK(15 * time.Second)

	text := lastUserText(t, llm.lastRequestBody(t))
	directive := strings.Index(text, "Plan mode is on.")
	context := strings.Index(text, "<mentions>")
	ask := strings.Index(text, "add a flag")
	if directive < 0 || context < 0 || ask < 0 {
		t.Fatalf("missing a piece of the composed message:\n%s", text)
	}
	if !(directive < context && context < ask) {
		t.Fatalf("want directive, context, request in that order:\n%s", text)
	}
}

func TestPromptWithoutContextIsUnchanged(t *testing.T) {
	llm := newStubLLM(t, textTurn("ok"))
	h, _ := initHost(t, llm.baseURL())
	sessionID := startSession(t, h, SessionParams{})

	h.callOK(MethodPrompt, PromptParams{SessionID: sessionID, Message: "hello"})
	h.collectRunOK(15 * time.Second)

	if text := lastUserText(t, llm.lastRequestBody(t)); text != "hello" {
		t.Fatalf("a prompt with no context must reach the model verbatim, got %q", text)
	}
}

// --- selection and diagnostics ----------------------------------------------

func TestSelectionAndDiagnosticsReachTheModel(t *testing.T) {
	llm := newStubLLM(t, textTurn("ok"))
	h, _ := initHost(t, llm.baseURL())
	sessionID := startSession(t, h, SessionParams{})

	diags := make([]Diagnostic, 0, 150)
	for i := 0; i < 150; i++ {
		diags = append(diags, Diagnostic{Path: "src/a.ts", Line: i + 1, Severity: "warning", Message: "unused"})
	}
	h.callOK(MethodPrompt, PromptParams{
		SessionID: sessionID,
		Message:   "fix these",
		Context: &PromptContext{
			Selection: &Selection{
				Path:  "src/a.ts",
				Range: Range{StartLine: 3, StartColumn: 1, EndLine: 5, EndColumn: 10},
				Text:  "const x = 1\n",
			},
			Diagnostics: diags,
		},
	})
	h.collectRunOK(15 * time.Second)

	text := lastUserText(t, llm.lastRequestBody(t))
	if !strings.Contains(text, `path="src/a.ts" lines="3:1-5:10"`) {
		t.Fatalf("the selection was not labelled with its range:\n%s", text)
	}
	if !strings.Contains(text, "const x = 1") {
		t.Fatalf("the selected text did not reach the model:\n%s", text)
	}
	// A broken file must not be allowed to fill the context, and the model is
	// told the list is partial rather than being handed a silent cut.
	if !strings.Contains(text, `<diagnostics count="100">`) {
		t.Fatalf("diagnostics were not capped:\n%s", firstLines(text, 6))
	}
	if !strings.Contains(text, "(50 more not shown)") {
		t.Fatalf("the dropped diagnostics were not announced:\n%s", text)
	}
	if strings.Count(text, "unused") != 100 {
		t.Fatalf("want 100 diagnostic lines, got %d", strings.Count(text, "unused"))
	}
}

// A selection is text the client already holds, so it is sent as given: an
// editor legitimately selects from a file outside the workspace root, and
// refusing that would break an ordinary editing session.
func TestSelectionOutsideTheWorkspaceIsStillSent(t *testing.T) {
	llm := newStubLLM(t, textTurn("ok"))
	h, dir := initHost(t, llm.baseURL())
	outside := filepath.Join(filepath.Dir(dir), "outside.txt")
	sessionID := startSession(t, h, SessionParams{})

	h.callOK(MethodPrompt, PromptParams{
		SessionID: sessionID,
		Message:   "explain this",
		Context: &PromptContext{Selection: &Selection{
			Path:  outside,
			Range: Range{StartLine: 1, StartColumn: 1, EndLine: 1, EndColumn: 5},
			Text:  "hello",
		}},
	})
	h.collectRunOK(15 * time.Second)

	if text := lastUserText(t, llm.lastRequestBody(t)); !strings.Contains(text, "hello") {
		t.Fatalf("the selected text did not reach the model:\n%s", text)
	}
}

// --- caps -------------------------------------------------------------------

func TestMentionsAreCapped(t *testing.T) {
	llm := newStubLLM(t, textTurn("ok"))
	h, dir := initHost(t, llm.baseURL())
	writeFile(t, dir, "a.txt", "alpha\n")
	sessionID := startSession(t, h, SessionParams{})

	mentions := make([]Mention, 40)
	for i := range mentions {
		mentions[i] = Mention{Path: "a.txt"}
	}
	h.callOK(MethodPrompt, PromptParams{
		SessionID: sessionID,
		Message:   "look at all of these",
		Context:   &PromptContext{Mentions: mentions},
	})
	h.collectRunOK(15 * time.Second)

	text := lastUserText(t, llm.lastRequestBody(t))
	if got := strings.Count(text, "--- a.txt"); got != maxMentions {
		t.Fatalf("want %d mentions kept, got %d", maxMentions, got)
	}
	if !strings.Contains(text, "8 further referenced paths were left out") {
		t.Fatalf("the dropped mentions were not announced:\n%s", firstLines(text, 3))
	}
}

func TestALargeMentionIsTruncatedAndSaysSo(t *testing.T) {
	llm := newStubLLM(t, textTurn("ok"))
	h, dir := initHost(t, llm.baseURL())
	body := strings.Repeat("x", 100<<10)
	writeFile(t, dir, "big.txt", body)
	sessionID := startSession(t, h, SessionParams{})

	h.callOK(MethodPrompt, PromptParams{
		SessionID: sessionID,
		Message:   "summarise this",
		Context:   &PromptContext{Mentions: []Mention{{Path: "big.txt"}}},
	})
	h.collectRunOK(15 * time.Second)

	text := lastUserText(t, llm.lastRequestBody(t))
	// Naming both numbers matters: it tells the model what it is missing.
	if !strings.Contains(text, "of 102400 bytes") {
		t.Fatalf("the truncation was not announced with the real size:\n%s", firstLines(text, 3))
	}
	if len(text) > maxMentionBytes+4096 {
		t.Fatalf("a capped mention carried %d bytes", len(text))
	}
}

// --- containment ------------------------------------------------------------

// The client is untrusted in the sense that a bug there must not become "read
// anything on disk into the model's context". Each escape is refused, with its
// own reason, before the prompt reaches the model at all.
func TestMentionEscapesAreRefused(t *testing.T) {
	llm := newStubLLM(t, textTurn("ok"))
	h, dir := initHost(t, llm.baseURL())
	writeFile(t, dir, "inside.txt", "inside\n")

	outside := filepath.Join(filepath.Dir(dir), "outside.txt")
	if err := os.WriteFile(outside, []byte("secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	link := filepath.Join(dir, "link.txt")
	haveLink := os.Symlink(outside, link) == nil
	if !haveLink {
		t.Log("symlinks are unavailable here; the link case is skipped")
	}

	cases := []struct {
		name string
		path string
		want string
	}{
		{"parent traversal", "../outside.txt", "escapes the workspace"},
		{"absolute", outside, "workspace-relative"},
		{"empty", "  ", "empty"},
	}
	if haveLink {
		cases = append(cases, struct{ name, path, want string }{
			"symlink out", "link.txt", "link",
		})
	}

	sessionID := startSession(t, h, SessionParams{})
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := h.call(MethodPrompt, PromptParams{
				SessionID: sessionID,
				Message:   "read this",
				Context:   &PromptContext{Mentions: []Mention{{Path: tc.path}}},
			})
			rpcErr, ok := rpcErrorOf(m)
			if !ok {
				t.Fatalf("a path escape was accepted: %v", m)
			}
			if rpcErr.Code != CodeInvalidParams {
				t.Fatalf("want %d, got %d: %s", CodeInvalidParams, rpcErr.Code, rpcErr.Message)
			}
			if !strings.Contains(rpcErr.Message, tc.want) {
				t.Fatalf("message %q does not explain the refusal (%q)", rpcErr.Message, tc.want)
			}
		})
	}

	// Nothing may have been read: the refusal is cheap on purpose.
	if calls := llm.callCount(); calls != 0 {
		t.Fatalf("a refused mention still reached the model (%d calls)", calls)
	}
}

func TestMentionOfAMissingFileIsRefused(t *testing.T) {
	llm := newStubLLM(t, textTurn("ok"))
	h, _ := initHost(t, llm.baseURL())
	sessionID := startSession(t, h, SessionParams{})

	// Silently skipping it would leave the model answering about a file it
	// never saw, which reads as a model failure rather than a client bug.
	m := h.call(MethodPrompt, PromptParams{
		SessionID: sessionID,
		Message:   "read this",
		Context:   &PromptContext{Mentions: []Mention{{Path: "nope.txt"}}},
	})
	if _, ok := rpcErrorOf(m); !ok {
		t.Fatalf("a missing mention was accepted: %v", m)
	}
	if calls := llm.callCount(); calls != 0 {
		t.Fatalf("the prompt ran anyway (%d model calls)", calls)
	}
}

// --- attachments ------------------------------------------------------------

func TestAttachmentsRequireAVisionSession(t *testing.T) {
	llm := newStubLLM(t, textTurn("ok"))
	h, _ := initHost(t, llm.baseURL())
	sessionID := startSession(t, h, SessionParams{})

	m := h.call(MethodPrompt, PromptParams{
		SessionID:   sessionID,
		Message:     "what is this?",
		Attachments: []Attachment{{Data: tinyPNG, MIME: "image/png"}},
	})
	rpcErr, ok := rpcErrorOf(m)
	if !ok {
		t.Fatalf("an attachment to a non-vision session was accepted: %v", m)
	}
	if rpcErr.Code != CodeInvalidParams || !strings.Contains(rpcErr.Message, "vision: true") {
		// The error has to name the setting: a run that quietly ignored the
		// image would look like a model that cannot see.
		t.Fatalf("unhelpful refusal: %d %s", rpcErr.Code, rpcErr.Message)
	}
}

func TestPastedImageRidesTheVisionPath(t *testing.T) {
	llm := newStubLLM(t, textTurn("a red square"))
	h, _ := initHost(t, llm.baseURL())
	sessionID := startSession(t, h, SessionParams{Vision: true})

	h.callOK(MethodPrompt, PromptParams{
		SessionID:   sessionID,
		Message:     "what is this?",
		Attachments: []Attachment{{Data: tinyPNG, MIME: "image/png", Detail: "high"}},
	})
	h.collectRunOK(15 * time.Second)

	body := llm.lastRequestBody(t)
	if !strings.Contains(body, "image_url") || !strings.Contains(body, "data:image/png;base64,") {
		t.Fatalf("the image did not reach the model:\n%s", firstLines(body, 3))
	}
	if text := lastUserText(t, body); !strings.Contains(text, "what is this?") {
		t.Fatalf("the text did not travel with the image: %q", text)
	}
}

func TestAttachmentDataMustBeBase64(t *testing.T) {
	llm := newStubLLM(t, textTurn("ok"))
	h, _ := initHost(t, llm.baseURL())
	sessionID := startSession(t, h, SessionParams{Vision: true})

	m := h.call(MethodPrompt, PromptParams{
		SessionID:   sessionID,
		Message:     "what is this?",
		Attachments: []Attachment{{Data: "not base64!!"}},
	})
	rpcErr, ok := rpcErrorOf(m)
	if !ok || rpcErr.Code != CodeInvalidParams {
		t.Fatalf("malformed attachment data was accepted: %v", m)
	}
	if !strings.Contains(rpcErr.Message, "base64") {
		t.Fatalf("unhelpful refusal: %s", rpcErr.Message)
	}
}

func TestAttachmentWithoutDataOrPathIsRefused(t *testing.T) {
	llm := newStubLLM(t, textTurn("ok"))
	h, _ := initHost(t, llm.baseURL())
	sessionID := startSession(t, h, SessionParams{Vision: true})

	m := h.call(MethodPrompt, PromptParams{
		SessionID:   sessionID,
		Message:     "what is this?",
		Attachments: []Attachment{{MIME: "image/png"}},
	})
	rpcErr, ok := rpcErrorOf(m)
	if !ok || rpcErr.Code != CodeInvalidParams || !strings.Contains(rpcErr.Message, "path or data") {
		t.Fatalf("an empty attachment was accepted: %v", m)
	}
}

// firstLines keeps a failure message readable when the subject is a whole file.
func firstLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}
