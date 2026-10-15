// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package web

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/anvil-dev/anvil/internal/web/workspace"
)

func (s *Server) handleWorkspaces(w http.ResponseWriter, r *http.Request) {
	if s.workspace == nil {
		http.Error(w, "workspace store unavailable", http.StatusInternalServerError)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/workspaces")
	path = strings.Trim(path, "/")

	if path == "" && r.Method == http.MethodGet {
		fleetID := r.URL.Query().Get("fleet_id")
		if fleetID == "" {
			http.Error(w, "fleet_id required", http.StatusBadRequest)
			return
		}
		ws, err := s.workspace.EnsureDefaultWorkspace(fleetID, "")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		s.syncWorkspaceToken(ws)
		list, err := s.workspace.ListWorkspaces(fleetID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		for _, wsi := range list {
			s.syncWorkspaceToken(wsi)
		}
		writeJSON(w, http.StatusOK, map[string]any{"workspaces": list})
		return
	}

	if path == "" && r.Method == http.MethodPost {
		var body struct {
			FleetID string `json:"fleet_id"`
			Name    string `json:"name"`
			Summary string `json:"summary"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		ws, err := s.workspace.CreateWorkspace(body.FleetID, body.Name, body.Summary)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.syncWorkspaceToken(ws)
		writeJSON(w, http.StatusCreated, ws)
		return
	}

	parts := strings.Split(path, "/")
	id := parts[0]
	if id == "" {
		http.NotFound(w, r)
		return
	}

	if len(parts) == 1 && r.Method == http.MethodGet {
		ws, err := s.workspace.GetWorkspace(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		writeJSON(w, http.StatusOK, ws)
		return
	}

	if len(parts) == 1 && r.Method == http.MethodDelete {
		if err := s.workspace.DeleteWorkspace(id); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}

	if len(parts) == 2 && parts[1] == "graph" && r.Method == http.MethodGet {
		g, err := s.workspace.Graph(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, g)
		return
	}

	if len(parts) >= 2 && parts[1] == "projects" {
		s.handleWorkspaceProjects(w, r, id, parts[2:])
		return
	}

	http.NotFound(w, r)
}

func (s *Server) handleWorkspaceProjects(w http.ResponseWriter, r *http.Request, wsID string, rest []string) {
	if len(rest) == 0 {
		if r.Method == http.MethodGet {
			list, err := s.workspace.ListProjects(wsID)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"projects": list})
			return
		}
		if r.Method == http.MethodPost {
			var body struct {
				Key       string   `json:"key"`
				Name      string   `json:"name"`
				Summary   string   `json:"summary"`
				DependsOn []string `json:"depends_on"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "bad json", http.StatusBadRequest)
				return
			}
			p, err := s.workspace.CreateProject(workspace.CreateProjectInput{
				WorkspaceID: wsID, Key: body.Key, Name: body.Name,
				Summary: body.Summary, DependsOn: body.DependsOn,
			})
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			writeJSON(w, http.StatusCreated, p)
			return
		}
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	projID := rest[0]
	if len(rest) == 1 && r.Method == http.MethodGet {
		p, err := s.workspace.GetProject(projID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		binds, _ := s.workspace.ListBindingsForProject(projID)
		writeJSON(w, http.StatusOK, map[string]any{"project": p, "bindings": binds})
		return
	}
	if len(rest) == 1 && r.Method == http.MethodDelete {
		if err := s.workspace.DeleteProject(projID); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	if len(rest) == 1 && r.Method == http.MethodPatch {
		var body struct {
			DependsOn []string `json:"depends_on"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		if err := s.workspace.SetProjectDeps(projID, body.DependsOn); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		p, _ := s.workspace.GetProject(projID)
		writeJSON(w, http.StatusOK, p)
		return
	}
	if len(rest) == 2 && rest[1] == "bind-worker" && r.Method == http.MethodPost {
		var body struct {
			WorkerID  string   `json:"worker_id"`
			FleetID   string   `json:"fleet_id"`
			NodeID    string   `json:"node_id"`
			DependsOn []string `json:"depends_on"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		if err := s.workspace.BindWorker(body.WorkerID, projID, body.FleetID, body.NodeID); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if body.DependsOn != nil {
			_ = s.workspace.SetWorkerDeps(body.WorkerID, body.DependsOn)
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	if len(rest) == 2 && rest[1] == "unbind-worker" && r.Method == http.MethodPost {
		var body struct {
			WorkerID string `json:"worker_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		_ = s.workspace.UnbindWorker(body.WorkerID)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	http.NotFound(w, r)
}

func (s *Server) syncWorkspaceToken(ws workspace.Workspace) {
	cli, _, ok := s.clientForFleet(ws.FleetID)
	if !ok || cli == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = cli.RegisterWorkspace(ctx, ws.ID, ws.Token, ws.Name)
}
