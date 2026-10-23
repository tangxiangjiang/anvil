// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package eval

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/anvil-dev/anvil/internal/llm"
)

const systemIntro = "You are Anvil, a local coding agent working in the directory:\n"

var (
	noteTimestamp = regexp.MustCompile(`\b\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})\b`)
	spawnSummary  = regexp.MustCompile(`^(agent-[0-9a-f]{16})( finished in [0-9]+ steps\.| failed: | was cancelled: )`)
	workflowRow   = regexp.MustCompile(`^([^\r\n]+?  (?:finished|completed|succeeded|failed|cancelled|blocked|skipped|pending|running)[ ]+)(agent-[0-9a-f]{16}|—)([ ]{2,}[^\r\n]*)$`)
	windowsTemp   = regexp.MustCompile(`(?i)^[a-z]:/(?:users/[^/]+/appdata/local/temp|windows/temp|temp)/`)
	macTemp       = regexp.MustCompile(`^/private/var/folders/[^/]+/[^/]+/T/`)
)

// RequestDigest hashes CanonicalRequest. Invalid JSON in a typed request yields
// an empty digest; callers that need diagnostics should use CanonicalRequest.
func RequestDigest(req llm.Request) string {
	b, err := CanonicalRequest(req)
	if err != nil {
		return ""
	}
	return digestCanonical(b)
}

func digestCanonical(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// CanonicalRequest copies the request through JSON before touching any text.
// Only generated, format-specific runtime metadata is normalized. User text,
// schemas, cache policy, and tool arguments retain their meaning.
func CanonicalRequest(req llm.Request) ([]byte, error) {
	b, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	var copied llm.Request
	if err := json.Unmarshal(b, &copied); err != nil {
		return nil, fmt.Errorf("copy request: %w", err)
	}
	copied.Model = ""
	sort.SliceStable(copied.Tools, func(i, j int) bool { return copied.Tools[i].Name < copied.Tools[j].Name })
	var roots []string
	for i := range copied.Messages {
		m := &copied.Messages[i]
		if m.Role != llm.RoleSystem {
			continue
		}
		if strings.HasPrefix(m.Content, systemIntro) {
			rest := strings.TrimPrefix(m.Content, systemIntro)
			if end := strings.IndexByte(rest, '\n'); end >= 0 && end > 0 {
				cwd := rest[:end]
				if temporaryWorkspace(cwd) {
					roots = append(roots, cwd)
				}
				m.Content = systemIntro + "<workspace>" + rest[end:]
			}
		}
		m.Content = normalizeNotes(m.Content)
	}
	// Name is optional on tool results; otherwise recover it from the matching
	// invocation, not by guessing from result text. Only run_tests output has
	// Go summary elapsed-time volatility (pass/fail and package stay intact).
	toolNames := make(map[string]string)
	ids := make(map[string]string)
	normalizeID := func(id string) string {
		if canonical, ok := ids[id]; ok {
			return canonical
		}
		canonical := fmt.Sprintf("agent-<%d>", len(ids)+1)
		ids[id] = canonical
		return canonical
	}
	for i := range copied.Messages {
		m := &copied.Messages[i]
		// IDs may be reused in a later turn. A result belongs to the most recent
		// preceding invocation, never a future call that happens to share its ID.
		for _, call := range m.ToolCalls {
			toolNames[call.ID] = call.Name
		}
		if m.Role != llm.RoleTool {
			continue
		}
		name := m.Name
		if name == "" && m.ToolCallID != "" {
			name = toolNames[m.ToolCallID]
		}
		normalize := func(text string) string {
			for _, root := range roots {
				text = replaceWorkspace(text, root)
			}
			// Only the generated spawn heading or initial workflow table carries
			// volatile IDs. Identical-looking file/child body text must survive.
			lines := strings.Split(text, "\n")
			table := name == "run_workflow" && strings.HasPrefix(lines[0], "workflow ")
			for j, line := range lines {
				if j == 0 && name == "spawn_agent" {
					if match := spawnSummary.FindStringSubmatchIndex(line); match != nil {
						lines[j] = line[:match[2]] + normalizeID(line[match[2]:match[3]]) + line[match[3]:]
					}
				} else if j > 0 && table {
					if match := workflowRow.FindStringSubmatchIndex(line); match != nil {
						id := line[match[4]:match[5]]
						if id != "—" {
							lines[j] = line[:match[4]] + normalizeID(id) + line[match[5]:]
						}
					} else {
						table = false
					}
				}
			}
			return strings.Join(lines, "\n")
		}
		m.Content = normalize(m.Content)
		for j := range m.Parts {
			if m.Parts[j].Type == "text" {
				m.Parts[j].Text = normalize(m.Parts[j].Text)
			}
		}
		if name == "run_tests" {
			m.Content = normalizeGoTestDuration(m.Content)
			for j := range m.Parts {
				if m.Parts[j].Type == "text" {
					m.Parts[j].Text = normalizeGoTestDuration(m.Parts[j].Text)
				}
			}
		}
	}
	// Re-encode through generic JSON to canonicalize object key order, including
	// schema and tool-call RawMessages. Arrays and all their values stay intact.
	return canonicalJSON(copied)
}

func canonicalJSON(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var tree any
	decoder := json.NewDecoder(strings.NewReader(string(b)))
	decoder.UseNumber()
	if err := decoder.Decode(&tree); err != nil {
		return nil, err
	}
	return json.Marshal(tree)
}

var goTestSummary = regexp.MustCompile(`(?m)^((?:ok|FAIL)[ \t]+[^ \t\r\n]+[ \t]+)[0-9]+(?:\.[0-9]+)?s([ \t]*)$`)

func normalizeGoTestDuration(text string) string {
	// FormatResult may copy a FAIL package summary (including its duration)
	// into the generated failed list. Normalize only entries corroborated by
	// that exact summary in logTail, not arbitrary named tests or log text.
	if strings.HasPrefix(text, "tests: ") {
		if start := strings.Index(text, "\nlogTail:\n"); start >= 0 {
			failures := make(map[string]string)
			for _, match := range goTestSummary.FindAllStringSubmatch(text[start+len("\nlogTail:\n"):], -1) {
				if !strings.HasPrefix(match[0], "FAIL") {
					continue
				}
				end := strings.IndexAny(match[1], " \t")
				name := strings.TrimSpace(match[0][end:])
				failures[name] = strings.TrimSpace(match[1][end:] + "<duration>" + match[2])
			}
			lines := strings.Split(text[:start], "\n")
			inFailed := false
			for i, line := range lines {
				if line == "failed:" {
					inFailed = true
				} else if inFailed && strings.HasPrefix(line, "  - ") {
					if normalized, ok := failures[strings.TrimPrefix(line, "  - ")]; ok {
						lines[i] = "  - " + normalized
					}
				} else {
					inFailed = false
				}
			}
			text = strings.Join(lines, "\n") + text[start:]
		}
	}
	return goTestSummary.ReplaceAllString(text, "${1}<duration>${2}")
}

// Session notes are arbitrary hook text. The sole volatile grammar here is a
// valid RFC3339/RFC3339Nano timestamp inside the exact # Session notes section;
// ordinary dates, invalid timestamps, and later project-rule sections survive.
func normalizeNotes(text string) string {
	lines := strings.Split(text, "\n")
	inNotes := false
	for i, line := range lines {
		if strings.HasPrefix(line, "# ") {
			inNotes = line == "# Session notes"
			continue
		}
		if inNotes {
			lines[i] = noteTimestamp.ReplaceAllStringFunc(line, func(stamp string) string {
				if _, err := time.Parse(time.RFC3339Nano, stamp); err == nil {
					return "<timestamp>"
				}
				return stamp
			})
		}
	}
	return strings.Join(lines, "\n")
}

func temporaryWorkspace(cwd string) bool {
	// This grammar depends on the request, not the loading machine's TempDir.
	// Nonstandard TMPDIR paths stay significant rather than producing digests
	// that cannot be verified on another machine.
	path := strings.ReplaceAll(cwd, "\\", "/")
	for _, root := range []string{"/tmp/", "/var/tmp/", "/private/tmp/"} {
		if strings.HasPrefix(path, root) {
			return true
		}
	}
	return windowsTemp.MatchString(path) || macTemp.MatchString(path)
}

func replaceWorkspace(text, root string) string {
	aliases := []string{root, strings.ReplaceAll(root, "\\", "/"), strings.ReplaceAll(root, "/", "\\")}
	for _, alias := range aliases {
		if alias == "" {
			continue
		}
		var result strings.Builder
		for offset := 0; ; {
			i := strings.Index(text[offset:], alias)
			if i < 0 {
				result.WriteString(text[offset:])
				break
			}
			i += offset
			end := i + len(alias)
			beforeOK := i == 0 || strings.ContainsRune(" \t\r\n\"'`=([{:", rune(text[i-1]))
			afterOK := end == len(text) || strings.ContainsRune("/\\ \t\r\n\"'`)]},:;", rune(text[end]))
			result.WriteString(text[offset:i])
			if beforeOK && afterOK {
				result.WriteString("<workspace>")
			} else {
				result.WriteString(alias)
			}
			offset = end
		}
		text = result.String()
	}
	return text
}

func cloneResponse(response llm.Response) (*llm.Response, error) {
	b, err := json.Marshal(response)
	if err != nil {
		return nil, fmt.Errorf("marshal response: %w", err)
	}
	var copied llm.Response
	if err := json.Unmarshal(b, &copied); err != nil {
		return nil, fmt.Errorf("copy response: %w", err)
	}
	return &copied, nil
}
