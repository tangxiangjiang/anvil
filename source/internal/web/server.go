// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package web

import (
	"context"
	"encoding/json"
	"io/fs"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/anvil-dev/anvil/internal/web/auth"
	"github.com/anvil-dev/anvil/internal/web/delivery"
	"github.com/anvil-dev/anvil/internal/web/schedclient"
	"github.com/anvil-dev/anvil/internal/web/workspace"
)

// Server is the anvil-web HTTP server.
type Server struct {
	cfg       Config
	store     *auth.Store
	delivery  *delivery.Store
	workspace *workspace.Store
	sched     *schedclient.Client
	static    fs.FS
	log       *log.Logger
}

// New constructs a server. staticFS is typically embed FS rooted at static/.
// deliveryStore / workspaceStore may be nil; production main always opens them.
func New(cfg Config, store *auth.Store, deliveryStore *delivery.Store, workspaceStore *workspace.Store, staticFS fs.FS, logger *log.Logger) *Server {
	if logger == nil {
		logger = log.Default()
	}
	return &Server{
		cfg:       cfg,
		store:     store,
		delivery:  deliveryStore,
		workspace: workspaceStore,
		sched:     &schedclient.Client{BaseURL: cfg.SchedulerURL, Token: cfg.SchedulerToken()},
		static:    staticFS,
		log:       logger,
	}
}

// Handler returns the root mux.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/setup/status", s.handleSetupStatus)
	mux.HandleFunc("/api/v1/setup/admin", s.handleSetupAdmin)
	mux.HandleFunc("/api/v1/auth/login", s.handleLogin)
	mux.HandleFunc("/api/v1/auth/logout", s.requireAuth(s.handleLogout))
	mux.HandleFunc("/api/v1/auth/me", s.requireAuth(s.handleMe))
	mux.HandleFunc("/api/v1/fleets", s.requireAuth(s.handleFleets))
	mux.HandleFunc("/api/v1/clusters", s.requireAuth(s.handleClusters))
	mux.HandleFunc("/api/v1/clusters/", s.requireAuth(s.handleClusterRoutes))
	mux.HandleFunc("/api/v1/workspaces", s.requireAuth(s.handleWorkspaces))
	mux.HandleFunc("/api/v1/workspaces/", s.requireAuth(s.handleWorkspaces))
	mux.HandleFunc("/api/v1/nodes", s.requireAuth(s.handleNodes))
	mux.HandleFunc("/api/v1/nodes/", s.requireAuth(s.handleNodePatch))
	mux.HandleFunc("/api/v1/usage", s.requireAuth(s.handleUsage))
	mux.HandleFunc("/api/v1/delivery", s.requireAuth(s.handleDelivery))
	mux.HandleFunc("/api/v1/delivery/", s.requireAuth(s.handleDelivery))

	if s.static != nil {
		fileServer := http.FileServer(http.FS(s.static))
		mux.Handle("/", spaFallback(fileServer))
	}
	return mux
}

func spaFallback(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Run listens until ctx done.
func (s *Server) Run(ctx context.Context) error {
	srv := &http.Server{
		Addr:              s.cfg.Listen,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	ln, err := net.Listen("tcp", s.cfg.Listen)
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		shCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shCtx)
	}()
	s.log.Printf("anvil-web listen http://%s", s.cfg.Listen)
	err = srv.Serve(ln)
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok := bearer(r.Header.Get("Authorization"))
		user, err := s.store.LookupSession(tok)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		ctx := context.WithValue(r.Context(), ctxUser{}, user)
		next(w, r.WithContext(ctx))
	}
}

type ctxUser struct{}

func userFrom(ctx context.Context) auth.User {
	u, _ := ctx.Value(ctxUser{}).(auth.User)
	return u
}

func bearer(h string) string {
	const p = "Bearer "
	if strings.HasPrefix(h, p) {
		return strings.TrimSpace(h[len(p):])
	}
	return ""
}

func (s *Server) handleSetupStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	need, err := s.store.NeedsSetup()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	allowed := setupAllowed(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"needs_setup":   need,
		"setup_allowed": allowed,
	})
}

func (s *Server) handleSetupAdmin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !setupAllowed(r) {
		http.Error(w, "setup only allowed from localhost or 192.168.0.0/16", http.StatusForbidden)
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	user, err := s.store.CreateAdmin(body.Username, body.Password)
	if err != nil {
		if err.Error() == "setup already completed" {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	tok, _, err := s.store.Login(user.Username, body.Password)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":   true,
			"user": map[string]any{"id": user.ID, "username": user.Username},
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"token":      tok,
		"expires_in": int(s.cfg.SessionTTL().Seconds()),
		"user":       map[string]any{"id": user.ID, "username": user.Username},
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	tok, user, err := s.store.Login(body.Username, body.Password)
	if err != nil {
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token":      tok,
		"expires_in": int(s.cfg.SessionTTL().Seconds()),
		"user":       map[string]any{"id": user.ID, "username": user.Username},
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.store.Logout(bearer(r.Header.Get("Authorization")))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	u := userFrom(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"id": u.ID, "username": u.Username})
}

func (s *Server) handleFleets(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"fleets": s.cfg.FleetList()})
}

func (s *Server) handleNodes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	raw, err := s.sched.ListNodes(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}

func (s *Server) handleNodePatch(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/nodes/")
	id = strings.Trim(id, "/")
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPatch {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if err := s.sched.PatchNode(r.Context(), id, body); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id})
}

func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	raw, err := s.sched.Usage(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
