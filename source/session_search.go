// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/anvil-dev/anvil/internal/store"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

const (
	DefaultSessionSearchLimit = 50
	MaxSessionSearchLimit     = 200
	MaxSessionSnippetRunes    = 240
	maxSessionSearchRecords   = 250_000
	maxSessionSearchBytes     = 64 << 20
)

// SessionSearchOptions defines a local-only query over persisted sessions.
// Cursor is reserved for the pagination contract delivered in p_0.56.2 and is
// rejected in this phase rather than silently ignored.
type SessionSearchOptions struct {
	Local       LocalOptions
	Query       string
	AgentID     string
	Roles       []string
	Kinds       []string
	After       time.Time
	Before      time.Time
	Limit       int
	Cursor      string
	IncludeArgs bool
}

// TextRange indexes Unicode code points in the returned snippet.
type TextRange struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// SessionSearchHit is one bounded local search result. RecordPos is a logical,
// one-based record ordinal and never exposes a byte offset or SQLite row id.
type SessionSearchHit struct {
	AgentID   string      `json:"agent_id"`
	RunID     string      `json:"run_id,omitempty"`
	RecordPos uint64      `json:"record_pos"`
	Kind      string      `json:"kind"`
	Role      string      `json:"role,omitempty"`
	Name      string      `json:"name,omitempty"`
	Timestamp time.Time   `json:"timestamp,omitempty"`
	Title     string      `json:"title,omitempty"`
	Snippet   string      `json:"snippet"`
	Match     []TextRange `json:"match"`
}

// SessionSearchResult is ordered deterministically and bounded by Limit.
type SessionSearchResult struct {
	Hits       []SessionSearchHit `json:"hits"`
	NextCursor string             `json:"next_cursor,omitempty"`
	Scanned    int                `json:"scanned"`
	Partial    bool               `json:"partial"`
}

// SessionSearchQueryError identifies a validation failure before any store is
// scanned. Position is a Unicode code-point offset in Query; -1 means an
// option outside the query string.
type SessionSearchQueryError struct {
	Position int
	Message  string
}

func (e *SessionSearchQueryError) Error() string {
	if e == nil {
		return ""
	}
	if e.Position >= 0 {
		return fmt.Sprintf("invalid session search query at position %d: %s", e.Position, e.Message)
	}
	return "invalid session search options: " + e.Message
}

type sessionSearchTerm struct {
	text   string
	phrase bool
}

type compiledSessionSearch struct {
	positive []sessionSearchTerm
	negative []sessionSearchTerm
	roles    map[string]struct{}
	kinds    map[string]struct{}
	statuses map[string]struct{}
	after    time.Time
	before   time.Time
	filters  bool
}

type sessionSearchDocument struct {
	agentID     string
	runID       string
	pos         uint64
	kind        string
	role        string
	name        string
	status      string
	timestamp   time.Time
	title       string
	display     string
	folded      string
	titleFolded string
}

type rankedSessionHit struct {
	hit   SessionSearchHit
	score int
}

// SearchSessions scans the selected local backend through its canonical record
// API. It performs no provider, telemetry, web, MCP, resume, or mutation call.
func SearchSessions(ctx context.Context, opts SessionSearchOptions) (SessionSearchResult, error) {
	if err := ctx.Err(); err != nil {
		return SessionSearchResult{}, err
	}
	query, err := compileSessionSearch(opts)
	if err != nil {
		return SessionSearchResult{}, err
	}
	if strings.TrimSpace(opts.Cursor) != "" {
		return SessionSearchResult{}, &SessionSearchQueryError{
			Position: -1, Message: "cursor pagination is not available yet",
		}
	}
	limit := opts.Limit
	if limit < 0 {
		return SessionSearchResult{}, &SessionSearchQueryError{Position: -1, Message: "limit cannot be negative"}
	}
	if limit == 0 {
		limit = DefaultSessionSearchLimit
	}
	if limit > MaxSessionSearchLimit {
		limit = MaxSessionSearchLimit
	}
	cwd, err := resolveListCWD(opts.Local.CWD)
	if err != nil {
		return SessionSearchResult{}, err
	}
	backend, err := store.Open(cwd, opts.Local.Store)
	if err != nil {
		return SessionSearchResult{}, newError("open store", false, err)
	}
	defer backend.Close()
	return searchSessionsBackend(ctx, opts, query, limit, backend)
}

func searchSessionsBackend(
	ctx context.Context,
	opts SessionSearchOptions,
	query compiledSessionSearch,
	limit int,
	backend store.Backend,
) (SessionSearchResult, error) {
	var ids []string
	if strings.TrimSpace(opts.AgentID) != "" {
		if err := validPlanID("agent_id", opts.AgentID); err != nil {
			return SessionSearchResult{}, &SessionSearchQueryError{Position: -1, Message: err.Error()}
		}
		ids = []string{opts.AgentID}
	} else {
		var err error
		ids, err = backend.ListIDs()
		if err != nil {
			return SessionSearchResult{}, newError("list agents", false, err)
		}
		sort.Strings(ids)
	}
	result := SessionSearchResult{Hits: []SessionSearchHit{}}
	var ranked []rankedSessionHit
	scannedBytes := 0
	for _, agentID := range ids {
		if err := ctx.Err(); err != nil {
			return SessionSearchResult{}, err
		}
		records, loadErr := backend.Load(agentID)
		if loadErr != nil {
			if errors.Is(loadErr, store.ErrNotFound) {
				continue
			}
			result.Partial = true
			continue
		}
		if err := ctx.Err(); err != nil {
			return SessionSearchResult{}, err
		}
		records = append([]store.Record(nil), records...)
		scanEnd := len(records)
		for index, rec := range records {
			result.Scanned++
			scannedBytes += recordSearchSize(rec, opts.IncludeArgs)
			if result.Scanned >= maxSessionSearchRecords || scannedBytes >= maxSessionSearchBytes {
				result.Partial = true
				scanEnd = index + 1
				break
			}
			if result.Scanned%64 == 0 {
				if err := ctx.Err(); err != nil {
					return SessionSearchResult{}, err
				}
			}
		}
		documents, projectionPartial := projectSessionSearchDocuments(
			agentID, records[:scanEnd], opts.IncludeArgs,
		)
		result.Partial = result.Partial || projectionPartial
		if len(query.positive) == 0 && !query.filters {
			if len(documents) > 0 {
				documents = documents[:1] // metadata-only empty query
			}
		}
		ranked = append(ranked, matchSessionDocuments(documents, query)...)
		if result.Partial && (result.Scanned >= maxSessionSearchRecords || scannedBytes >= maxSessionSearchBytes) {
			break
		}
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		if !ranked[i].hit.Timestamp.Equal(ranked[j].hit.Timestamp) {
			return ranked[i].hit.Timestamp.After(ranked[j].hit.Timestamp)
		}
		if ranked[i].hit.AgentID != ranked[j].hit.AgentID {
			return ranked[i].hit.AgentID < ranked[j].hit.AgentID
		}
		if ranked[i].hit.RecordPos != ranked[j].hit.RecordPos {
			return ranked[i].hit.RecordPos < ranked[j].hit.RecordPos
		}
		return ranked[i].hit.Kind < ranked[j].hit.Kind
	})
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}
	for _, item := range ranked {
		result.Hits = append(result.Hits, item.hit)
	}
	return result, nil
}

func compileSessionSearch(opts SessionSearchOptions) (compiledSessionSearch, error) {
	out := compiledSessionSearch{after: opts.After, before: opts.Before}
	var err error
	out.roles, err = searchOptionSet(opts.Roles, allowedSessionRoles, "role")
	if err != nil {
		return out, err
	}
	out.kinds, err = searchOptionSet(opts.Kinds, allowedSessionKinds, "kind")
	if err != nil {
		return out, err
	}
	queryRoles := map[string]struct{}{}
	queryKinds := map[string]struct{}{}
	queryStatuses := map[string]struct{}{}
	tokens, err := tokenizeSessionQuery(opts.Query)
	if err != nil {
		return out, err
	}
	for _, token := range tokens {
		key, value, isFilter := strings.Cut(token.text, ":")
		if isFilter && !token.quoted {
			key = strings.ToLower(key)
			switch key {
			case "role":
				if token.negative {
					return out, queryError(token.position, "role filter cannot be negated")
				}
				if err := addSearchFilter(queryRoles, value, allowedSessionRoles, "role", token.position); err != nil {
					return out, err
				}
				continue
			case "kind":
				if token.negative {
					return out, queryError(token.position, "kind filter cannot be negated")
				}
				if err := addSearchFilter(queryKinds, value, allowedSessionKinds, "kind", token.position); err != nil {
					return out, err
				}
				continue
			case "status":
				if token.negative {
					return out, queryError(token.position, "status filter cannot be negated")
				}
				if err := addSearchFilter(queryStatuses, value, allowedSessionStatuses, "status", token.position); err != nil {
					return out, err
				}
				continue
			case "after", "before":
				if token.negative {
					return out, queryError(token.position, key+" filter cannot be negated")
				}
				date, parseErr := time.Parse("2006-01-02", value)
				if parseErr != nil {
					return out, queryError(token.position, key+" requires YYYY-MM-DD")
				}
				if key == "after" && (out.after.IsZero() || date.After(out.after)) {
					out.after = date
				}
				if key == "before" && (out.before.IsZero() || date.Before(out.before)) {
					out.before = date
				}
				continue
			}
		}
		term := sessionSearchTerm{text: normalizeSearchText(token.text), phrase: token.quoted}
		if term.text == "" {
			return out, queryError(token.position, "search term is empty after normalization")
		}
		if token.negative {
			out.negative = append(out.negative, term)
		} else {
			out.positive = append(out.positive, term)
		}
	}
	if intersected, conflict := intersectSearchSets(out.roles, queryRoles); conflict {
		return out, queryError(0, "role filters have no overlap")
	} else {
		out.roles = intersected
	}
	if intersected, conflict := intersectSearchSets(out.kinds, queryKinds); conflict {
		return out, queryError(0, "kind filters have no overlap")
	} else {
		out.kinds = intersected
	}
	out.statuses = queryStatuses
	if (!opts.After.IsZero() || !opts.Before.IsZero()) && !out.after.IsZero() && !out.before.IsZero() &&
		!out.after.Before(out.before) {
		return out, &SessionSearchQueryError{Position: -1, Message: "after must be earlier than before"}
	}
	if !out.after.IsZero() && !out.before.IsZero() && !out.after.Before(out.before) {
		return out, queryError(0, "after must be earlier than before")
	}
	out.filters = len(out.roles) > 0 || len(out.kinds) > 0 || len(out.statuses) > 0 ||
		!out.after.IsZero() || !out.before.IsZero()
	return out, nil
}

var (
	allowedSessionRoles = map[string]struct{}{
		"user": {}, "assistant": {}, "tool": {},
	}
	allowedSessionKinds = map[string]struct{}{
		"meta": {}, "message": {}, "error": {}, "plan": {}, "todo": {}, "run_end": {},
	}
	allowedSessionStatuses = map[string]struct{}{
		"finished": {}, "error": {}, "cancelled": {}, "interrupted": {},
	}
)

type sessionQueryToken struct {
	text     string
	position int
	quoted   bool
	negative bool
}

func tokenizeSessionQuery(input string) ([]sessionQueryToken, error) {
	runes := []rune(input)
	var out []sessionQueryToken
	for at := 0; at < len(runes); {
		for at < len(runes) && unicode.IsSpace(runes[at]) {
			at++
		}
		if at == len(runes) {
			break
		}
		token := sessionQueryToken{position: at}
		if runes[at] == '-' {
			token.negative = true
			at++
			if at == len(runes) || unicode.IsSpace(runes[at]) {
				return nil, queryError(token.position, "exclusion requires a term")
			}
		}
		if runes[at] == '"' {
			token.quoted = true
			at++
			start := at
			for at < len(runes) && runes[at] != '"' {
				at++
			}
			if at == len(runes) {
				return nil, queryError(token.position, "unterminated quoted phrase")
			}
			token.text = string(runes[start:at])
			at++
			if at < len(runes) && !unicode.IsSpace(runes[at]) {
				return nil, queryError(at, "quoted phrase must end at whitespace")
			}
		} else {
			start := at
			for at < len(runes) && !unicode.IsSpace(runes[at]) {
				at++
			}
			token.text = string(runes[start:at])
		}
		if token.text == "" {
			return nil, queryError(token.position, "empty quoted phrase")
		}
		out = append(out, token)
	}
	return out, nil
}

func queryError(position int, message string) error {
	return &SessionSearchQueryError{Position: position, Message: message}
}

func searchOptionSet(values []string, allowed map[string]struct{}, field string) (map[string]struct{}, error) {
	if len(values) == 0 {
		return nil, nil
	}
	out := make(map[string]struct{}, len(values))
	for _, raw := range values {
		value := strings.ToLower(strings.TrimSpace(raw))
		if _, ok := allowed[value]; !ok {
			return nil, &SessionSearchQueryError{Position: -1, Message: fmt.Sprintf("unknown %s %q", field, raw)}
		}
		out[value] = struct{}{}
	}
	return out, nil
}

func addSearchFilter(target map[string]struct{}, raw string, allowed map[string]struct{}, field string, pos int) error {
	if strings.TrimSpace(raw) == "" {
		return queryError(pos, field+" filter requires a value")
	}
	for _, part := range strings.Split(raw, "|") {
		value := strings.ToLower(strings.TrimSpace(part))
		if _, ok := allowed[value]; !ok {
			return queryError(pos, fmt.Sprintf("unknown %s %q", field, part))
		}
		target[value] = struct{}{}
	}
	return nil
}

func intersectSearchSets(options, query map[string]struct{}) (map[string]struct{}, bool) {
	if len(options) == 0 {
		return query, false
	}
	if len(query) == 0 {
		return options, false
	}
	out := map[string]struct{}{}
	for value := range options {
		if _, ok := query[value]; ok {
			out[value] = struct{}{}
		}
	}
	return out, len(out) == 0
}

func projectSessionSearchDocuments(agentID string, records []store.Record, includeArgs bool) ([]sessionSearchDocument, bool) {
	summary := store.Summarize(records)
	title := summary.Title
	metaDisplay := strings.Join(nonEmptySearchFields(title, summary.TaskTitle, agentID), " ")
	metaDisplay, metaFolded := prepareSearchText(metaDisplay)
	documents := []sessionSearchDocument{{
		agentID: agentID, pos: 1, kind: "meta", timestamp: summary.UpdatedAt,
		title: title, display: metaDisplay, folded: metaFolded,
		titleFolded: normalizeSearchText(title),
	}}
	partial := false
	for index, rec := range records {
		pos := uint64(index + 1)
		switch rec.Type {
		case store.TypeMessage:
			fields := []string{rec.Content, rec.Name}
			name := rec.Name
			kind := "message"
			for _, call := range rec.ToolCalls {
				fields = append(fields, call.Name)
				if name == "" {
					name = call.Name
				}
				if call.Name == "todo_write" {
					kind = "todo"
				}
				if includeArgs && len(call.Args) > 0 {
					fields = append(fields, string(call.Args))
				}
			}
			display, folded := prepareSearchText(strings.Join(nonEmptySearchFields(fields...), " "))
			documents = append(documents, sessionSearchDocument{
				agentID: agentID, runID: rec.RunID, pos: pos, kind: kind,
				role: rec.Role, name: name, timestamp: rec.Time, title: title,
				display: display, folded: folded, titleFolded: normalizeSearchText(title),
			})
		case store.TypeRunEnd:
			kind := "run_end"
			if rec.Err != "" || rec.Code != "" || rec.Status == "error" || rec.Status == "interrupted" {
				kind = "error"
			}
			display, folded := prepareSearchText(strings.Join(
				nonEmptySearchFields(rec.Status, rec.Code, rec.Err, rec.Cause), " ",
			))
			documents = append(documents, sessionSearchDocument{
				agentID: agentID, runID: rec.RunID, pos: pos, kind: kind,
				status: rec.Status, timestamp: rec.Time, title: title,
				display: display, folded: folded, titleFolded: normalizeSearchText(title),
			})
		case store.TypeRunState:
			kind := "run_end"
			if rec.RunState == "error" || rec.RunState == "interrupted" {
				kind = "error"
			}
			display, folded := prepareSearchText(strings.Join(
				nonEmptySearchFields(rec.RunState, rec.Cause), " ",
			))
			documents = append(documents, sessionSearchDocument{
				agentID: agentID, runID: rec.RunID, pos: pos, kind: kind,
				status: rec.RunState, timestamp: rec.Time, title: title,
				display: display, folded: folded, titleFolded: normalizeSearchText(title),
			})
		case store.TypePlanArtifact:
			artifact, err := planArtifactFromRecord(rec)
			if err != nil {
				partial = true
				continue
			}
			fields := []string{artifact.Title, artifact.Body, artifact.ArtifactID}
			for _, item := range artifact.Items {
				fields = append(fields, item.ID, item.Title, item.Goal)
				fields = append(fields, item.Plan...)
				fields = append(fields, item.Acceptance...)
			}
			display, folded := prepareSearchText(strings.Join(nonEmptySearchFields(fields...), " "))
			documents = append(documents, sessionSearchDocument{
				agentID: agentID, runID: rec.RunID, pos: pos, kind: "plan",
				name: artifact.ArtifactID, timestamp: rec.Time, title: title,
				display: display, folded: folded, titleFolded: normalizeSearchText(title),
			})
		}
	}
	return documents, partial
}

func matchSessionDocuments(documents []sessionSearchDocument, query compiledSessionSearch) []rankedSessionHit {
	var out []rankedSessionHit
	directRuns := map[string]struct{}{}
	eligibleByRun := map[string][]sessionSearchDocument{}
	for _, doc := range documents {
		if !sessionDocumentFilters(doc, query) {
			continue
		}
		if doc.runID != "" {
			eligibleByRun[doc.runID] = append(eligibleByRun[doc.runID], doc)
		}
		score, matches := scoreSessionDocument(doc, query)
		if !matches {
			continue
		}
		out = append(out, rankedSessionHit{hit: sessionDocumentHit(doc, query.positive), score: score})
		if doc.runID != "" {
			directRuns[doc.runID] = struct{}{}
		}
	}
	if len(query.positive) == 0 {
		return out
	}
	for runID, docs := range eligibleByRun {
		if _, direct := directRuns[runID]; direct {
			continue
		}
		var aggregate strings.Builder
		excluded := false
		for _, doc := range docs {
			aggregate.WriteByte(' ')
			aggregate.WriteString(doc.folded)
			if containsAnySearchTerm(doc.folded, query.negative) {
				excluded = true
			}
		}
		if excluded || !containsAllSearchTerms(aggregate.String(), query.positive) {
			continue
		}
		chosen := docs[0]
		for _, doc := range docs {
			if firstSearchIndex(doc.folded, query.positive) >= 0 {
				chosen = doc
				break
			}
		}
		chosen.runID = runID
		out = append(out, rankedSessionHit{hit: sessionDocumentHit(chosen, query.positive), score: 100})
	}
	return out
}

func sessionDocumentFilters(doc sessionSearchDocument, query compiledSessionSearch) bool {
	if len(query.roles) > 0 {
		if _, ok := query.roles[doc.role]; !ok {
			return false
		}
	}
	if len(query.kinds) > 0 {
		if _, ok := query.kinds[doc.kind]; !ok {
			return false
		}
	}
	if len(query.statuses) > 0 {
		if _, ok := query.statuses[doc.status]; !ok {
			return false
		}
	}
	if !query.after.IsZero() && (doc.timestamp.IsZero() || doc.timestamp.Before(query.after)) {
		return false
	}
	if !query.before.IsZero() && (doc.timestamp.IsZero() || !doc.timestamp.Before(query.before)) {
		return false
	}
	return true
}

func scoreSessionDocument(doc sessionSearchDocument, query compiledSessionSearch) (int, bool) {
	if containsAnySearchTerm(doc.folded, query.negative) {
		return 0, false
	}
	if len(query.positive) == 0 {
		return 50, true
	}
	if !containsAllSearchTerms(doc.folded, query.positive) {
		return 0, false
	}
	if doc.kind == "meta" && len(query.positive) == 1 &&
		(doc.titleFolded == query.positive[0].text || normalizeSearchText(doc.agentID) == query.positive[0].text) {
		return 500, true
	}
	if doc.kind == "meta" && containsAllSearchTerms(doc.titleFolded, query.positive) {
		return 400, true
	}
	for _, term := range query.positive {
		if term.phrase && strings.Contains(doc.folded, term.text) {
			return 300, true
		}
	}
	return 200, true
}

func sessionDocumentHit(doc sessionSearchDocument, terms []sessionSearchTerm) SessionSearchHit {
	snippet, ranges := sessionSearchSnippet(doc.display, terms)
	return SessionSearchHit{
		AgentID: doc.agentID, RunID: doc.runID, RecordPos: doc.pos,
		Kind: doc.kind, Role: doc.role, Name: doc.name, Timestamp: doc.timestamp,
		Title: doc.title, Snippet: snippet, Match: ranges,
	}
}

func containsAllSearchTerms(text string, terms []sessionSearchTerm) bool {
	for _, term := range terms {
		if !strings.Contains(text, term.text) {
			return false
		}
	}
	return true
}

func containsAnySearchTerm(text string, terms []sessionSearchTerm) bool {
	for _, term := range terms {
		if strings.Contains(text, term.text) {
			return true
		}
	}
	return false
}

func firstSearchIndex(text string, terms []sessionSearchTerm) int {
	best := -1
	for _, term := range terms {
		if at := strings.Index(text, term.text); at >= 0 && (best < 0 || at < best) {
			best = at
		}
	}
	return best
}

func sessionSearchSnippet(display string, terms []sessionSearchTerm) (string, []TextRange) {
	display, folded, foldToDisplay := searchableDisplay(display)
	runes := []rune(display)
	first := len(runes) / 2
	if len(terms) == 0 {
		first = 0
	} else if at := firstSearchRune(folded, terms); at >= 0 && at < len(foldToDisplay) {
		first = foldToDisplay[at]
	}
	start, end := searchSnippetWindow(len(runes), first)
	prefix := start > 0
	suffix := end < len(runes)
	body := string(runes[start:end])
	snippet := body
	offset := 0
	if prefix {
		snippet = "…" + snippet
		offset = 1
	}
	if suffix {
		snippet += "…"
	}
	_, snippetFolded, snippetMap := searchableDisplay(body)
	ranges := make([]TextRange, 0, len(terms))
	for _, term := range terms {
		at := strings.Index(snippetFolded, term.text)
		if at < 0 {
			continue
		}
		foldStart := len([]rune(snippetFolded[:at]))
		foldEnd := foldStart + len([]rune(term.text))
		if foldStart >= len(snippetMap) {
			continue
		}
		displayStart := snippetMap[foldStart]
		displayEnd := len([]rune(body))
		if foldEnd < len(snippetMap) {
			displayEnd = snippetMap[foldEnd]
		}
		for next := foldEnd; displayEnd <= displayStart && next < len(snippetMap); next++ {
			displayEnd = snippetMap[next]
		}
		if displayEnd <= displayStart {
			displayEnd = min(displayStart+1, len([]rune(body)))
		}
		ranges = append(ranges, TextRange{Start: offset + displayStart, End: offset + displayEnd})
	}
	if ranges == nil {
		ranges = []TextRange{}
	}
	return snippet, ranges
}

func searchSnippetWindow(length, center int) (int, int) {
	if length <= MaxSessionSnippetRunes {
		return 0, length
	}
	start := center - MaxSessionSnippetRunes/2
	if start < 0 {
		start = 0
	}
	end := start + MaxSessionSnippetRunes
	if end > length {
		end = length
		start = end - MaxSessionSnippetRunes
	}
	// Ellipses are part of the bound.
	if start > 0 {
		start++
	}
	if end < length {
		end--
	}
	return start, end
}

func firstSearchRune(folded string, terms []sessionSearchTerm) int {
	best := -1
	for _, term := range terms {
		at := strings.Index(folded, term.text)
		if at < 0 {
			continue
		}
		runeAt := len([]rune(folded[:at]))
		if best < 0 || runeAt < best {
			best = runeAt
		}
	}
	return best
}

func prepareSearchText(value string) (string, string) {
	display, folded, _ := searchableDisplay(value)
	return display, folded
}

func normalizeSearchText(value string) string {
	_, folded, _ := searchableDisplay(value)
	return folded
}

func searchableDisplay(value string) (string, string, []int) {
	normalized := norm.NFKC.String(value)
	displayRunes := make([]rune, 0, len([]rune(normalized)))
	pendingSpace := false
	for _, r := range normalized {
		switch {
		case unicode.IsControl(r):
			continue
		case unicode.IsSpace(r):
			if len(displayRunes) > 0 {
				pendingSpace = true
			}
		default:
			if pendingSpace {
				displayRunes = append(displayRunes, ' ')
				pendingSpace = false
			}
			displayRunes = append(displayRunes, r)
		}
	}
	var folded strings.Builder
	var mapping []int
	folder := cases.Fold()
	for index, r := range displayRunes {
		part := []rune(folder.String(string(r)))
		folded.WriteString(string(part))
		for range part {
			mapping = append(mapping, index)
		}
	}
	mapping = append(mapping, len(displayRunes))
	return string(displayRunes), folded.String(), mapping
}

func nonEmptySearchFields(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, value)
		}
	}
	return out
}

func recordSearchSize(rec store.Record, includeArgs bool) int {
	size := len(rec.Content) + len(rec.Title) + len(rec.TaskTitle) + len(rec.Err) +
		len(rec.Body) + len(rec.Items) + len(rec.Cause) + len(rec.RunState)
	if includeArgs {
		for _, call := range rec.ToolCalls {
			size += len(call.Args)
		}
	}
	return size
}
