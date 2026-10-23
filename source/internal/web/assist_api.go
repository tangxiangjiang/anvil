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
	"strconv"
	"strings"
	"time"

	"github.com/anvil-dev/anvil/internal/web/delivery"
)

// The drafting assistant (p_0.45.8).
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
//     surface saves through its own PATCH. That is what makes p_0.45.3's
//     no-silent-write invariant structural rather than a note in three files.
//
// The boundary this crosses, and how: design 23 says agentd is not an LLM reverse
// proxy and the *worker* owns cognition. anvil-web calling a provider to draft
// planning text is a different act — no tools, no execution, no conversation
// state — and it is written down in doc/design/v0.x/40-web-assist.md rather than
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
	"project_readme": {
		Label: "Project README",
		Fields: []AssistField{
			{Name: "readme", Label: "README", Hint: "The whole README in markdown: what the project is, how to build and run it, and the conventions a newcomer needs."},
		},
	},
	"major": {
		Label: "Generation",
		Fields: []AssistField{
			{Name: "title", Label: "Title", Hint: "A short name for this generation, a few words at most."},
			{Name: "summary", Label: "Summary", Hint: "What this generation covers and what it is for."},
		},
	},
	"milestone": {
		Label: "Milestone",
		Fields: []AssistField{
			{Name: "title", Label: "Milestone title", Hint: "What this milestone delivers, in a few words."},
			{Name: "goals", Label: "Goals", Hint: "What the milestone achieves, as outcomes."},
			{Name: "completion", Label: "Completion criteria", Hint: "What done means, testably. This is what the agent is told."},
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
	Field       string      `json:"field,omitempty"`
	Instruction string      `json:"instruction"`
	Target      *AssistRef  `json:"target,omitempty"`
	Cites       []AssistRef `json:"cites,omitempty"`
}

// AssistRef is a stable reference to a Delivery record. It deliberately has no
// path or free-text member: the server resolves all prompt context from its store.
type AssistRef struct {
	Type     string `json:"type"`
	ID       string `json:"id"`
	ParentID string `json:"parent_id,omitempty"`
	Major    int    `json:"major,omitempty"`
	Minor    int    `json:"minor,omitempty"`
	Patch    int    `json:"patch,omitempty"`
	Field    string `json:"field,omitempty"`
}

// AssistCitation is safe, resolved citation metadata returned for display. The
// resolved content is never echoed to the browser.
type AssistCitation struct {
	Ref   AssistRef `json:"ref"`
	Label string    `json:"label"`
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
	Citations []AssistCitation `json:"citations,omitempty"`
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
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
		return
	}
	surface, ok := assistSurfaces[strings.TrimSpace(in.Surface)]
	if !ok {
		http.Error(w, "unknown surface: "+in.Surface, http.StatusBadRequest)
		return
	}
	if len(in.Instruction) > 4096 {
		http.Error(w, "instruction exceeds 4096 bytes", http.StatusRequestEntityTooLarge)
		return
	}
	if strings.TrimSpace(in.Instruction) == "" && len(in.Cites) == 0 {
		http.Error(w, "an instruction or at least one cite is required", http.StatusBadRequest)
		return
	}
	if len(in.Cites) > 8 {
		http.Error(w, "at most 8 citations are allowed", http.StatusRequestEntityTooLarge)
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

	resolved, current, citations, status, err := s.resolveAssistContext(in, allowed)
	if err != nil {
		http.Error(w, err.Error(), status)
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

	raw, usage, model, err := s.callAssist(r.Context(), surface.Label, allowed, in, resolved, current)
	if err != nil {
		// A provider failure is reported as an error, never as an empty proposal
		// list: an empty list reads as "the model had no suggestions", which is a
		// different and misleading statement.
		http.Error(w, "assist: "+err.Error(), http.StatusBadGateway)
		return
	}

	proposals, perr := parseAssistProposals(raw, allowed, current)
	if perr != nil {
		http.Error(w, "assist: "+perr.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, AssistResult{
		Available: true,
		Model:     model,
		Proposals: proposals,
		Citations: citations,
		Usage:     usage,
	})
}

// callAssist performs the one provider request.
func (s *Server) callAssist(ctx context.Context, surfaceLabel string, allowed map[string]AssistField, in AssistRequest, resolved []resolvedAssistCitation, current map[string]string) (string, *assistUsage, string, error) {
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
			{"role": "user", "content": assistUserPrompt(in.Instruction, resolved, current)},
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
func assistUserPrompt(instruction string, cites []resolvedAssistCitation, current map[string]string) string {
	var b strings.Builder
	if len(cites) > 0 {
		b.WriteString("Context the request refers to:\n")
		for _, c := range cites {
			fmt.Fprintf(&b, "--- %s ---\n%s\n", c.Label, c.Content)
		}
		b.WriteString("\n")
	}
	if len(current) > 0 {
		b.WriteString("Current text of the fields being revised:\n")
		for k, v := range current {
			if strings.TrimSpace(v) == "" {
				continue
			}
			fmt.Fprintf(&b, "--- %s ---\n%s\n\n", k, v)
		}
	}
	b.WriteString("Request: ")
	b.WriteString(instruction)
	if strings.TrimSpace(instruction) == "" {
		b.WriteString("Improve the fields using the context above.")
	}
	return b.String()
}

const (
	maxAssistCitationBytes = 4096
	maxAssistContextBytes  = 16384
)

type assistScope struct {
	Project string
	EngReq  string
	Major   int
}

type assistRecord struct {
	Scope  assistScope
	Label  string
	Fields map[string]string
}

type resolvedAssistCitation struct {
	Label   string
	Content string
}

func (s *Server) resolveAssistContext(in AssistRequest, allowed map[string]AssistField) ([]resolvedAssistCitation, map[string]string, []AssistCitation, int, error) {
	current := map[string]string{}
	if s.delivery == nil {
		return nil, nil, nil, http.StatusServiceUnavailable, fmt.Errorf("delivery store is unavailable")
	}
	snap, err := s.delivery.Snapshot()
	if err != nil {
		return nil, nil, nil, http.StatusInternalServerError, fmt.Errorf("load assist context: %w", err)
	}

	var target *assistRecord
	switch in.Surface {
	case "requirement_version", "design", "design_version":
		if in.Target == nil {
			return nil, nil, nil, http.StatusBadRequest, fmt.Errorf("surface %s requires a target record", in.Surface)
		}
	}
	if in.Target != nil {
		if in.Target.Field != "" {
			return nil, nil, nil, http.StatusBadRequest, fmt.Errorf("target must identify a record, not a field")
		}
		targetTypes := map[string][]string{
			"requirement_version": {"requirement_version"},
			"design":              {"design"},
			"design_version":      {"design_version"},
			"major":               {"major"},
			"milestone":           {"requirement_version"},
			"roadmap_polish":      {"requirement_version"},
			"test_case":           {"requirement_version"},
		}
		permitted := false
		for _, typ := range targetTypes[in.Surface] {
			if in.Target.Type == typ {
				permitted = true
				break
			}
		}
		if !permitted {
			return nil, nil, nil, http.StatusBadRequest, fmt.Errorf("target type %q is not valid for surface %s", in.Target.Type, in.Surface)
		}
		rec, status, err := assistRecordFromSnapshot(snap, *in.Target)
		if err != nil {
			return nil, nil, nil, status, err
		}
		target = &rec
		for name := range allowed {
			if value, ok := rec.Fields[name]; ok {
				current[name] = value
			}
		}
	}
	if len(in.Cites) > 0 && target == nil {
		return nil, nil, nil, http.StatusBadRequest, fmt.Errorf("citations require a target record")
	}

	resolved := make([]resolvedAssistCitation, 0, len(in.Cites))
	meta := make([]AssistCitation, 0, len(in.Cites))
	total := 0
	for _, ref := range in.Cites {
		rec, status, err := assistRecordFromSnapshot(snap, ref)
		if err != nil {
			return nil, nil, nil, status, err
		}
		if !assistScopesMatch(target.Scope, rec.Scope) {
			return nil, nil, nil, http.StatusForbidden, fmt.Errorf("citation crosses the target project or generation")
		}
		content, label, ok := assistCitationField(rec, ref.Field)
		if !ok {
			return nil, nil, nil, http.StatusBadRequest, fmt.Errorf("field %q is not citeable for %s", ref.Field, ref.Type)
		}
		if len(content) > maxAssistCitationBytes {
			return nil, nil, nil, http.StatusRequestEntityTooLarge, fmt.Errorf("citation %s exceeds %d bytes", label, maxAssistCitationBytes)
		}
		total += len(content)
		if total > maxAssistContextBytes {
			return nil, nil, nil, http.StatusRequestEntityTooLarge, fmt.Errorf("resolved citation context exceeds %d bytes", maxAssistContextBytes)
		}
		resolved = append(resolved, resolvedAssistCitation{Label: label, Content: content})
		meta = append(meta, AssistCitation{Ref: ref, Label: label})
	}
	return resolved, current, meta, http.StatusOK, nil
}

func assistScopesMatch(target, cite assistScope) bool {
	if target.EngReq != "" {
		return cite.EngReq == target.EngReq && cite.Project == target.Project && cite.Major == target.Major
	}
	return cite.EngReq == "" && cite.Major == target.Major
}

func assistCitationField(rec assistRecord, field string) (string, string, bool) {
	if field == "overview" {
		var b strings.Builder
		for _, name := range []string{"title", "problem", "goals", "non_goals", "constraints", "success"} {
			if value := strings.TrimSpace(rec.Fields[name]); value != "" {
				fmt.Fprintf(&b, "%s: %s\n", name, value)
			}
		}
		return strings.TrimSpace(b.String()), rec.Label + " · overview", true
	}
	value, ok := rec.Fields[field]
	if !ok {
		return "", "", false
	}
	return value, rec.Label + " · " + field, true
}

func assistRecordFromSnapshot(snap delivery.Snapshot, ref AssistRef) (assistRecord, int, error) {
	ref.Type = strings.TrimSpace(ref.Type)
	ref.ID = strings.TrimSpace(ref.ID)
	ref.ParentID = strings.TrimSpace(ref.ParentID)
	if ref.Type == "" || ref.ID == "" || len(ref.ID) > 256 || len(ref.ParentID) > 256 {
		return assistRecord{}, http.StatusBadRequest, fmt.Errorf("citation type and bounded record id are required")
	}
	if strings.Contains(ref.ID, "..") || strings.ContainsAny(ref.ID, `/\`) ||
		strings.Contains(ref.ParentID, "..") || strings.ContainsAny(ref.ParentID, `/\`) {
		return assistRecord{}, http.StatusBadRequest, fmt.Errorf("record references cannot contain paths")
	}
	projectScope := func(eng delivery.EngReq) string {
		if eng.ProjectID != "" {
			return eng.ProjectID
		}
		return eng.ID
	}
	findEng := func(id string) *delivery.EngReq {
		for i := range snap.EngReqs {
			if snap.EngReqs[i].ID == id {
				return &snap.EngReqs[i]
			}
		}
		return nil
	}

	switch ref.Type {
	case "requirement_version":
		eng := findEng(ref.ID)
		if eng != nil {
			for _, v := range eng.Versions {
				if v.Major == ref.Major && v.Minor == ref.Minor && v.Patch == ref.Patch {
					return assistRecord{
						Scope: assistScope{Project: projectScope(*eng), EngReq: eng.ID, Major: v.Major},
						Label: fmt.Sprintf("Requirement %s %d.%d.%d", eng.ID, v.Major, v.Minor, v.Patch),
						Fields: map[string]string{
							"title": v.Title, "problem": v.Problem, "goals": v.Goals,
							"non_goals": v.NonGoals, "constraints": v.Constraints, "success": v.Success,
						},
					}, http.StatusOK, nil
				}
			}
		}
	case "design":
		for _, d := range snap.Designs {
			if d.ID == ref.ID {
				eng := findEng(d.EngReqID)
				if eng == nil {
					break
				}
				return assistRecord{
					Scope: assistScope{Project: projectScope(*eng), EngReq: eng.ID, Major: d.Major},
					Label: "Design " + d.ID,
					Fields: map[string]string{
						"title": d.Title, "adr_context": d.AdrContext, "adr_decision": d.AdrDecision,
						"adr_consequences": d.AdrConsequences, "adr_spec": d.AdrSpec,
						"adr_architecture": d.AdrArchitecture, "adr_sequence": d.AdrSequence,
						"adr_sample": d.AdrSample,
					},
				}, http.StatusOK, nil
			}
		}
	case "design_version":
		for _, d := range snap.Designs {
			if d.ID != ref.ParentID {
				continue
			}
			eng := findEng(d.EngReqID)
			if eng == nil {
				break
			}
			for _, v := range d.Versions {
				if v.ID == ref.ID {
					return assistRecord{
						Scope: assistScope{Project: projectScope(*eng), EngReq: eng.ID, Major: d.Major},
						Label: "Design version " + v.ID,
						Fields: map[string]string{
							"title": v.Title, "gate": v.Gate,
							"sub_architecture": v.SubArchitecture, "class_design": v.ClassDesign,
						},
					}, http.StatusOK, nil
				}
			}
		}
	case "major":
		major, err := strconv.Atoi(ref.ID)
		if err != nil || major < 0 {
			return assistRecord{}, http.StatusBadRequest, fmt.Errorf("major reference id must be a non-negative integer")
		}
		for _, m := range snap.Majors {
			if m.Major == major {
				return assistRecord{
					Scope:  assistScope{Major: major},
					Label:  fmt.Sprintf("Generation %d", major),
					Fields: map[string]string{"title": m.Title, "summary": m.Summary},
				}, http.StatusOK, nil
			}
		}
	default:
		return assistRecord{}, http.StatusBadRequest, fmt.Errorf("unsupported citation type %q", ref.Type)
	}
	return assistRecord{}, http.StatusNotFound, fmt.Errorf("referenced record is missing or expired")
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
