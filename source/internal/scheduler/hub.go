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
	"github.com/anvil-dev/anvil/internal/web/auth"
)

// NodeInfo is a public snapshot of a joined agentd.
type NodeInfo struct {
	ID            string               `json:"id"`
	MaxWorkers    int                  `json:"max_workers"`
	Idle          int                  `json:"idle"`
	Busy          int                  `json:"busy"`
	Accepting     bool                 `json:"accepting"`
	Workers       []daemon.WorkerState `json:"workers,omitempty"`
	Capabilities  []string             `json:"capabilities,omitempty"`
	LastHeartbeat time.Time            `json:"last_heartbeat"`
	Stale         bool                 `json:"stale"`
}

// UsageTotals aggregates LLM tokens.
type UsageTotals struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
	Jobs         int `json:"jobs"`
}

// UsageReport is GET /v1/usage.
type UsageReport struct {
	Global UsageTotals            `json:"global"`
	Nodes  map[string]UsageTotals `json:"nodes"`
}

type nodeConn struct {
	id        string
	max       int
	idle      int
	busy      int
	accepting bool
	workers   []daemon.WorkerState
	caps      []string
	lastHB    time.Time
	conn      net.Conn
	writeMu   sync.Mutex
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
	jobNode map[string]string // jobID -> nodeID

	usageMu sync.Mutex
	usageG  UsageTotals
	usageN  map[string]UsageTotals

	modelsMu    sync.Mutex
	modelsAt    time.Time
	modelsCache []modelJSON

	sessions *auth.Store // optional; validates anvil-web session Bearers on app HTTP
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
		jobNode: make(map[string]string),
		usageN:  make(map[string]UsageTotals),
	}
}

// SetSessions attaches a web auth store for app-HTTP session Bearer acceptance.
func (h *Hub) SetSessions(s *auth.Store) { h.sessions = s }

// CloseSessions closes the optional web sessions store.
func (h *Hub) CloseSessions() {
	if h.sessions != nil {
		_ = h.sessions.Close()
		h.sessions = nil
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
			Accepting:     n.accepting,
			Workers:       append([]daemon.WorkerState(nil), n.workers...),
			Capabilities:  append([]string(nil), n.caps...),
			LastHeartbeat: n.lastHB,
			Stale:         now.Sub(n.lastHB) > staleAfter,
		})
	}
	return out
}

// UsageSnapshot returns aggregated token usage.
func (h *Hub) UsageSnapshot() UsageReport {
	h.usageMu.Lock()
	defer h.usageMu.Unlock()
	nodes := make(map[string]UsageTotals, len(h.usageN))
	for k, v := range h.usageN {
		nodes[k] = v
	}
	return UsageReport{Global: h.usageG, Nodes: nodes}
}

// PatchNodeRequest is PATCH /v1/nodes/{id}.
type PatchNodeRequest struct {
	MaxWorkers *int  `json:"max_workers,omitempty"`
	Accepting  *bool `json:"accepting,omitempty"`
}

// PatchNode sends cluster.control to a node.
func (h *Hub) PatchNode(nodeID string, req PatchNodeRequest) error {
	h.nodesMu.Lock()
	n, ok := h.nodes[nodeID]
	h.nodesMu.Unlock()
	if !ok {
		return fmt.Errorf("unknown node_id %q", nodeID)
	}
	ctl := daemon.ClusterControlBody{
		SetMaxWorkers: req.MaxWorkers,
		Accepting:     req.Accepting,
	}
	body, _ := daemon.MarshalBody(ctl)
	if err := h.writeNode(n, daemon.Envelope{Kind: daemon.KindClusterControl, Body: body}); err != nil {
		return err
	}
	// Optimistic local update until next HB.
	h.nodesMu.Lock()
	if req.MaxWorkers != nil {
		n.max = *req.MaxWorkers
	}
	if req.Accepting != nil {
		n.accepting = *req.Accepting
	}
	h.nodesMu.Unlock()
	return nil
}

func (h *Hub) registerNode(n *nodeConn) {
	h.nodesMu.Lock()
	defer h.nodesMu.Unlock()
	if old, ok := h.nodes[n.id]; ok && old.conn != n.conn {
		_ = old.conn.Close()
	}
	n.accepting = true
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
	n.accepting = hb.Accepting
	n.workers = append([]daemon.WorkerState(nil), hb.Workers...)
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
		if !n.accepting {
			return nil, fmt.Errorf("node %q is draining", prefer)
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
		if now.Sub(n.lastHB) > staleAfter || !n.accepting {
			continue
		}
		if n.idle > 0 {
			return n, nil
		}
	}
	return nil, fmt.Errorf("no idle accepting agentd workers")
}

func (h *Hub) deliverDone(done daemon.JobDoneBody) {
	h.waitMu.Lock()
	ch := h.waiters[done.JobID]
	nodeID := h.jobNode[done.JobID]
	delete(h.jobNode, done.JobID)
	h.waitMu.Unlock()
	if done.Usage != nil {
		h.recordUsage(nodeID, done.Usage)
	}
	if ch != nil {
		select {
		case ch <- done:
		default:
		}
	}
}

func (h *Hub) recordUsage(nodeID string, u *daemon.TokenUsage) {
	if u == nil {
		return
	}
	h.usageMu.Lock()
	defer h.usageMu.Unlock()
	h.usageG.InputTokens += u.InputTokens
	h.usageG.OutputTokens += u.OutputTokens
	h.usageG.TotalTokens += u.TotalTokens
	h.usageG.Jobs++
	if nodeID == "" {
		nodeID = "_"
	}
	cur := h.usageN[nodeID]
	cur.InputTokens += u.InputTokens
	cur.OutputTokens += u.OutputTokens
	cur.TotalTokens += u.TotalTokens
	cur.Jobs++
	h.usageN[nodeID] = cur
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
	h.jobNode[job.JobID] = n.id
	h.waitMu.Unlock()
	defer func() {
		h.waitMu.Lock()
		delete(h.waiters, job.JobID)
		delete(h.jobNode, job.JobID)
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
