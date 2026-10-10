// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/anvil-dev/anvil/internal/daemon"
	"github.com/anvil-dev/anvil/internal/id"
)

// AppHandler returns the authenticated app HTTP mux (for ServeApp and tests).
func (h *Hub) AppHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/health", h.withAuth(h.handleHealth))
	mux.HandleFunc("/v1/llm", h.withAuth(h.handleLLM))
	mux.HandleFunc("/v1/nodes", h.withAuth(h.handleNodes))
	mux.HandleFunc("/v1/nodes/", h.withAuth(h.handleNodeByID))
	mux.HandleFunc("/v1/models", h.withAuth(h.handleModels))
	mux.HandleFunc("/v1/jobs", h.withAuth(h.handleJobs))
	mux.HandleFunc("/v1/usage", h.withAuth(h.handleUsage))
	return mux
}

// ServeApp serves the HTTP app plane until ctx is done.
func (h *Hub) ServeApp(ctx context.Context) error {
	srv := &http.Server{
		Addr:              h.cfg.ListenApp,
		Handler:           h.AppHandler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	ln, err := net.Listen("tcp", h.cfg.ListenApp)
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	h.log.Printf("scheduler app listen http://%s", h.cfg.ListenApp)
	err = srv.Serve(ln)
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

func (h *Hub) withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		want := h.Token()
		if want == "" {
			http.Error(w, "scheduler token not configured", http.StatusServiceUnavailable)
			return
		}
		got := bearer(r.Header.Get("Authorization"))
		if got == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if got == want {
			next(w, r)
			return
		}
		// Optional: accept anvil-web session Bearers (same SQLite as web db_path).
		if h.sessions != nil {
			if _, err := h.sessions.LookupSession(got); err == nil {
				next(w, r)
				return
			}
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}
}

func bearer(h string) string {
	const p = "Bearer "
	if strings.HasPrefix(h, p) {
		return strings.TrimSpace(h[len(p):])
	}
	return ""
}

func (h *Hub) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "nodes": len(h.ListNodes())})
}

type llmPublic struct {
	BaseURL   string `json:"base_url,omitempty"`
	APIKeySet bool   `json:"api_key_set"`
	Model     string `json:"model,omitempty"`
	Provider  string `json:"provider,omitempty"`
	MaxSteps  int    `json:"max_steps,omitempty"`
}

type llmPut struct {
	BaseURL  string `json:"base_url"`
	APIKey   string `json:"api_key"`
	Model    string `json:"model"`
	Provider string `json:"provider"`
	MaxSteps int    `json:"max_steps"`
}

func (h *Hub) handleLLM(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		llm := h.GetLLM()
		writeJSON(w, http.StatusOK, llmPublic{
			BaseURL:   llm.BaseURL,
			APIKeySet: llm.APIKey != "",
			Model:     llm.Model,
			Provider:  llm.Provider,
			MaxSteps:  llm.MaxSteps,
		})
	case http.MethodPut:
		var body llmPut
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		cur := h.GetLLM()
		if body.BaseURL != "" {
			cur.BaseURL = body.BaseURL
		}
		if body.APIKey != "" {
			cur.APIKey = body.APIKey
		}
		if body.Model != "" {
			cur.Model = body.Model
		}
		if body.Provider != "" {
			cur.Provider = body.Provider
		}
		if body.MaxSteps > 0 {
			cur.MaxSteps = body.MaxSteps
		}
		h.SetLLM(cur)
		writeJSON(w, http.StatusOK, llmPublic{
			BaseURL:   cur.BaseURL,
			APIKeySet: cur.APIKey != "",
			Model:     cur.Model,
			Provider:  cur.Provider,
			MaxSteps:  cur.MaxSteps,
		})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Hub) handleNodes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"nodes": h.ListNodes()})
}

func (h *Hub) handleNodeByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/v1/nodes/")
	id = strings.Trim(id, "/")
	if id == "" || strings.Contains(id, "/") {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	switch r.Method {
	case http.MethodPatch:
		var req PatchNodeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		if req.MaxWorkers == nil && req.Accepting == nil {
			http.Error(w, "max_workers or accepting required", http.StatusBadRequest)
			return
		}
		if err := h.PatchNode(id, req); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id})
	case http.MethodGet:
		for _, n := range h.ListNodes() {
			if n.ID == id {
				writeJSON(w, http.StatusOK, n)
				return
			}
		}
		http.Error(w, "not found", http.StatusNotFound)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Hub) handleUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, h.UsageSnapshot())
}

func (h *Hub) handleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	list, err := h.ListModels(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": list})
}

type jobRequest struct {
	JobID     string   `json:"job_id"`
	RepoID    string   `json:"repo_id"`
	Prompt    string   `json:"prompt"`
	ResumeID  string   `json:"resume_id"`
	MaxSteps  int      `json:"max_steps"`
	Model     string   `json:"model"`
	Provider  string   `json:"provider"`
	Plugins   []string `json:"plugins"`
	NodeID    string   `json:"node_id"`
	ForceStub bool     `json:"force_stub"`
}

func (h *Hub) handleJobs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req jobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if req.RepoID == "" || req.Prompt == "" {
		http.Error(w, "repo_id and prompt are required", http.StatusBadRequest)
		return
	}
	jobID := req.JobID
	if jobID == "" {
		jobID = id.New()
	}
	job := daemon.JobAssignBody{
		JobID:     jobID,
		RepoID:    req.RepoID,
		Prompt:    req.Prompt,
		ResumeID:  req.ResumeID,
		MaxSteps:  req.MaxSteps,
		Model:     req.Model,
		Provider:  req.Provider,
		Plugins:   req.Plugins,
		ForceStub: req.ForceStub,
	}
	if job.Model == "" {
		job.Model = h.GetLLM().Model
	}
	done, err := h.AssignJobTo(r.Context(), job, req.NodeID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	writeJSON(w, http.StatusOK, done)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// Run starts cluster and app listeners; returns the first fatal error.
func (h *Hub) Run(ctx context.Context) error {
	errCh := make(chan error, 2)
	go func() { errCh <- h.ServeCluster(ctx) }()
	go func() { errCh <- h.ServeApp(ctx) }()
	select {
	case <-ctx.Done():
		return nil
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("scheduler: %w", err)
		}
		return nil
	}
}
