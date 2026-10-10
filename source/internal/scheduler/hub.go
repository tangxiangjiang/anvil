// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package scheduler

import (
	"context"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"github.com/anvil-dev/anvil/internal/daemon"
)

// NodeInfo is a public snapshot of a joined agentd.
type NodeInfo struct {
	ID            string    `json:"id"`
	MaxWorkers    int       `json:"max_workers"`
	Idle          int       `json:"idle"`
	Busy          int       `json:"busy"`
	Capabilities  []string  `json:"capabilities,omitempty"`
	LastHeartbeat time.Time `json:"last_heartbeat"`
	Stale         bool      `json:"stale"`
}

type nodeConn struct {
	id      string
	max     int
	idle    int
	busy    int
	caps    []string
	lastHB  time.Time
	conn    net.Conn
	writeMu sync.Mutex
}

// Hub is the scheduler control plane (cluster + app state).
type Hub struct {
	cfg Config
	log *log.Logger

	llmMu sync.RWMutex
	llm   daemon.ClusterLLMBody

	nodesMu sync.Mutex
	nodes   map[string]*nodeConn
	rr      int

	waitMu  sync.Mutex
	waiters map[string]chan daemon.JobDoneBody

	modelsMu    sync.Mutex
	modelsAt    time.Time
	modelsCache []modelJSON
}

// NewHub constructs a hub with bootstrap llm.
func NewHub(cfg Config, logger *log.Logger) *Hub {
	if logger == nil {
		logger = log.Default()
	}
	return &Hub{
		cfg:     cfg,
		log:     logger,
		llm:     cfg.BootstrapLLM(),
		nodes:   make(map[string]*nodeConn),
		waiters: make(map[string]chan daemon.JobDoneBody),
	}
}

// Token returns the auth token.
func (h *Hub) Token() string { return h.cfg.Token() }

// GetLLM returns a copy of current llm (includes api_key; caller must redact for HTTP GET).
func (h *Hub) GetLLM() daemon.ClusterLLMBody {
	h.llmMu.RLock()
	defer h.llmMu.RUnlock()
	return h.llm
}

// SetLLM replaces llm and fans out cluster.config to joined nodes.
func (h *Hub) SetLLM(body daemon.ClusterLLMBody) {
	h.llmMu.Lock()
	h.llm = body
	h.llmMu.Unlock()
	h.modelsMu.Lock()
	h.modelsAt = time.Time{}
	h.modelsCache = nil
	h.modelsMu.Unlock()
	h.broadcastConfig(body)
}

func (h *Hub) broadcastConfig(body daemon.ClusterLLMBody) {
	payload, _ := daemon.MarshalBody(daemon.ClusterConfigBody{LLM: &body})
	h.nodesMu.Lock()
	defer h.nodesMu.Unlock()
	for _, n := range h.nodes {
		_ = h.writeNode(n, daemon.Envelope{Kind: daemon.KindClusterConfig, Body: payload})
	}
}

func (h *Hub) writeNode(n *nodeConn, env daemon.Envelope) error {
	n.writeMu.Lock()
	defer n.writeMu.Unlock()
	return daemon.WriteEnvelope(n.conn, env)
}

// ListNodes returns capacity snapshots.
func (h *Hub) ListNodes() []NodeInfo {
	h.nodesMu.Lock()
	defer h.nodesMu.Unlock()
	staleAfter := time.Duration(h.cfg.StaleSec) * time.Second
	now := time.Now()
	out := make([]NodeInfo, 0, len(h.nodes))
	for _, n := range h.nodes {
		out = append(out, NodeInfo{
			ID:            n.id,
			MaxWorkers:    n.max,
			Idle:          n.idle,
			Busy:          n.busy,
			Capabilities:  append([]string(nil), n.caps...),
			LastHeartbeat: n.lastHB,
			Stale:         now.Sub(n.lastHB) > staleAfter,
		})
	}
	return out
}

func (h *Hub) registerNode(n *nodeConn) {
	h.nodesMu.Lock()
	defer h.nodesMu.Unlock()
	if old, ok := h.nodes[n.id]; ok && old.conn != n.conn {
		_ = old.conn.Close()
	}
	h.nodes[n.id] = n
}

func (h *Hub) unregisterNode(id string, conn net.Conn) {
	h.nodesMu.Lock()
	defer h.nodesMu.Unlock()
	if cur, ok := h.nodes[id]; ok && cur.conn == conn {
		delete(h.nodes, id)
	}
}

func (h *Hub) updateHeartbeat(id string, hb daemon.ClusterHeartbeatBody) {
	h.nodesMu.Lock()
	defer h.nodesMu.Unlock()
	n, ok := h.nodes[id]
	if !ok {
		return
	}
	n.idle = hb.Idle
	n.busy = hb.Busy
	n.max = hb.Max
	n.caps = append([]string(nil), hb.Capabilities...)
	n.lastHB = time.Now()
}

func (h *Hub) pickNodeID(prefer string) (*nodeConn, error) {
	h.nodesMu.Lock()
	defer h.nodesMu.Unlock()
	if len(h.nodes) == 0 {
		return nil, fmt.Errorf("no agentd nodes joined")
	}
	staleAfter := time.Duration(h.cfg.StaleSec) * time.Second
	now := time.Now()

	if prefer != "" {
		n, ok := h.nodes[prefer]
		if !ok {
			return nil, fmt.Errorf("unknown node_id %q", prefer)
		}
		if now.Sub(n.lastHB) > staleAfter {
			return nil, fmt.Errorf("node %q is stale", prefer)
		}
		if n.idle <= 0 {
			return nil, fmt.Errorf("node %q has no idle workers", prefer)
		}
		return n, nil
	}

	ids := make([]string, 0, len(h.nodes))
	for id := range h.nodes {
		ids = append(ids, id)
	}
	for i := 0; i < len(ids); i++ {
		h.rr = (h.rr + 1) % len(ids)
		n := h.nodes[ids[h.rr]]
		if now.Sub(n.lastHB) > staleAfter {
			continue
		}
		if n.idle > 0 {
			return n, nil
		}
	}
	return nil, fmt.Errorf("no idle agentd workers")
}

func (h *Hub) deliverDone(done daemon.JobDoneBody) {
	h.waitMu.Lock()
	ch := h.waiters[done.JobID]
	h.waitMu.Unlock()
	if ch != nil {
		select {
		case ch <- done:
		default:
		}
	}
}

// AssignJobTo runs a job on preferNode (empty = round-robin idle).
func (h *Hub) AssignJobTo(ctx context.Context, job daemon.JobAssignBody, preferNode string) (daemon.JobDoneBody, error) {
	if job.JobID == "" {
		return daemon.JobDoneBody{}, fmt.Errorf("job_id is required")
	}
	n, err := h.pickNodeID(preferNode)
	if err != nil {
		return daemon.JobDoneBody{}, err
	}
	ch := make(chan daemon.JobDoneBody, 1)
	h.waitMu.Lock()
	h.waiters[job.JobID] = ch
	h.waitMu.Unlock()
	defer func() {
		h.waitMu.Lock()
		delete(h.waiters, job.JobID)
		h.waitMu.Unlock()
	}()

	body, _ := daemon.MarshalBody(job)
	if err := h.writeNode(n, daemon.Envelope{Kind: daemon.KindClusterAssign, Body: body}); err != nil {
		return daemon.JobDoneBody{}, fmt.Errorf("assign to %s: %w", n.id, err)
	}
	h.nodesMu.Lock()
	if n.idle > 0 {
		n.idle--
		n.busy++
	}
	h.nodesMu.Unlock()

	select {
	case <-ctx.Done():
		return daemon.JobDoneBody{}, ctx.Err()
	case done := <-ch:
		return done, nil
	}
}
