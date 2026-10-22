// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package scheduler

import (
	"context"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/anvil-dev/anvil/internal/daemon"
	"github.com/anvil-dev/anvil/internal/id"
	"github.com/anvil-dev/anvil/internal/web/auth"
)

// NodeInfo is a public snapshot of a joined agentd.
type NodeInfo struct {
	ID            string               `json:"id"`
	Name          string               `json:"name,omitempty"`
	Description   string               `json:"description,omitempty"`
	Hostname      string               `json:"hostname,omitempty"`
	SandboxDir    string               `json:"sandbox_dir,omitempty"`
	WorkspaceID   string               `json:"workspace_id,omitempty"`
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
	id          string
	name        string
	description string
	hostname    string
	sandboxDir  string
	workspaceID string
	max         int
	idle        int
	busy        int
	accepting   bool
	workers     []daemon.WorkerState
	caps        []string
	lastHB      time.Time
	conn        net.Conn
	writeMu     sync.Mutex
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

	waitMu       sync.Mutex
	waiters      map[string]chan daemon.JobDoneBody
	eventHooks   map[string]func(daemon.JobEventBody)
	jobNode      map[string]string // jobID -> nodeID
	approvalWait map[string]chan daemon.JobApprovalResultBody

	workerWaitMu sync.Mutex
	workerWait   map[string]chan daemon.ClusterWorkerResultBody // req id -> result

	sandboxWaitMu sync.Mutex
	sandboxWait   map[string]chan daemon.ClusterSandboxResultBody

	usageMu sync.Mutex
	usageG  UsageTotals
	usageN  map[string]UsageTotals

	modelsMu    sync.Mutex
	modelsAt    time.Time
	modelsCache []modelJSON

	wsMu       sync.RWMutex
	workspaces map[string]wsReg // token -> registration

	sessions *auth.Store // optional; validates anvil-web session Bearers on app HTTP
}

type wsReg struct {
	ID    string
	Token string
	Name  string
}

// NewHub constructs a hub with bootstrap llm.
func NewHub(cfg Config, logger *log.Logger) *Hub {
	if logger == nil {
		logger = log.Default()
	}
	return &Hub{
		cfg:          cfg,
		log:          logger,
		llm:          cfg.BootstrapLLM(),
		nodes:        make(map[string]*nodeConn),
		waiters:      make(map[string]chan daemon.JobDoneBody),
		eventHooks:   make(map[string]func(daemon.JobEventBody)),
		jobNode:      make(map[string]string),
		approvalWait: make(map[string]chan daemon.JobApprovalResultBody),
		usageN:       make(map[string]UsageTotals),
		workspaces:   make(map[string]wsReg),
		workerWait:   make(map[string]chan daemon.ClusterWorkerResultBody),
		sandboxWait:  make(map[string]chan daemon.ClusterSandboxResultBody),
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
			Name:          n.name,
			Description:   n.description,
			Hostname:      n.hostname,
			SandboxDir:    n.sandboxDir,
			WorkspaceID:   n.workspaceID,
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

// RegisterWorkspace records a workspace token for Dev Node join (0.34+).
func (h *Hub) RegisterWorkspace(id, token, name string) {
	token = strings.TrimSpace(token)
	id = strings.TrimSpace(id)
	if token == "" || id == "" {
		return
	}
	h.wsMu.Lock()
	defer h.wsMu.Unlock()
	if h.workspaces == nil {
		h.workspaces = make(map[string]wsReg)
	}
	h.workspaces[token] = wsReg{ID: id, Token: token, Name: name}
}

// ResolveWorkspaceToken returns workspace id for a join token.
func (h *Hub) ResolveWorkspaceToken(token string) (string, bool) {
	token = strings.TrimSpace(token)
	if token == "" {
		return "", false
	}
	h.wsMu.RLock()
	defer h.wsMu.RUnlock()
	r, ok := h.workspaces[token]
	return r.ID, ok
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

// Meta is GET /v1/meta.
type Meta struct {
	Name          string `json:"name,omitempty"`
	Description   string `json:"description,omitempty"`
	ListenApp     string `json:"listen_app"`
	ListenCluster string `json:"listen_cluster"`
}

// Meta returns scheduler identity for the fleet UI.
func (h *Hub) Meta() Meta {
	name := h.cfg.Name
	if name == "" {
		name = "scheduler"
	}
	return Meta{
		Name:          name,
		Description:   h.cfg.Description,
		ListenApp:     h.cfg.ListenApp,
		ListenCluster: h.cfg.ListenCluster,
	}
}

func (h *Hub) deliverWorkerResult(msg daemon.Envelope) {
	var res daemon.ClusterWorkerResultBody
	_ = daemon.DecodeBody(msg, &res)
	h.workerWaitMu.Lock()
	ch := h.workerWait[msg.ID]
	delete(h.workerWait, msg.ID)
	h.workerWaitMu.Unlock()
	if ch != nil {
		select {
		case ch <- res:
		default:
		}
	}
}

func (h *Hub) deliverSandboxResult(msg daemon.Envelope) {
	var res daemon.ClusterSandboxResultBody
	_ = daemon.DecodeBody(msg, &res)
	h.sandboxWaitMu.Lock()
	ch := h.sandboxWait[msg.ID]
	delete(h.sandboxWait, msg.ID)
	h.sandboxWaitMu.Unlock()
	if ch != nil {
		select {
		case ch <- res:
		default:
		}
	}
}

func (h *Hub) workerRPC(ctx context.Context, nodeID, kind string, req any, timeout time.Duration) (daemon.ClusterWorkerResultBody, error) {
	h.nodesMu.Lock()
	n, ok := h.nodes[nodeID]
	h.nodesMu.Unlock()
	if !ok {
		return daemon.ClusterWorkerResultBody{}, fmt.Errorf("unknown node_id %q", nodeID)
	}
	reqID := id.New()
	ch := make(chan daemon.ClusterWorkerResultBody, 1)
	h.workerWaitMu.Lock()
	h.workerWait[reqID] = ch
	h.workerWaitMu.Unlock()
	defer func() {
		h.workerWaitMu.Lock()
		delete(h.workerWait, reqID)
		h.workerWaitMu.Unlock()
	}()
	body, _ := daemon.MarshalBody(req)
	if err := h.writeNode(n, daemon.Envelope{Kind: kind, ID: reqID, Body: body}); err != nil {
		return daemon.ClusterWorkerResultBody{}, err
	}
	select {
	case <-ctx.Done():
		return daemon.ClusterWorkerResultBody{}, ctx.Err()
	case res := <-ch:
		if !res.OK {
			return res, fmt.Errorf("%s", res.Error)
		}
		return res, nil
	case <-time.After(timeout):
		return daemon.ClusterWorkerResultBody{}, fmt.Errorf("worker rpc timed out")
	}
}

func (h *Hub) applyWorkerResult(nodeID string, res daemon.ClusterWorkerResultBody) {
	h.nodesMu.Lock()
	defer h.nodesMu.Unlock()
	n, ok := h.nodes[nodeID]
	if !ok {
		return
	}
	switch res.Request {
	case "remove":
		out := n.workers[:0]
		for _, w := range n.workers {
			if w.ID != res.Worker.ID {
				out = append(out, w)
			}
		}
		n.workers = out
	default:
		if res.Worker.ID == "" {
			return
		}
		found := false
		for i, w := range n.workers {
			if w.ID == res.Worker.ID {
				n.workers[i] = res.Worker
				found = true
				break
			}
		}
		if !found {
			n.workers = append(n.workers, res.Worker)
		}
	}
	idle, busy := 0, 0
	for _, w := range n.workers {
		switch w.Status {
		case daemon.SlotIdle:
			idle++
		case daemon.SlotBusy, daemon.SlotStarting:
			busy++
		}
	}
	n.idle = idle
	n.busy = busy
	n.max = len(n.workers)
}

// AddWorker asks agentd to create a project worker (stopped until Start).
func (h *Hub) AddWorker(ctx context.Context, nodeID string, req daemon.ClusterWorkerAddBody) (daemon.ClusterWorkerResultBody, error) {
	res, err := h.workerRPC(ctx, nodeID, daemon.KindClusterWorkerAdd, req, 3*time.Minute)
	if err != nil {
		return res, err
	}
	h.applyWorkerResult(nodeID, res)
	return res, nil
}

// RemoveWorker asks agentd to remove a project worker.
func (h *Hub) RemoveWorker(ctx context.Context, nodeID, workerID string) (daemon.ClusterWorkerResultBody, error) {
	res, err := h.workerRPC(ctx, nodeID, daemon.KindClusterWorkerRemove, daemon.ClusterWorkerRemoveBody{WorkerID: workerID}, 60*time.Second)
	if err != nil {
		return res, err
	}
	if res.Worker.ID == "" {
		res.Worker.ID = workerID
	}
	h.applyWorkerResult(nodeID, res)
	return res, nil
}

// StartWorker enables a stopped project worker.
func (h *Hub) StartWorker(ctx context.Context, nodeID, workerID string) (daemon.ClusterWorkerResultBody, error) {
	res, err := h.workerRPC(ctx, nodeID, daemon.KindClusterWorkerStart, daemon.ClusterWorkerControlBody{WorkerID: workerID}, 30*time.Second)
	if err != nil {
		return res, err
	}
	h.applyWorkerResult(nodeID, res)
	return res, nil
}

// ShutdownWorker stops an idle project worker (keeps config).
func (h *Hub) ShutdownWorker(ctx context.Context, nodeID, workerID string) (daemon.ClusterWorkerResultBody, error) {
	res, err := h.workerRPC(ctx, nodeID, daemon.KindClusterWorkerShutdown, daemon.ClusterWorkerControlBody{WorkerID: workerID}, 30*time.Second)
	if err != nil {
		return res, err
	}
	h.applyWorkerResult(nodeID, res)
	return res, nil
}

func (h *Hub) sandboxRPC(ctx context.Context, nodeID, kind string, req any) (daemon.ClusterSandboxResultBody, error) {
	h.nodesMu.Lock()
	n, ok := h.nodes[nodeID]
	h.nodesMu.Unlock()
	if !ok {
		return daemon.ClusterSandboxResultBody{}, fmt.Errorf("unknown node_id %q", nodeID)
	}
	reqID := id.New()
	ch := make(chan daemon.ClusterSandboxResultBody, 1)
	h.sandboxWaitMu.Lock()
	h.sandboxWait[reqID] = ch
	h.sandboxWaitMu.Unlock()
	defer func() {
		h.sandboxWaitMu.Lock()
		delete(h.sandboxWait, reqID)
		h.sandboxWaitMu.Unlock()
	}()
	body, _ := daemon.MarshalBody(req)
	if err := h.writeNode(n, daemon.Envelope{Kind: kind, ID: reqID, Body: body}); err != nil {
		return daemon.ClusterSandboxResultBody{}, err
	}
	select {
	case <-ctx.Done():
		return daemon.ClusterSandboxResultBody{}, ctx.Err()
	case res := <-ch:
		if !res.OK {
			return res, fmt.Errorf("%s", res.Error)
		}
		return res, nil
	case <-time.After(30 * time.Second):
		return daemon.ClusterSandboxResultBody{}, fmt.Errorf("sandbox rpc timed out")
	}
}

// ListSandbox lists directories under the node's sandbox.
func (h *Hub) ListSandbox(ctx context.Context, nodeID, path string) (daemon.ClusterSandboxResultBody, error) {
	return h.sandboxRPC(ctx, nodeID, daemon.KindClusterSandboxList, daemon.ClusterSandboxListBody{Path: path})
}

// MkdirSandbox creates a directory under the node's sandbox.
func (h *Hub) MkdirSandbox(ctx context.Context, nodeID, path string) (daemon.ClusterSandboxResultBody, error) {
	return h.sandboxRPC(ctx, nodeID, daemon.KindClusterSandboxMkdir, daemon.ClusterSandboxMkdirBody{Path: path})
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
	if hb.Hostname != "" {
		n.hostname = hb.Hostname
	}
	if hb.Name != "" {
		n.name = hb.Name
	}
	if hb.Description != "" {
		n.description = hb.Description
	}
	if hb.SandboxDir != "" {
		n.sandboxDir = hb.SandboxDir
	}
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

func (h *Hub) deliverEvent(ev daemon.JobEventBody) {
	h.waitMu.Lock()
	hook := h.eventHooks[ev.JobID]
	h.waitMu.Unlock()
	if hook != nil {
		hook(ev)
	}
}

func (h *Hub) deliverDone(done daemon.JobDoneBody) {
	h.waitMu.Lock()
	ch := h.waiters[done.JobID]
	nodeID := h.jobNode[done.JobID]
	delete(h.jobNode, done.JobID)
	delete(h.eventHooks, done.JobID)
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

func (h *Hub) deliverApprovalResult(msg daemon.Envelope) {
	var res daemon.JobApprovalResultBody
	_ = daemon.DecodeBody(msg, &res)
	h.waitMu.Lock()
	ch := h.approvalWait[msg.ID]
	delete(h.approvalWait, msg.ID)
	h.waitMu.Unlock()
	if ch != nil {
		select {
		case ch <- res:
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

// CancelJob asks the node that owns jobID to cancel it.
func (h *Hub) CancelJob(jobID string) error {
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return fmt.Errorf("job_id required")
	}
	h.waitMu.Lock()
	nodeID := h.jobNode[jobID]
	h.waitMu.Unlock()
	if nodeID == "" {
		return fmt.Errorf("job %q not found on scheduler", jobID)
	}
	h.nodesMu.Lock()
	n := h.nodes[nodeID]
	h.nodesMu.Unlock()
	if n == nil {
		return fmt.Errorf("node %q for job %q is offline", nodeID, jobID)
	}
	body, _ := daemon.MarshalBody(daemon.JobCancelBody{JobID: jobID})
	if err := h.writeNode(n, daemon.Envelope{Kind: daemon.KindJobCancel, ID: jobID, Body: body}); err != nil {
		return fmt.Errorf("cancel on %s: %w", nodeID, err)
	}
	return nil
}

// AnswerJobApproval routes a response to the node currently owning jobID and
// waits for the daemon to confirm that the request was still pending.
func (h *Hub) AnswerJobApproval(ctx context.Context, resp daemon.JobApprovalResponseBody) (daemon.JobApprovalResultBody, error) {
	if strings.TrimSpace(resp.JobID) == "" || strings.TrimSpace(resp.RequestID) == "" {
		return daemon.JobApprovalResultBody{}, fmt.Errorf("job_id and request_id required")
	}
	h.waitMu.Lock()
	nodeID := h.jobNode[resp.JobID]
	h.waitMu.Unlock()
	if nodeID == "" {
		return daemon.JobApprovalResultBody{}, fmt.Errorf("job %q is not running", resp.JobID)
	}
	h.nodesMu.Lock()
	n := h.nodes[nodeID]
	h.nodesMu.Unlock()
	if n == nil {
		return daemon.JobApprovalResultBody{}, fmt.Errorf("node %q for job %q is offline", nodeID, resp.JobID)
	}
	reqID := id.New()
	ch := make(chan daemon.JobApprovalResultBody, 1)
	h.waitMu.Lock()
	h.approvalWait[reqID] = ch
	h.waitMu.Unlock()
	defer func() {
		h.waitMu.Lock()
		delete(h.approvalWait, reqID)
		h.waitMu.Unlock()
	}()
	body, _ := daemon.MarshalBody(resp)
	if err := h.writeNode(n, daemon.Envelope{Kind: daemon.KindJobApprovalResponse, ID: reqID, Body: body}); err != nil {
		return daemon.JobApprovalResultBody{}, err
	}
	select {
	case <-ctx.Done():
		return daemon.JobApprovalResultBody{}, ctx.Err()
	case res := <-ch:
		return res, nil
	case <-time.After(10 * time.Second):
		return daemon.JobApprovalResultBody{}, fmt.Errorf("approval response timed out")
	}
}

// AssignJobTo runs a job on preferNode (empty = round-robin idle).
// onEvent, if non-nil, receives live JobEventBody frames (assistant deltas, tools, …).
func (h *Hub) AssignJobTo(ctx context.Context, job daemon.JobAssignBody, preferNode string, onEvent ...func(daemon.JobEventBody)) (daemon.JobDoneBody, error) {
	if job.JobID == "" {
		return daemon.JobDoneBody{}, fmt.Errorf("job_id is required")
	}
	n, err := h.pickNodeID(preferNode)
	if err != nil {
		return daemon.JobDoneBody{}, err
	}
	ch := make(chan daemon.JobDoneBody, 1)
	var hook func(daemon.JobEventBody)
	if len(onEvent) > 0 {
		hook = onEvent[0]
	}
	h.waitMu.Lock()
	h.waiters[job.JobID] = ch
	h.jobNode[job.JobID] = n.id
	if hook != nil {
		h.eventHooks[job.JobID] = hook
	}
	h.waitMu.Unlock()
	defer func() {
		h.waitMu.Lock()
		delete(h.waiters, job.JobID)
		delete(h.jobNode, job.JobID)
		delete(h.eventHooks, job.JobID)
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
