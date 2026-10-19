// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/anvil-dev/anvil/internal/web/delivery"
)

func (s *Server) handleDelivery(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/delivery")
	path = strings.Trim(path, "/")
	if s.delivery == nil {
		http.Error(w, "delivery store unavailable", http.StatusInternalServerError)
		return
	}

	if path == "" && r.Method == http.MethodGet {
		snap, err := s.delivery.Snapshot()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// Supplement workers from seed helpers when empty labels only from eng_reqs.
		if len(snap.Workers) == 0 {
			snap.Workers = []delivery.WorkerRef{
				{ID: "w_flutter_chat", Label: "flutter-chat-local", Fleet: "local", Node: "dev-node", ProjectRel: "flutter-chat-local"},
				{ID: "w_demo", Label: "demo", Fleet: "local", Node: "dev-node", ProjectRel: "demo"},
			}
		} else {
			snap.Workers = mergeWorkerHelpers(snap.Workers)
		}
		writeJSON(w, http.StatusOK, snap)
		return
	}

	if path == "reset" && r.Method == http.MethodPost {
		if err := s.delivery.Reset(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		snap, err := s.delivery.Snapshot()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		snap.Workers = mergeWorkerHelpers(snap.Workers)
		writeJSON(w, http.StatusOK, snap)
		return
	}

	if path == "majors" && r.Method == http.MethodGet {
		list, err := s.delivery.ListMajors()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"majors": list})
		return
	}

	if path == "majors" && r.Method == http.MethodPost {
		var body struct {
			Major   *int   `json:"major"`
			Title   string `json:"title"`
			Status  string `json:"status"`
			Summary string `json:"summary"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		maj := 0
		if body.Major != nil {
			maj = *body.Major
		} else {
			n, err := s.delivery.NextMajorNumber()
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			maj = n
		}
		m, err := s.delivery.AddMajor(maj, body.Title, delivery.MajorPlanning, body.Summary)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusCreated, m)
		return
	}

	if strings.HasPrefix(path, "majors/") {
		raw := strings.TrimPrefix(path, "majors/")
		// majors/{n}/preview-delete | majors/{n}
		parts := strings.Split(raw, "/")
		maj, err := strconv.Atoi(parts[0])
		if err != nil || maj < 0 {
			http.Error(w, "bad major", http.StatusBadRequest)
			return
		}
		if len(parts) == 2 && parts[1] == "preview-delete" && r.Method == http.MethodGet {
			ms, vers, err := s.delivery.CountMilestonesUnderMajor(maj)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"major": maj, "milestone_count": ms, "version_count": vers,
			})
			return
		}
		if len(parts) == 2 && parts[1] == "advance" && r.Method == http.MethodPost {
			m, err := s.delivery.AdvanceMajor(maj)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			writeJSON(w, http.StatusOK, m)
			return
		}
		if len(parts) == 1 && r.Method == http.MethodPatch {
			var body struct {
				Title   *string `json:"title"`
				Status  *string `json:"status"`
				Summary *string `json:"summary"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "bad json", http.StatusBadRequest)
				return
			}
			m, err := s.delivery.UpdateMajor(maj, body.Title, body.Status, body.Summary)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			writeJSON(w, http.StatusOK, m)
			return
		}
		if len(parts) == 1 && r.Method == http.MethodDelete {
			item, err := s.delivery.DeleteMajorToTrash(maj)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			writeJSON(w, http.StatusOK, item)
			return
		}
	}

	if path == "trash" && r.Method == http.MethodGet {
		list, err := s.delivery.ListTrash()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": list})
		return
	}

	if strings.HasPrefix(path, "trash/") {
		id := strings.TrimPrefix(path, "trash/")
		rest := ""
		if i := strings.Index(id, "/"); i >= 0 {
			rest = id[i+1:]
			id = id[:i]
		}
		if id == "" {
			http.NotFound(w, r)
			return
		}
		if rest == "restore" && r.Method == http.MethodPost {
			var body struct {
				Mode string `json:"mode"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			res, err := s.delivery.RestoreMajorFromTrash(id, body.Mode)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if res.Conflict {
				writeJSON(w, http.StatusConflict, res)
				return
			}
			writeJSON(w, http.StatusOK, res)
			return
		}
		if rest == "" && r.Method == http.MethodDelete {
			if err := s.delivery.PurgeTrash(id); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
			return
		}
	}

	if path == "eng-reqs/ensure-project" && r.Method == http.MethodPost {
		var body struct {
			ProjectID   string `json:"project_id"`
			WorkspaceID string `json:"workspace_id"`
			ProjectKey  string `json:"project_key"`
			Title       string `json:"title"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		eng, err := s.delivery.EnsureProjectEng(body.ProjectID, body.WorkspaceID, body.ProjectKey, body.Title)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, eng)
		return
	}

	if path == "eng-reqs" && r.Method == http.MethodPost {
		var body struct {
			WorkerID       string `json:"worker_id"`
			Label          string `json:"label"`
			Fleet          string `json:"fleet"`
			Node           string `json:"node"`
			ProjectRel     string `json:"project_rel"`
			ProjectID      string `json:"project_id"`
			ProjectKey     string `json:"project_key"`
			WorkspaceID    string `json:"workspace_id"`
			Title          string `json:"title"`
			ReqsRel        string `json:"reqs_rel"`
			Major          *int   `json:"major"`
			Minor          int    `json:"minor"`
			MilestoneTitle string `json:"milestone_title"`
			Status         string `json:"status"`
			Goals          string `json:"goals"`
			NonGoals       string `json:"non_goals"`
			Success        string `json:"success"`
			Constraints    string `json:"constraints"`
			Phases         []struct {
				Title    string `json:"title"`
				Delivery string `json:"delivery"`
			} `json:"phases"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		in := delivery.CreateEngReqInput{
			Worker: delivery.WorkerRef{
				ID: body.WorkerID, Label: body.Label, Fleet: body.Fleet,
				Node: body.Node, ProjectRel: body.ProjectRel,
			},
			ProjectID:      body.ProjectID,
			ProjectKey:     body.ProjectKey,
			WorkspaceID:    body.WorkspaceID,
			Title:          body.Title,
			ReqsRel:        body.ReqsRel,
			Minor:          body.Minor,
			MilestoneTitle: body.MilestoneTitle,
			Status:         body.Status,
			Goals:          body.Goals,
			NonGoals:       body.NonGoals,
			Success:        body.Success,
			Constraints:    body.Constraints,
		}
		for _, p := range body.Phases {
			in.Phases = append(in.Phases, delivery.MilestonePhaseInput{
				Title: p.Title, Delivery: p.Delivery,
			})
		}
		if body.Major == nil {
			in.AutoMajor = true
		} else {
			in.Major = *body.Major
		}
		eng, err := s.delivery.CreateEngReq(in)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusCreated, eng)
		return
	}

	if path == "next-milestone" && r.Method == http.MethodGet {
		engID := r.URL.Query().Get("eng_req_id")
		workerID := r.URL.Query().Get("worker_id")
		major := -1
		if m := r.URL.Query().Get("major"); m != "" {
			if n, err := strconv.Atoi(m); err == nil && n >= 0 {
				major = n
			}
		}
		if engID == "" && workerID != "" {
			id, found, err := s.delivery.EngIDByWorker(workerID)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if found {
				engID = id
			}
		}
		maj, min, err := s.delivery.NextMilestone(engID, major)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"major": maj, "minor": min, "patch": 1,
			"version": fmt.Sprintf("%d.%d", maj, min),
		})
		return
	}

	// eng-reqs/{id}/milestones/{M}/{m}  DELETE
	// eng-reqs/{id}/versions[/{M}/{m}/{p}[/approve]]
	if strings.HasPrefix(path, "eng-reqs/") {
		rest := strings.TrimPrefix(path, "eng-reqs/")
		parts := strings.Split(rest, "/")
		if len(parts) == 4 && parts[1] == "milestones" {
			engID := parts[0]
			major, err1 := strconv.Atoi(parts[2])
			minor, err2 := strconv.Atoi(parts[3])
			if err1 != nil || err2 != nil || major < 0 || minor < 0 {
				http.Error(w, "bad milestone", http.StatusBadRequest)
				return
			}
			if r.Method == http.MethodDelete {
				if err := s.delivery.DeleteMilestone(engID, major, minor); err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				writeJSON(w, http.StatusOK, map[string]any{"ok": true, "eng_req_id": engID, "major": major, "minor": minor})
				return
			}
			if r.Method == http.MethodPatch {
				var body struct {
					Status string `json:"status"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					http.Error(w, "bad json", http.StatusBadRequest)
					return
				}
				if err := s.delivery.SetMilestoneLifecycle(engID, major, minor, body.Status); err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				writeJSON(w, http.StatusOK, map[string]any{
					"ok": true, "eng_req_id": engID, "major": major, "minor": minor, "status": body.Status,
				})
				return
			}
		}
		if len(parts) >= 2 && parts[1] == "versions" {
			engID := parts[0]
			if len(parts) == 2 && r.Method == http.MethodPost {
				var body struct {
					Major int    `json:"major"`
					Minor int    `json:"minor"`
					Patch int    `json:"patch"`
					Title string `json:"title"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					http.Error(w, "bad json", http.StatusBadRequest)
					return
				}
				v, err := s.delivery.AddVersion(delivery.AddVersionInput{
					EngReqID: engID, Major: body.Major, Minor: body.Minor, Patch: body.Patch, Title: body.Title,
				})
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				writeJSON(w, http.StatusCreated, v)
				return
			}
			if len(parts) >= 5 {
				major, err1 := strconv.Atoi(parts[2])
				minor, err2 := strconv.Atoi(parts[3])
				patch, err3 := strconv.Atoi(parts[4])
				if err1 != nil || err2 != nil || err3 != nil {
					http.Error(w, "bad version", http.StatusBadRequest)
					return
				}
				if len(parts) == 6 && parts[5] == "approve" && r.Method == http.MethodPost {
					v, err := s.delivery.ApproveVersion(engID, major, minor, patch)
					if err != nil {
						http.Error(w, err.Error(), http.StatusBadRequest)
						return
					}
					writeJSON(w, http.StatusOK, v)
					return
				}
				if len(parts) == 5 && r.Method == http.MethodPatch {
					var body struct {
						Title       *string `json:"title"`
						Problem     *string `json:"problem"`
						Goals       *string `json:"goals"`
						NonGoals    *string `json:"non_goals"`
						Constraints *string `json:"constraints"`
						Success     *string `json:"success"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						http.Error(w, "bad json", http.StatusBadRequest)
						return
					}
					v, err := s.delivery.UpdateVersion(delivery.UpdateVersionInput{
						EngReqID: engID, Major: major, Minor: minor, Patch: patch,
						Title: body.Title, Problem: body.Problem, Goals: body.Goals,
						NonGoals: body.NonGoals, Constraints: body.Constraints, Success: body.Success,
					})
					if err != nil {
						http.Error(w, err.Error(), http.StatusBadRequest)
						return
					}
					writeJSON(w, http.StatusOK, v)
					return
				}
				if len(parts) == 5 && r.Method == http.MethodDelete {
					if err := s.delivery.DeleteVersion(engID, major, minor, patch); err != nil {
						http.Error(w, err.Error(), http.StatusBadRequest)
						return
					}
					writeJSON(w, http.StatusOK, map[string]any{
						"ok": true, "eng_req_id": engID, "major": major, "minor": minor, "patch": patch,
					})
					return
				}
			}
		}
	}

	if path == "designs" && r.Method == http.MethodPost {
		var body struct {
			EngReqID string `json:"eng_req_id"`
			Major    int    `json:"major"`
			Minor    int    `json:"minor"`
			Title    string `json:"title"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		d, err := s.delivery.CreateDesign(delivery.CreateDesignInput{
			EngReqID: body.EngReqID, Major: body.Major, Minor: body.Minor, Title: body.Title,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusCreated, d)
		return
	}

	// designs/{id}[/approve|/bootstrap|/versions|/versions/{vid}]
	if strings.HasPrefix(path, "designs/") {
		rest := strings.TrimPrefix(path, "designs/")
		parts := strings.Split(rest, "/")
		id := parts[0]
		if len(parts) == 2 && parts[1] == "approve" && r.Method == http.MethodPost {
			d, err := s.delivery.ApproveDesign(id)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			writeJSON(w, http.StatusOK, d)
			return
		}
		if len(parts) == 2 && parts[1] == "bootstrap" && r.Method == http.MethodPost {
			d, err := s.delivery.BootstrapDesignRoadmap(id)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			writeJSON(w, http.StatusOK, d)
			return
		}
		if len(parts) == 2 && parts[1] == "versions" && r.Method == http.MethodPost {
			var body struct {
				Title           string `json:"title"`
				Gate            string `json:"gate"`
				SubArchitecture string `json:"sub_architecture"`
				ClassDesign     string `json:"class_design"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil && r.ContentLength != 0 {
				http.Error(w, "bad json", http.StatusBadRequest)
				return
			}
			v, err := s.delivery.AddDesignVersion(id, delivery.AddDesignVersionInput{
				Title: body.Title, Gate: body.Gate,
				SubArchitecture: body.SubArchitecture, ClassDesign: body.ClassDesign,
			})
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			writeJSON(w, http.StatusCreated, v)
			return
		}
		if len(parts) == 4 && parts[1] == "versions" && parts[3] == "gate-check" && r.Method == http.MethodPost {
			vid := parts[2]
			var body struct {
				Index int  `json:"index"`
				Done  bool `json:"done"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "bad json", http.StatusBadRequest)
				return
			}
			v, err := s.delivery.ToggleDesignVersionGateCheck(id, vid, body.Index, body.Done)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			writeJSON(w, http.StatusOK, v)
			return
		}
		if len(parts) == 3 && parts[1] == "versions" {
			vid := parts[2]
			if r.Method == http.MethodPatch {
				var body struct {
					Title           *string   `json:"title"`
					Gate            *string   `json:"gate"`
					SubArchitecture *string   `json:"sub_architecture"`
					ClassDesign     *string   `json:"class_design"`
					SortOrder       *int      `json:"sort_order"`
					DependsOn       *[]string `json:"depends_on"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					http.Error(w, "bad json", http.StatusBadRequest)
					return
				}
				v, err := s.delivery.PatchDesignVersion(id, vid, delivery.PatchDesignVersionInput{
					Title: body.Title, Gate: body.Gate,
					SubArchitecture: body.SubArchitecture, ClassDesign: body.ClassDesign,
					SortOrder: body.SortOrder, DependsOn: body.DependsOn,
				})
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				writeJSON(w, http.StatusOK, v)
				return
			}
			if r.Method == http.MethodDelete {
				if err := s.delivery.DeleteDesignVersion(id, vid); err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				writeJSON(w, http.StatusOK, map[string]any{"ok": true})
				return
			}
		}
		if len(parts) == 1 && r.Method == http.MethodPatch {
			var body struct {
				ChosenOptionID  *string                 `json:"chosen_option_id"`
				ADR             *string                 `json:"adr"`
				AdrContext      *string                 `json:"adr_context"`
				AdrDecision     *string                 `json:"adr_decision"`
				AdrConsequences *string                 `json:"adr_consequences"`
				AdrSpec         *string                 `json:"adr_spec"`
				AdrArchitecture *string                 `json:"adr_architecture"`
				AdrSequence     *string                 `json:"adr_sequence"`
				AdrSample       *string                 `json:"adr_sample"`
				Options         []delivery.DesignOption `json:"options"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "bad json", http.StatusBadRequest)
				return
			}
			d, err := s.delivery.PatchDesign(id, delivery.PatchDesignInput{
				ChosenOptionID:  body.ChosenOptionID,
				ADR:             body.ADR,
				AdrContext:      body.AdrContext,
				AdrDecision:     body.AdrDecision,
				AdrConsequences: body.AdrConsequences,
				AdrSpec:         body.AdrSpec,
				AdrArchitecture: body.AdrArchitecture,
				AdrSequence:     body.AdrSequence,
				AdrSample:       body.AdrSample,
				Options:         body.Options,
			})
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			writeJSON(w, http.StatusOK, d)
			return
		}
		if len(parts) == 1 && r.Method == http.MethodDelete {
			if err := s.delivery.DeleteDesign(id); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
			return
		}
	}

	// roadmap/milestones/{id} — canvas position / Worker / run_state / Execute
	if strings.HasPrefix(path, "roadmap/milestones/") {
		id := strings.TrimPrefix(path, "roadmap/milestones/")
		id = strings.Trim(id, "/")
		if id != "" && strings.HasSuffix(id, "/merge") && r.Method == http.MethodPost {
			msID := strings.TrimSuffix(id, "/merge")
			msID = strings.Trim(msID, "/")
			var body struct {
				ForceStub bool `json:"force_stub"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			m, err := s.delivery.MergeMilestone(msID, delivery.MergeMilestoneInput{
				ForceStub: body.ForceStub,
				MergeRoot: s.cfg.DeliveryMergeRoot,
			})
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			writeJSON(w, http.StatusOK, m)
			return
		}
		if id != "" && strings.HasSuffix(id, "/acceptance-check") && r.Method == http.MethodPost {
			msID := strings.TrimSuffix(id, "/acceptance-check")
			msID = strings.Trim(msID, "/")
			var body struct {
				Index int  `json:"index"`
				Done  bool `json:"done"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "bad json", http.StatusBadRequest)
				return
			}
			m, err := s.delivery.ToggleMilestoneAcceptanceCheck(msID, body.Index, body.Done)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			writeJSON(w, http.StatusOK, m)
			return
		}
		if id != "" && !strings.Contains(id, "/") && r.Method == http.MethodPatch {
			var body struct {
				X               *float64 `json:"x"`
				Y               *float64 `json:"y"`
				ExecuteWorkerID *string  `json:"execute_worker_id"`
				RunState        *string  `json:"run_state"`
				TestNote        *string  `json:"test_note"`
				Execute         *bool    `json:"execute"`
				SubmitTest      *bool    `json:"submit_test"`
				PassTest        *bool    `json:"pass_test"`
				FailTest        *bool    `json:"fail_test"`
				FailTitle       string   `json:"fail_title"`
				FailBody        string   `json:"fail_body"`
				ReadyDev        *bool    `json:"ready_dev"`
				AcceptMilestone *bool    `json:"accept_milestone"`
				ForceStub       bool     `json:"force_stub"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "bad json", http.StatusBadRequest)
				return
			}
			exec := body.Execute != nil && *body.Execute
			submit := body.SubmitTest != nil && *body.SubmitTest
			pass := body.PassTest != nil && *body.PassTest
			fail := body.FailTest != nil && *body.FailTest
			ready := body.ReadyDev != nil && *body.ReadyDev
			accept := body.AcceptMilestone != nil && *body.AcceptMilestone
			if body.X == nil && body.Y == nil && body.ExecuteWorkerID == nil && body.RunState == nil &&
				body.TestNote == nil && !exec && !submit && !pass && !fail && !ready && !accept {
				http.Error(w, "x/y, execute_worker_id, run_state, test_note, execute, or test/ready/accept action required", http.StatusBadRequest)
				return
			}
			if (body.X != nil) != (body.Y != nil) {
				http.Error(w, "x and y must both be set when moving", http.StatusBadRequest)
				return
			}
			// Pause / Stop: soft-cancel active phase job then set run_state.
			if body.RunState != nil && !exec {
				rs := strings.ToLower(strings.TrimSpace(*body.RunState))
				if rs == delivery.RunPaused || rs == delivery.RunStopped {
					if err := s.softStopMilestoneJobs(id, rs); err != nil {
						http.Error(w, err.Error(), http.StatusBadRequest)
						return
					}
					m, err := s.delivery.GetMilestone(id)
					if err != nil {
						http.Error(w, err.Error(), http.StatusBadRequest)
						return
					}
					writeJSON(w, http.StatusOK, m)
					return
				}
			}
			// 提交测试: stop active jobs first so testing does not race a live phase job.
			if submit {
				_ = s.softStopMilestoneJobs(id, delivery.RunStopped)
			}

			// Start: persist worker, preflight job, then commit Execute; roll back on enqueue failure.
			if exec {
				prev, err := s.delivery.GetMilestone(id)
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				if body.ExecuteWorkerID != nil || body.X != nil || body.Y != nil || body.TestNote != nil {
					if _, err := s.delivery.PatchMilestone(id, delivery.PatchMilestoneInput{
						X:               body.X,
						Y:               body.Y,
						ExecuteWorkerID: body.ExecuteWorkerID,
						TestNote:        body.TestNote,
					}); err != nil {
						http.Error(w, err.Error(), http.StatusBadRequest)
						return
					}
				}
				if _, err := s.preflightMilestonePhaseJob(id, body.ForceStub); err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				m, err := s.delivery.PatchMilestone(id, delivery.PatchMilestoneInput{Execute: true})
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				if _, err := s.startMilestonePhaseJob(id, body.ForceStub); err != nil {
					_ = s.delivery.RestoreMilestoneRun(id, prev.Status, prev.RunState)
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				m, err = s.delivery.GetMilestone(id)
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				writeJSON(w, http.StatusOK, m)
				return
			}

			m, err := s.delivery.PatchMilestone(id, delivery.PatchMilestoneInput{
				X:               body.X,
				Y:               body.Y,
				ExecuteWorkerID: body.ExecuteWorkerID,
				RunState:        body.RunState,
				TestNote:        body.TestNote,
				SubmitTest:      submit,
				PassTest:        pass,
				FailTest:        fail,
				FailTitle:       body.FailTitle,
				FailBody:        body.FailBody,
				ReadyDev:        ready,
				AcceptMilestone: accept,
			})
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			writeJSON(w, http.StatusOK, m)
			return
		}
	}

	if path == "jobs" && r.Method == http.MethodPost {
		var body struct {
			MilestoneID string `json:"milestone_id"`
			ForceStub   bool   `json:"force_stub"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		ph, err := s.startMilestonePhaseJob(body.MilestoneID, body.ForceStub)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, ph)
		return
	}

	if strings.HasPrefix(path, "jobs/") && r.Method == http.MethodGet {
		rest := strings.TrimPrefix(path, "jobs/")
		parts := strings.Split(rest, "/")
		if len(parts) == 2 && parts[1] == "events" {
			s.handleDeliveryJobEvents(w, r, parts[0])
			return
		}
	}

	if path == "buglist" && r.Method == http.MethodPost {
		var body struct {
			EngReqID string `json:"eng_req_id"`
			Major    int    `json:"major"`
			Minor    int    `json:"minor"`
			Title    string `json:"title"`
			Body     string `json:"body"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		b, err := s.delivery.CreateBuglistItem(delivery.CreateBuglistInput{
			EngReqID: body.EngReqID,
			Major:    body.Major,
			Minor:    body.Minor,
			Title:    body.Title,
			Body:     body.Body,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, b)
		return
	}

	if path == "buglist/regress" && r.Method == http.MethodPost {
		var body struct {
			EngReqID  string `json:"eng_req_id"`
			Major     int    `json:"major"`
			Minor     int    `json:"minor"`
			ForceStub bool   `json:"force_stub"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		res, err := s.startRegressJob(body.EngReqID, body.Major, body.Minor, body.ForceStub)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, res)
		return
	}

	if strings.HasPrefix(path, "buglist/") && r.Method == http.MethodPost {
		rest := strings.TrimPrefix(path, "buglist/")
		parts := strings.Split(rest, "/")
		if len(parts) == 2 && parts[1] == "fix" {
			var body struct {
				ForceStub bool `json:"force_stub"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			res, err := s.startBugFixJob(parts[0], body.ForceStub)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			writeJSON(w, http.StatusOK, res)
			return
		}
	}

	if path == "test-cases" && r.Method == http.MethodPost {
		var body struct {
			EngReqID string `json:"eng_req_id"`
			Major    int    `json:"major"`
			Minor    int    `json:"minor"`
			Cases    []struct {
				Title string `json:"title"`
				Body  string `json:"body"`
			} `json:"cases"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		drafts := make([]delivery.TestCaseDraft, 0, len(body.Cases))
		for _, c := range body.Cases {
			drafts = append(drafts, delivery.TestCaseDraft{Title: c.Title, Body: c.Body})
		}
		list, err := s.delivery.CreateTestCases(delivery.CreateTestCasesInput{
			EngReqID: body.EngReqID,
			Major:    body.Major,
			Minor:    body.Minor,
			Cases:    drafts,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"cases": list})
		return
	}

	if path == "test-cases/run" && r.Method == http.MethodPost {
		var body struct {
			EngReqID  string `json:"eng_req_id"`
			Major     int    `json:"major"`
			Minor     int    `json:"minor"`
			ForceStub bool   `json:"force_stub"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		res, err := s.startTestRunJob(body.EngReqID, body.Major, body.Minor, body.ForceStub)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, res)
		return
	}

	if path == "polishes" && r.Method == http.MethodPost {
		var body struct {
			EngReqID string `json:"eng_req_id"`
			Major    int    `json:"major"`
			Minor    int    `json:"minor"`
			Title    string `json:"title"`
			Body     string `json:"body"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		p, err := s.delivery.CreatePolish(delivery.CreatePolishInput{
			EngReqID: body.EngReqID,
			Major:    body.Major,
			Minor:    body.Minor,
			Title:    body.Title,
			Body:     body.Body,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, p)
		return
	}

	if strings.HasPrefix(path, "polishes/") {
		rest := strings.TrimPrefix(path, "polishes/")
		parts := strings.Split(rest, "/")
		if len(parts) == 2 && parts[1] == "run" && r.Method == http.MethodPost {
			var body struct {
				ForceStub bool `json:"force_stub"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			res, err := s.startPolishJob(parts[0], body.ForceStub)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			writeJSON(w, http.StatusOK, res)
			return
		}
	}

	http.NotFound(w, r)
}

func mergeWorkerHelpers(have []delivery.WorkerRef) []delivery.WorkerRef {
	helpers := []delivery.WorkerRef{
		{ID: "w_flutter_chat", Label: "flutter-chat-local", Fleet: "local", Node: "dev-node", ProjectRel: "flutter-chat-local"},
		{ID: "w_demo", Label: "demo", Fleet: "local", Node: "dev-node", ProjectRel: "demo"},
	}
	seen := map[string]bool{}
	var out []delivery.WorkerRef
	for _, w := range have {
		if w.ID == "" || seen[w.ID] {
			continue
		}
		seen[w.ID] = true
		out = append(out, w)
	}
	for _, w := range helpers {
		if seen[w.ID] {
			continue
		}
		seen[w.ID] = true
		out = append(out, w)
	}
	return out
}
