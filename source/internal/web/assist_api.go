// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// The drafting assistant (p_0.46.6).
//
// One endpoint for every surface that asks a model for wording. The contract is
// built so it cannot corrupt a record:
//
//   - The model returns **structured patches** (field + value + rationale), not
//     prose. A response that is not patches is an error, not a fallback.
//   - Each surface has an **allowlist** of fields it may propose. A patch naming
//     any other field is refused server-side, because the client cannot be trusted
//     to limit where a patch lands.
//   - Nothing is applied here. The endpoint proposes; the operator accepts; the
//     surface saves through its own PATCH. That is what makes p_0.46.1's
//     no-silent-write invariant structural rather than a note in three files.
//
// The boundary this crosses, and how: design 23 says agentd is not an LLM reverse
// proxy and the *worker* owns cognition. anvil-web calling a provider to draft
// planning text is a different act — no tools, no execution, no conversation
// state — and it is written down in doc/design/v1.0/40-web-assist.md rather than
// assumed.

// AssistField is one field a surface may ask about.
type AssistField struct {
	// Name is the patch key, matched exactly.
	Name string `json:"name"`
	// Label is what the operator sees.
	Label string `json:"label"`
	// Hint tells the model what belongs in it.
	Hint string `json:"hint"`
}

// assistSurfaces is the allowlist per surface.
//
// The hint is doing real work: it is the difference between "write me a design"
// and "write the Completion criteria, which are what the agent is told done
// means". A generic prompt is why the mocks were useless.
var assistSurfaces = map[string]struct {
	Label  string
	Fields []AssistField
}{
	"requirement_version": {
		Label: "Requirement version",
		Fields: []AssistField{
			{Name: "problem", Label: "Delivery note", Hint: "What to build and why, in the operator's words."},
			{Name: "goals", Label: "Goals", Hint: "What this version achieves, as outcomes not tasks."},
			{Name: "success", Label: "Completion criteria", Hint: "What done means, testably. This is what the agent is told."},
		},
	},
	"design": {
		Label: "Design",
		Fields: []AssistField{
			{Name: "adr_context", Label: "Context", Hint: "The situation that forces a decision."},
			{Name: "adr_decision", Label: "Decision", Hint: "What we will do, stated plainly."},
			{Name: "adr_consequences", Label: "Consequences", Hint: "What follows, including what gets harder."},
			{Name: "adr_architecture", Label: "Architecture", Hint: "Structure as text or a mermaid diagram."},
		},
	},
	"design_version": {
		Label: "Design version",
		Fields: []AssistField{
			{Name: "gate", Label: "Gate", Hint: "Markdown checklist of what must be true to start this phase, one '- [ ] item' per line."},
			{Name: "sub_architecture", Label: "Sub-architecture", Hint: "How this version fits the whole."},
			{Name: "class_design", Label: "Class design", Hint: "The types and responsibilities this version introduces."},
		},
	},
	"roadmap_polish": {
		Label: "Polish note",
		Fields: []AssistField{
			{Name: "body", Label: "Polish note", Hint: "What to polish and why it matters."},
		},
	},
	"test_case": {
		Label: "Test case",
		Fields: []AssistField{
			{Name: "body", Label: "Case", Hint: "Given / When / Then. One scenario per case."},
		},
	},
}

// AssistRequest is POST /api/v1/delivery/assist.
type AssistRequest struct {
	Surface string `json:"surface"`
	// Field limits the proposal to one field; empty means every field the surface
	// allows.
	Field       string   `json:"field,omitempty"`
	Instruction string   `json:"instruction"`
	Cites       []string `json:"cites,omitempty"`
	// Current is the text already in the field(s), so the model improves rather
	// than replaces.
	Current map[string]string `json:"current,omitempty"`
}

// AssistProposal is one proposed edit.
type AssistProposal struct {
	Field     string `json:"field"`
	Label     string `json:"label"`
	Before    string `json:"before"`
	Value     string `json:"value"`
	Rationale string `json:"rationale,omitempty"`
}

// AssistResult is the endpoint's answer.
type AssistResult struct {
	Available bool             `json:"available"`
	Reason    string           `json:"reason,omitempty"`
	Model     string           `json:"model,omitempty"`
	Proposals []AssistProposal `json:"proposals"`
	Usage     *assistUsage     `json:"usage,omitempty"`
}

type assistUsage struct {
	PromptTokens     int `json:"prompt_tokens,omitempty"`
	CompletionTokens int `json:"completion_tokens,omitempty"`
}

// handleAssist proposes field edits; it never applies them.
func (s *Server) handleAssist(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var in AssistRequest
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
		return
	}
	surface, ok := assistSurfaces[strings.TrimSpace(in.Surface)]
	if !ok {
		http.Error(w, "unknown surface: "+in.Surface, http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(in.Instruction) == "" && len(in.Cites) == 0 {
		http.Error(w, "an instruction or at least one cite is required", http.StatusBadRequest)
		return
	}

	// Fields this request may propose, filtered by the optional single-field limit.
	allowed := map[string]AssistField{}
	for _, f := range surface.Fields {
		if in.Field != "" && f.Name != in.Field {
			continue
		}
		allowed[f.Name] = f
	}
	if len(allowed) == 0 {
		http.Error(w, "field "+in.Field+" is not proposable on surface "+in.Surface, http.StatusBadRequest)
		return
	}

	// Off is stated, not faked.
	if !s.cfg.Assist.Enabled() {
		writeJSON(w, http.StatusOK, AssistResult{
			Available: false,
			Reason:    "AI assist is not configured",
			Proposals: []AssistProposal{},
		})
		return
	}

	raw, usage, model, err := s.callAssist(r.Context(), surface.Label, allowed, in)
	if err != nil {
		// A provider failure is reported as an error, never as an empty proposal
		// list: an empty list reads as "the model had no suggestions", which is a
		// different and misleading statement.
		http.Error(w, "assist: "+err.Error(), http.StatusBadGateway)
		return
	}

	proposals, perr := parseAssistProposals(raw, allowed, in.Current)
	if perr != nil {
		http.Error(w, "assist: "+perr.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, AssistResult{
		Available: true,
		Model:     model,
		Proposals: proposals,
		Usage:     usage,
	})
}

// callAssist performs the one provider request.
func (s *Server) callAssist(ctx context.Context, surfaceLabel string, allowed map[string]AssistField, in AssistRequest) (string, *assistUsage, string, error) {
	cfg := s.cfg.Assist
	timeout := time.Duration(cfg.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	body := map[string]any{
		"model":       cfg.Model,
		"temperature": 0.2,
		"messages": []map[string]string{
			{"role": "system", "content": assistSystemPrompt(surfaceLabel, allowed)},
			{"role": "user", "content": assistUserPrompt(in)},
		},
	}
	// Ask for JSON so the response is parseable patches rather than prose, without
	// relying on a provider-specific function-calling surface.
	body["response_format"] = map[string]string{"type": "json_object"}

	enc, err := json.Marshal(body)
	if err != nil {
		return "", nil, "", err
	}
	url := cfg.BaseURL + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(enc))
	if err != nil {
		return "", nil, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if key := cfg.APIKey(); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", nil, "", err
	}
	defer res.Body.Close()
	payload, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode >= 300 {
		return "", nil, "", fmt.Errorf("provider HTTP %d: %s", res.StatusCode, strings.TrimSpace(string(payload)))
	}

	var out struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage *assistUsage `json:"usage"`
	}
	if err := json.Unmarshal(payload, &out); err != nil {
		return "", nil, "", fmt.Errorf("decode provider response: %w", err)
	}
	if len(out.Choices) == 0 {
		return "", nil, "", fmt.Errorf("provider returned no choices")
	}
	model := out.Model
	if model == "" {
		model = cfg.Model
	}
	return out.Choices[0].Message.Content, out.Usage, model, nil
}

// assistSystemPrompt states the contract in the terms the model can act on.
func assistSystemPrompt(surfaceLabel string, allowed map[string]AssistField) string {
	var fields []string
	for name, f := range allowed {
		fields = append(fields, fmt.Sprintf(`  "%s" (%s): %s`, name, f.Label, f.Hint))
	}
	return fmt.Sprintf(`You draft planning text for a software delivery console. Surface: %s.

Return ONLY a JSON object of this exact shape:
{"proposals":[{"field":"<name>","value":"<text>","rationale":"<one short sentence>"}]}

You may propose only these fields:
%s

Rules:
- One proposal per field, at most.
- "value" is the complete new text for that field, not a fragment or a diff.
- Use markdown where the field's hint says so (checklists, diagrams).
- Be concrete about what "done" means; vague criteria are the failure mode this
  console exists to prevent.
- If you have nothing useful to add, return {"proposals":[]}.`, surfaceLabel, strings.Join(fields, "\n"))
}

// assistUserPrompt carries the instruction and the cited context.
func assistUserPrompt(in AssistRequest) string {
	var b strings.Builder
	if len(in.Cites) > 0 {
		b.WriteString("Context the request refers to:\n")
		for _, c := range in.Cites {
			b.WriteString("- ")
			b.WriteString(c)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	if len(in.Current) > 0 {
		b.WriteString("Current text of the fields being revised:\n")
		for k, v := range in.Current {
			if strings.TrimSpace(v) == "" {
				continue
			}
			fmt.Fprintf(&b, "--- %s ---\n%s\n\n", k, v)
		}
	}
	b.WriteString("Request: ")
	b.WriteString(in.Instruction)
	if strings.TrimSpace(in.Instruction) == "" {
		b.WriteString("Improve the fields using the context above.")
	}
	return b.String()
}

// parseAssistProposals enforces the contract.
//
// Three refusals, all deliberate: prose instead of patches, an empty response
// where patches were required, and any field outside the allowlist. The last is
// the one that matters — a model that proposes `gate` on a requirement surface
// would otherwise write to a field the surface does not own.
func parseAssistProposals(raw string, allowed map[string]AssistField, current map[string]string) ([]AssistProposal, error) {
	text := strings.TrimSpace(raw)
	// Tolerate a fenced block even though response_format was requested: a
	// provider that ignores it should not make the feature look broken.
	if strings.HasPrefix(text, "```") {
		if i := strings.Index(text, "\n"); i >= 0 {
			text = text[i+1:]
		}
		text = strings.TrimSuffix(strings.TrimSpace(text), "```")
	}
	var parsed struct {
		Proposals []struct {
			Field     string `json:"field"`
			Value     string `json:"value"`
			Rationale string `json:"rationale"`
		} `json:"proposals"`
	}
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		return nil, fmt.Errorf("the assistant did not return patches (got prose): %w", err)
	}

	out := make([]AssistProposal, 0, len(parsed.Proposals))
	seen := map[string]bool{}
	for _, p := range parsed.Proposals {
		f, ok := allowed[p.Field]
		if !ok {
			return nil, fmt.Errorf("the assistant proposed a field this surface does not own: %q", p.Field)
		}
		if seen[p.Field] {
			continue // one proposal per field; the first wins
		}
		seen[p.Field] = true
		out = append(out, AssistProposal{
			Field:     p.Field,
			Label:     f.Label,
			Before:    current[p.Field],
			Value:     p.Value,
			Rationale: p.Rationale,
		})
	}
	return out, nil
}
