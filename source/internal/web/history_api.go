// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package web

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/anvil-dev/anvil/internal/web/delivery"
)

// auditMiddleware attributes every mutating delivery call (p_0.45.12).
//
// One middleware rather than a call in each handler: a handler that forgets is
// exactly how an audit trail becomes a half-truth, and there are 15 mutating
// routes today with more coming.
//
// It infers the entity from the path. That is deliberately shallow — the row says
// "PATCH designs id=des_7" plus the actor — because a deeper claim would need each
// handler's knowledge, which is what writing it per-handler avoided. The detail
// field carries the request body for the mutations where that is the whole story
// (a field edit), truncated.
func (s *Server) auditDelivery(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || s.delivery == nil {
			next(w, r)
			return
		}
		// Capture the body so it can be recorded, then hand the same bytes to the
		// handler: a middleware that consumes a body is a middleware that breaks
		// every POST behind it.
		body := readAndRestoreBody(r)
		next(w, r)
		rec := auditFromRequest(r, body, userFrom(r.Context()).Username)
		if rec.EntityKind != "" {
			_ = s.delivery.RecordAudit(rec)
		}
	}
}

// auditFromRequest turns a mutating delivery request into an audit row.
func auditFromRequest(r *http.Request, body, actor string) delivery.AuditEntry {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/delivery"), "/")
	parts := strings.Split(path, "/")
	kind, id, action := inferEntity(r.Method, parts)
	detail := ""
	if body != "" && len(body) <= 4000 {
		detail = body
	}
	return delivery.AuditEntry{
		Actor:      actor,
		Action:     r.Method + " " + path,
		EntityKind: kind,
		EntityID:   id,
		Summary:    action,
		Detail:     detail,
		RequestID:  requestIDFrom(r.Context()),
	}
}

// inferEntity maps a path to (kind, id, human summary). Unknown shapes return an
// empty kind, which the caller treats as "do not record" rather than recording a
// meaningless row.
func inferEntity(method string, parts []string) (kind, id, summary string) {
	if len(parts) == 0 || parts[0] == "" {
		return "", "", ""
	}
	verb := map[string]string{
		http.MethodPost:   "created",
		http.MethodPatch:  "updated",
		http.MethodDelete: "deleted",
	}[method]
	if verb == "" {
		verb = "changed"
	}
	switch parts[0] {
	case "eng-reqs":
		if len(parts) >= 2 && parts[1] != "" {
			if len(parts) >= 6 && parts[2] == "versions" {
				return "requirement_version",
					parts[1] + ":" + parts[3] + "." + parts[4] + "." + parts[5],
					"version " + verb
			}
			return "requirement", parts[1], "requirement " + verb
		}
		return "requirement", "", "requirement " + verb
	case "majors":
		if len(parts) >= 2 {
			if len(parts) >= 3 && parts[2] == "advance" {
				return "major", parts[1], "generation advanced"
			}
			return "major", parts[1], "generation " + verb
		}
		return "major", "", "generation " + verb
	case "designs":
		if len(parts) >= 2 {
			if len(parts) >= 3 && parts[2] == "approve" {
				return "design", parts[1], "design approved"
			}
			if len(parts) >= 3 && parts[2] == "bootstrap" {
				return "design", parts[1], "roadmap bootstrapped"
			}
			if len(parts) >= 4 && parts[2] == "versions" {
				if len(parts) >= 5 && parts[4] == "gate-check" {
					return "design_version", parts[1] + ":" + parts[3], "gate checkbox toggled"
				}
				return "design_version", parts[1] + ":" + parts[3], "design version " + verb
			}
			return "design", parts[1], "design " + verb
		}
	case "roadmap":
		if len(parts) >= 3 && parts[1] == "milestones" {
			if len(parts) >= 4 && parts[3] == "acceptance-check" {
				return "milestone", parts[2], "acceptance checkbox toggled"
			}
			if len(parts) >= 4 && parts[3] == "merge" {
				return "milestone", parts[2], "merged to main"
			}
			return "milestone", parts[2], "milestone " + verb
		}
	case "test-cases", "buglist", "polishes":
		if len(parts) >= 3 {
			return strings.TrimSuffix(parts[0], "s"), parts[2], parts[0] + " " + verb
		}
		return strings.TrimSuffix(parts[0], "s"), "", parts[0] + " " + verb
	case "trash":
		if len(parts) >= 2 {
			return "trash", parts[1], "trash " + verb
		}
	}
	return "", "", ""
}

// readAndRestoreBody consumes the request body and puts it back, unwrapped.
//
// A middleware that consumes a body breaks every POST behind it, so the same
// bytes are handed on. The cap bounds what the audit row can store; a body
// larger than it is truncated for the record but passed through whole.
func readAndRestoreBody(r *http.Request) string {
	if r.Body == nil {
		return ""
	}
	const maxAuditBody = 8192
	all, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	_ = r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(all))
	if err != nil {
		return ""
	}
	if len(all) > maxAuditBody {
		return string(all[:maxAuditBody])
	}
	return string(all)
}

// ---------------------------------------------------------------------------
// Job history + search endpoints
// ---------------------------------------------------------------------------

func (s *Server) handleJobs(w http.ResponseWriter, r *http.Request) {
	if s.delivery == nil {
		http.Error(w, "delivery store not configured", http.StatusServiceUnavailable)
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/jobs"), "/")
	if path != "" && r.Method == http.MethodGet {
		// /api/v1/jobs/{id}/events
		parts := strings.Split(path, "/")
		if len(parts) == 2 && parts[1] == "events" {
			events, err := s.delivery.JobEvents(parts[0])
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"job_id": parts[0], "events": events})
			return
		}
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	filter := delivery.JobFilter{
		MilestoneID: r.URL.Query().Get("milestone_id"),
		WorkerID:    r.URL.Query().Get("worker_id"),
		Status:      r.URL.Query().Get("status"),
	}
	jobs, err := s.delivery.ListJobs(filter)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": jobs})
}

// handleJobsOrCancel routes /api/v1/jobs/{id}/cancel to the cancel handler and
// everything else to the read handler.
//
// Kept as one mux entry because both live under the same prefix; the dispatch is
// by method and path suffix, and a POST to anything that is not a cancel is a 405
// rather than a silent read.
func (s *Server) handleJobsOrCancel(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(strings.Trim(r.URL.Path, "/"), "/cancel") {
		s.handleJobCancel(w, r)
		return
	}
	s.handleJobs(w, r)
}

// handleJobCancel stops a job by id, dispatching on how it was started.
//
// Before this the caller had to try the Workbench cancel first and fall back to
// the milestone lever - and for a phase job the first call always failed, because
// the Workbench only knows its own sessions. The kind is in the job record, so the
// decision belongs here rather than in every caller.
func (s *Server) handleJobCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.delivery == nil {
		http.Error(w, "delivery store not configured", http.StatusServiceUnavailable)
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/jobs"), "/")
	parts := strings.Split(path, "/")
	if len(parts) != 2 || parts[1] != "cancel" || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	jobID := parts[0]

	jobs, err := s.delivery.ListJobs(delivery.JobFilter{Limit: 500})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var rec *delivery.JobRecord
	for i := range jobs {
		if jobs[i].JobID == jobID {
			rec = &jobs[i]
			break
		}
	}
	if rec == nil {
		http.Error(w, "unknown job: "+jobID, http.StatusNotFound)
		return
	}
	if rec.Status != "running" && rec.Status != "queued" {
		http.Error(w, "job is not running (status "+rec.Status+")", http.StatusConflict)
		return
	}

	switch rec.Kind {
	case "workbench":
		// The session owns the stream; cancelling it ends the read and reports
		// cancelled.
		if sess, ok := s.workbench.byJob(jobID); ok {
			if sess.OwnerID != 0 && sess.OwnerID != userFrom(r.Context()).ID {
				http.Error(w, "job is outside this user scope", http.StatusForbidden)
				return
			}
			if err := s.jobsCancel(r.Context(), jobID); err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
			s.cancelWorkbenchSession(sess)
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "job_id": jobID, "via": "workbench"})
			return
		}
		// A workbench job with no live session is stale: it ended without its
		// record being finished. Report it rather than pretending to stop it.
		http.Error(w, "that session is no longer live; the job already ended", http.StatusConflict)
		return

	default:
		// A phase / campaign / polish job: ask the scheduler, then stop the
		// milestone loop so it does not enqueue the next phase.
		if err := s.jobsCancel(r.Context(), jobID); err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		if rec.MilestoneID != "" {
			rs := delivery.RunStopped
			if _, err := s.delivery.PatchMilestone(rec.MilestoneID, delivery.PatchMilestoneInput{RunState: &rs}); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		} else {
			http.Error(w, "that job has no milestone to stop", http.StatusConflict)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "job_id": jobID, "via": "milestone"})
		return
	}
}

// cancelWorkbenchSession stops a live session's stream and records the intent.
func (s *Server) cancelWorkbenchSession(sess *workbenchSession) {
	if sess == nil {
		return
	}
	sess.abortApprovals("session stopped")
	s.wbCancelMu.Lock()
	cancel := s.wbCancel[sess.ID]
	s.wbCancelMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// SearchHit remains the API-facing name used by the HTTP tests.
type SearchHit = delivery.SearchResult

// handleSearch answers GET /api/v1/search?q=.
//
// Matching is an indexed FTS5 query. The store owns query normalization,
// ranking, snippets, scope filtering and hard resource bounds.
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.delivery == nil {
		http.Error(w, "delivery store not configured", http.StatusServiceUnavailable)
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeJSON(w, http.StatusOK, map[string]any{"q": "", "hits": []SearchHit{}})
		return
	}
	hits, err := s.delivery.Search(q, r.URL.Query().Get("scope"), 50)
	if errors.Is(err, delivery.ErrSearchQueryTooLong) {
		http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"q": q, "hits": hits})
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
