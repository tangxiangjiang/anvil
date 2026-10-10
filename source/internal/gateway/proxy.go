// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package gateway

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

func (s *Server) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{
			"error": map[string]any{"message": "method not allowed", "type": "invalid_request"},
		})
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 32<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": map[string]any{"message": "read body failed", "type": "invalid_request"},
		})
		return
	}
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": map[string]any{"message": "invalid json", "type": "invalid_request"},
		})
		return
	}
	modelID, _ := req["model"].(string)
	route, up, err := s.cfg.ResolveModel(modelID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{
			"error": map[string]any{"message": err.Error(), "type": "invalid_request"},
		})
		return
	}
	key := os.Getenv(up.APIKeyEnv)
	if key == "" {
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"error": map[string]any{"message": "upstream credentials not configured", "type": "server_error"},
		})
		return
	}
	req["model"] = route.UpstreamModel
	forward, err := json.Marshal(req)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": map[string]any{"message": "rewrite failed", "type": "server_error"},
		})
		return
	}

	url := up.BaseURL + "/chat/completions"
	upReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, url, bytes.NewReader(forward))
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"error": map[string]any{"message": "upstream request build failed", "type": "server_error"},
		})
		return
	}
	upReq.Header.Set("Content-Type", "application/json")
	upReq.Header.Set("Authorization", "Bearer "+key)
	if accept := r.Header.Get("Accept"); accept != "" {
		upReq.Header.Set("Accept", accept)
	}

	resp, err := s.client.Do(upReq)
	if err != nil {
		s.log.Printf("upstream error model=%s: %v", route.ID, err)
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"error": map[string]any{"message": "upstream unreachable", "type": "server_error"},
		})
		return
	}
	defer resp.Body.Close()

	ct := resp.Header.Get("Content-Type")
	if ct != "" {
		w.Header().Set("Content-Type", ct)
	} else {
		w.Header().Set("Content-Type", "application/json")
	}
	// Pass through streaming-related headers when present.
	if v := resp.Header.Get("Cache-Control"); v != "" {
		w.Header().Set("Cache-Control", v)
	}
	w.WriteHeader(resp.StatusCode)

	stream := false
	if b, ok := req["stream"].(bool); ok && b {
		stream = true
	}
	if stream || strings.Contains(ct, "text/event-stream") {
		flusher, ok := w.(http.Flusher)
		buf := make([]byte, 32*1024)
		for {
			n, readErr := resp.Body.Read(buf)
			if n > 0 {
				if _, werr := w.Write(buf[:n]); werr != nil {
					return
				}
				if ok {
					flusher.Flush()
				}
			}
			if readErr != nil {
				return
			}
		}
	}
	_, _ = io.Copy(w, resp.Body)
}

func newHTTPClient() *http.Client {
	return &http.Client{Timeout: 120 * time.Second}
}