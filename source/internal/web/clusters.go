// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/anvil-dev/anvil/internal/web/schedclient"
)

func (s *Server) clientForFleet(fleetID string) (*schedclient.Client, FleetEntry, bool) {
	for _, f := range s.cfg.FleetList() {
		if f.ID == fleetID {
			return &schedclient.Client{
				BaseURL: f.SchedulerURL,
				Token:   s.cfg.SchedulerToken(),
				HTTP:    &http.Client{Timeout: 15 * time.Second},
			}, f, true
		}
	}
	return nil, FleetEntry{}, false
}

func (s *Server) handleClusters(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	fleets := s.cfg.FleetList()
	type clusterOut struct {
		ID            string          `json:"id"`
		Name          string          `json:"name"`
		Description   string          `json:"description,omitempty"`
		SchedulerURL  string          `json:"scheduler_url"`
		ListenApp     string          `json:"listen_app,omitempty"`
		ListenCluster string          `json:"listen_cluster,omitempty"`
		Online        bool            `json:"online"`
		Error         string          `json:"error,omitempty"`
		Nodes         json.RawMessage `json:"nodes,omitempty"`
	}
	out := make([]clusterOut, len(fleets))
	var wg sync.WaitGroup
	tok := s.cfg.SchedulerToken()
	for i, f := range fleets {
		wg.Add(1)
		go func(i int, f FleetEntry) {
			defer wg.Done()
			c := clusterOut{
				ID:           f.ID,
				Name:         f.Name,
				SchedulerURL: f.SchedulerURL,
			}
			cli := &schedclient.Client{BaseURL: f.SchedulerURL, Token: tok, HTTP: &http.Client{Timeout: 8 * time.Second}}
			ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
			defer cancel()
			meta, err := cli.GetMeta(ctx)
			if err != nil {
				c.Error = err.Error()
				out[i] = c
				return
			}
			c.Online = true
			if meta.Name != "" {
				c.Name = meta.Name
			}
			c.Description = meta.Description
			c.ListenApp = meta.ListenApp
			c.ListenCluster = meta.ListenCluster
			nodes, err := cli.ListNodes(ctx)
			if err != nil {
				c.Error = err.Error()
			} else {
				// ListNodes returns full {"nodes":[...]} — unwrap if needed
				var wrap struct {
					Nodes json.RawMessage `json:"nodes"`
				}
				if json.Unmarshal(nodes, &wrap) == nil && len(wrap.Nodes) > 0 {
					c.Nodes = wrap.Nodes
				} else {
					c.Nodes = nodes
				}
			}
			out[i] = c
		}(i, f)
	}
	wg.Wait()
	writeJSON(w, http.StatusOK, map[string]any{"clusters": out})
}

func (s *Server) handleClusterRoutes(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/v1/clusters/")
	rest = strings.Trim(rest, "/")
	parts := strings.Split(rest, "/")
	if len(parts) < 1 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	fleetID := parts[0]
	if !safeClusterRouteID(fleetID) {
		http.Error(w, "invalid fleet scope", http.StatusBadRequest)
		return
	}
	cli, _, ok := s.clientForFleet(fleetID)
	if !ok {
		http.Error(w, "unknown fleet", http.StatusNotFound)
		return
	}
	// /clusters/{fid}/nodes/{nid}
	// /clusters/{fid}/nodes/{nid}/workers
	// /clusters/{fid}/nodes/{nid}/workers/{wid}
	if len(parts) < 3 || parts[1] != "nodes" {
		http.NotFound(w, r)
		return
	}
	nodeID := parts[2]
	if !safeClusterRouteID(nodeID) {
		http.Error(w, "invalid node scope", http.StatusBadRequest)
		return
	}
	if len(parts) == 3 && r.Method == http.MethodPatch {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		if err := cli.PatchNode(r.Context(), nodeID, body); err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	if len(parts) == 4 && parts[3] == "workers" && r.Method == http.MethodPost {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		raw, err := cli.AddWorker(r.Context(), nodeID, body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(raw)
		return
	}
	if len(parts) == 5 && parts[3] == "workers" && r.Method == http.MethodDelete {
		raw, err := cli.RemoveWorker(r.Context(), nodeID, parts[4])
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(raw)
		return
	}
	if len(parts) == 6 && parts[3] == "workers" && r.Method == http.MethodPost {
		var raw []byte
		var err error
		switch parts[5] {
		case "start":
			raw, err = cli.StartWorker(r.Context(), nodeID, parts[4])
		case "shutdown":
			raw, err = cli.ShutdownWorker(r.Context(), nodeID, parts[4])
		default:
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(raw)
		return
	}
	if len(parts) == 4 && parts[3] == "sandbox" && r.Method == http.MethodGet {
		workerID := strings.TrimSpace(r.URL.Query().Get("worker"))
		var raw json.RawMessage
		var err error
		if workerID == "" {
			raw, err = cli.ListSandbox(r.Context(), nodeID, r.URL.Query().Get("path"))
		} else if safeClusterRouteID(workerID) {
			raw, err = cli.ListWorkerSandbox(r.Context(), nodeID, workerID, r.URL.Query().Get("path"))
		} else {
			http.Error(w, "invalid worker scope", http.StatusBadRequest)
			return
		}
		if err != nil {
			writeSchedulerReviewError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(raw)
		return
	}
	if len(parts) == 5 && parts[3] == "sandbox" && parts[4] == "file" && r.Method == http.MethodGet {
		workerID := strings.TrimSpace(r.URL.Query().Get("worker"))
		if !safeClusterRouteID(workerID) {
			http.Error(w, "invalid worker scope", http.StatusBadRequest)
			return
		}
		raw, err := cli.ReadWorkerFile(r.Context(), nodeID, workerID, r.URL.Query().Get("path"))
		if err != nil {
			writeSchedulerReviewError(w, err)
			return
		}
		writeRawJSON(w, raw)
		return
	}
	if len(parts) == 5 && parts[3] == "git" && r.Method == http.MethodGet {
		workerID := strings.TrimSpace(r.URL.Query().Get("worker"))
		if !safeClusterRouteID(workerID) {
			http.Error(w, "invalid worker scope", http.StatusBadRequest)
			return
		}
		var raw json.RawMessage
		var err error
		switch parts[4] {
		case "status":
			raw, err = cli.GitStatus(r.Context(), nodeID, workerID)
		case "diff":
			raw, err = cli.GitDiff(
				r.Context(), nodeID, workerID, r.URL.Query().Get("path"), r.URL.Query().Get("staged") == "true",
			)
		default:
			http.NotFound(w, r)
			return
		}
		if err != nil {
			writeSchedulerReviewError(w, err)
			return
		}
		writeRawJSON(w, raw)
		return
	}
	if len(parts) == 5 && parts[3] == "sandbox" && parts[4] == "mkdir" && r.Method == http.MethodPost {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		raw, err := cli.MkdirSandbox(r.Context(), nodeID, body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(raw)
		return
	}
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

func safeClusterRouteID(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			continue
		}
		return false
	}
	return true
}

func writeRawJSON(w http.ResponseWriter, raw json.RawMessage) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}

func writeSchedulerReviewError(w http.ResponseWriter, err error) {
	var upstream *schedclient.HTTPError
	if errors.As(err, &upstream) && json.Valid(upstream.Body) {
		status := upstream.Status
		if status < 400 || status > 599 {
			status = http.StatusBadGateway
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(upstream.Body)
		return
	}
	writeJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "code": "scheduler_unreachable", "error": err.Error()})
}
