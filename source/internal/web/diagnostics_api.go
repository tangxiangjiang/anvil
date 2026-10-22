// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/anvil-dev/anvil/internal/web/schedclient"
)

// Actionable errors (p_0.45.11).
//
// Today a failed action surfaces the server's raw message. "gate checklist
// incomplete (V1): x" tells an operator what is wrong but not what to do, and at
// 2am that difference is the whole thing.
//
// Each known class gets a sentence naming the cause and the next step, with the
// raw message kept underneath rather than replaced — a wrapper that hides the
// original is worse than no wrapper, because it makes an unexpected failure look
// like a known one.

// ActionError is a failure with a next step attached.
type ActionError struct {
	// Code is stable for the client to key on.
	Code string `json:"code"`
	// Title is the short heading.
	Title string `json:"title"`
	// What is one sentence saying what happened.
	What string `json:"what"`
	// Next is one sentence saying what to do.
	Next string `json:"next"`
	// Raw is the server's own message, kept verbatim.
	Raw string `json:"raw,omitempty"`
	// Href is where to go to fix it, when there is a place.
	Href string `json:"href,omitempty"`
}

// classifyError maps a server message to an actionable error.
//
// Matching is textual because the store returns formatted errors rather than
// typed ones; the alternative would be a typed error for every rule in the
// store, which is a larger change than this phase. The mapping is deliberately
// narrow: an unrecognised message returns a generic class that still shows the
// raw text, so an unknown failure is never dressed up as a known one.
func classifyError(raw string, status int) ActionError {
	msg := strings.ToLower(raw)
	out := ActionError{Code: "unknown", Title: "That did not work", What: "", Next: "", Raw: raw}

	switch {
	case strings.Contains(msg, "gate checklist incomplete"):
		out.Code = "gate_incomplete"
		out.Title = "A gate still has unchecked items"
		out.What = "Phase jobs and test runs are blocked until every checkbox in the design version's Gate is checked."
		out.Next = "Open the design, check the remaining items, then try again."
		out.Href = "#/delivery/designs"

	case strings.Contains(msg, "acceptance checklist incomplete"):
		out.Code = "acceptance_incomplete"
		out.Title = "The acceptance checklist is not finished"
		out.What = "A milestone can only be accepted once every acceptance item is checked."
		out.Next = "Open the milestone on the Roadmap and finish the acceptance list."
		out.Href = "#/delivery/roadmap"

	case strings.Contains(msg, "campaign job already") || strings.Contains(msg, "already running"):
		out.Code = "already_running"
		out.Title = "Something is already running here"
		out.What = "This milestone or worker already has a job in flight."
		out.Next = "Wait for it to finish, or stop it from the job bar at the top of the page."
		out.Href = "#/jobs"

	case strings.Contains(msg, "not found in cluster inventory"):
		out.Code = "worker_missing"
		out.Title = "That worker is not in the cluster"
		out.What = "The console could not find the worker in any configured fleet, so it cannot route work to it."
		out.Next = "Check that the fleet is reachable and the worker is bound to a project in Clusters."
		out.Href = "#/clusters"

	case strings.Contains(msg, "no scheduler fleet configured") || strings.Contains(msg, "no agentd nodes joined"):
		out.Code = "no_fleet"
		out.Title = "No scheduler is reachable"
		out.What = "The console has no fleet it can talk to, so it cannot start or watch work."
		out.Next = "Start a scheduler and agentd, then add the fleet in Clusters."
		out.Href = "#/clusters"

	case strings.Contains(msg, "ai assist is not configured") || strings.Contains(msg, "not configured"):
		out.Code = "not_configured"
		out.Title = "That feature is not configured"
		out.What = "The server is running without the configuration this action needs."
		out.Next = "Set the provider configuration for anvil-web and restart it."
		out.Href = ""

	case strings.Contains(msg, "major is sealed") || strings.Contains(msg, "sealed"):
		out.Code = "major_sealed"
		out.Title = "This generation is sealed"
		out.What = "A sealed generation accepts no new milestones or edits."
		out.Next = "Create a new generation, or work in the one currently in development."
		out.Href = "#/delivery/requirements"

	case strings.Contains(msg, "already open") || strings.Contains(msg, "session is already open"):
		out.Code = "session_busy"
		out.Title = "That session is open somewhere else"
		out.What = "Two handles on one conversation would interleave their runs into one transcript."
		out.Next = "Close it in the other window or tab, then resume."
		out.Href = ""

	case strings.Contains(msg, "already accepted"):
		out.Code = "already_accepted"
		out.Title = "Already accepted"
		out.What = "This milestone has already been accepted."
		out.Next = "Merge it to main if that is what you meant."
		out.Href = "#/delivery/roadmap"

	case strings.Contains(msg, "only one of") || strings.Contains(msg, "requires status"):
		out.Code = "wrong_state"
		out.Title = "That action does not apply in this state"
		out.What = "The milestone has moved on from the state this action needs."
		out.Next = "Refresh the page to see its current state, then pick the action that matches."
		out.Href = "#/delivery/roadmap"

	case status == http.StatusUnauthorized:
		out.Code = "unauthorized"
		out.Title = "You are signed out"
		out.What = "The session expired or the token was rejected."
		out.Next = "Sign in again."
		out.Href = ""

	case status == http.StatusForbidden:
		out.Code = "forbidden"
		out.Title = "Not allowed"
		out.What = "This account cannot perform that action."
		out.Next = "Ask an administrator for access."
		out.Href = ""

	case status >= 500:
		out.Code = "server_error"
		out.Title = "The server failed"
		out.What = "The request reached the server but it could not complete it."
		out.Next = "Try again; if it keeps failing, check the anvil-web log for the request."
		out.Href = ""
	}

	if out.What == "" {
		// An unrecognised failure is shown as unknown with its raw text, never as a
		// guess. A wrapper that hides the original makes an unexpected failure look
		// like a known one.
		out.What = "The server refused the request and gave no further detail."
		out.Next = "Check the message below; if it is unclear, the anvil-web log has the request."
	}
	return out
}

// handleClassifyError lets the client ask for the actionable form of a message it
// already has.
//
// It is a POST with the raw fields rather than a GET with a query string because a
// server message can be long and can contain newlines.
func (s *Server) handleClassifyError(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var in struct {
		Message string `json:"message"`
		Status  int    `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, classifyError(in.Message, in.Status))
}

// HealthReport is what the shell strip renders (GET /api/v1/health).
type HealthReport struct {
	Fleets  []FleetHealth `json:"fleets"`
	OK      bool          `json:"ok"`
	Summary string        `json:"summary"`
}

// FleetHealth is one fleet's reachability and capacity.
type FleetHealth struct {
	ID        string `json:"id"`
	Name      string `json:"name,omitempty"`
	URL       string `json:"url,omitempty"`
	Reachable bool   `json:"reachable"`
	Error     string `json:"error,omitempty"`
	Nodes     int    `json:"nodes"`
	Idle      int    `json:"idle"`
	Busy      int    `json:"busy"`
}

// handleHealth answers GET /api/v1/health.
//
// It is deliberately shallow and fast: the shell asks on every route change, and a
// health check that is expensive is a health check that gets removed. Each fleet is
// probed with the same ListNodes call the pickers already make, with its own
// timeout, so one dead fleet cannot stall the strip.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	report := HealthReport{Fleets: []FleetHealth{}}
	for _, f := range s.cfg.Fleets {
		fh := FleetHealth{ID: f.ID, URL: f.SchedulerURL}
		cli := &schedclient.Client{
			BaseURL: f.SchedulerURL,
			Token:   s.cfg.SchedulerToken(),
			HTTP:    &http.Client{Timeout: 5 * time.Second},
		}
		raw, err := cli.ListNodes(r.Context())
		if err != nil {
			fh.Reachable = false
			fh.Error = err.Error()
			report.Fleets = append(report.Fleets, fh)
			continue
		}
		fh.Reachable = true
		var wrap struct {
			Nodes []struct {
				Idle *int `json:"idle"`
				Busy *int `json:"busy"`
			} `json:"nodes"`
		}
		if jerr := json.Unmarshal(raw, &wrap); jerr == nil {
			fh.Nodes = len(wrap.Nodes)
			for _, n := range wrap.Nodes {
				if n.Idle != nil {
					fh.Idle += *n.Idle
				}
				if n.Busy != nil {
					fh.Busy += *n.Busy
				}
			}
		}
		report.Fleets = append(report.Fleets, fh)
	}

	reachable := 0
	for _, f := range report.Fleets {
		if f.Reachable {
			reachable++
		}
	}
	report.OK = len(report.Fleets) > 0 && reachable == len(report.Fleets)
	switch {
	case len(report.Fleets) == 0:
		report.Summary = "No fleet configured"
	case reachable == len(report.Fleets):
		report.Summary = "All fleets reachable"
	default:
		report.Summary = "Some fleets unreachable"
	}
	writeJSON(w, http.StatusOK, report)
}
