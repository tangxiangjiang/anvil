// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package gateway

import (
	"context"
	"log"
	"net"
	"net/http"
	"time"
)

// Server is the model gateway HTTP server.
type Server struct {
	cfg    Config
	client *http.Client
	log    *log.Logger
	http   *http.Server
	addr   string // actual listen address after Serve
}

// NewServer builds a gateway server (does not listen yet).
func NewServer(cfg Config, logger *log.Logger) (*Server, error) {
	if err := cfg.Normalize(); err != nil {
		return nil, err
	}
	if logger == nil {
		logger = log.Default()
	}
	s := &Server{
		cfg:    cfg,
		client: newHTTPClient(),
		log:    logger,
	}
	mux := http.NewServeMux()
	token := cfg.Token()
	mux.HandleFunc("/v1/models", requireAuth(token, s.handleModels))
	mux.HandleFunc("/v1/chat/completions", requireAuth(token, s.handleChatCompletions))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	s.http = &http.Server{
		Addr:              cfg.Listen,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	return s, nil
}

// Serve listens until ctx is cancelled.
func (s *Server) Serve(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.cfg.Listen)
	if err != nil {
		return err
	}
	s.addr = ln.Addr().String()
	s.log.Printf("anvil-gateway listening on http://%s", s.addr)
	errCh := make(chan error, 1)
	go func() {
		errCh <- s.http.Serve(ln)
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.http.Shutdown(shutdownCtx)
		return nil
	case err := <-errCh:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}

// Close shuts down the server.
func (s *Server) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.http.Shutdown(ctx)
}

// Addr returns the actual listen address after Serve has bound (supports :0).
func (s *Server) Addr() string {
	if s.addr != "" {
		return s.addr
	}
	return s.cfg.Listen
}
