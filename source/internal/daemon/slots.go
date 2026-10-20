// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package daemon

import (
	"fmt"
	"sync"
)

const (
	SlotIdle      = "idle"
	SlotBusy      = "busy"
	SlotStarting  = "starting"
	SlotUnhealthy = "unhealthy"
	SlotStopped   = "stopped" // configured but not accepting jobs
)

// Slots tracks worker capacity.
type Slots struct {
	mu      sync.Mutex
	max     int
	nextID  int
	byID    map[string]*slot
	jobToID map[string]string
}

type slot struct {
	id          string
	name        string
	description string
	status      string
	jobID       string
	cancel      func()
	projectPath string
	gitRemote   string
	repoID      string
	// llm overrides agentd's llm block for jobs on this slot (0.47). Nil means
	// inherit, which is what every worker did before the field existed.
	llm *WorkerLLMBody
}

// NewSlots creates a registry with max concurrent busy slots.
func NewSlots(max int) *Slots {
	s := &Slots{
		max:     max,
		nextID:  max,
		byID:    make(map[string]*slot),
		jobToID: make(map[string]string),
	}
	for i := 0; i < max; i++ {
		id := fmt.Sprintf("worker-%d", i+1)
		s.byID[id] = &slot{id: id, status: SlotIdle}
	}
	return s
}

// SetWorkerLLM replaces one worker's provider override (nil clears it).
//
// A worker whose provider changed should pick it up on its next job rather than
// after a daemon restart: the whole reason a per-worker model exists is to switch
// cost per phase, and a restart to change a model defeats that.
func (s *Slots) SetWorkerLLM(workerID string, llm *WorkerLLMBody) (WorkerState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sl := s.byID[workerID]
	if sl == nil {
		return WorkerState{}, fmt.Errorf("unknown worker %q", workerID)
	}
	sl.llm = llm
	return s.snapshotLocked(sl), nil
}

// LLMFor returns a worker's override, or nil when it inherits.
func (s *Slots) LLMFor(workerID string) *WorkerLLMBody {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sl := s.byID[workerID]; sl != nil {
		return sl.llm
	}
	return nil
}

// Max returns configured capacity.
func (s *Slots) Max() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.max
}

// Resize shrinks capacity by removing idle slots. Growing is not supported
// (use AddBoundWorker / project worker add — no anonymous slots).
func (s *Slots) Resize(max int) error {
	if max < 0 {
		return fmt.Errorf("max_workers must be >= 0")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if max == s.max {
		return nil
	}
	if max > s.max {
		return fmt.Errorf("cannot grow capacity anonymously; add a project worker instead")
	}
	// Shrink: remove idle slots until at target or no idle left.
	for len(s.byID) > max {
		removed := false
		for id, sl := range s.byID {
			if sl.status == SlotIdle {
				delete(s.byID, id)
				removed = true
				break
			}
		}
		if !removed {
			break
		}
	}
	s.max = len(s.byID)
	return nil
}

// Snapshot returns list result.
func (s *Slots) Snapshot() WorkerListResultBody {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := WorkerListResultBody{MaxWorkers: s.max}
	for _, sl := range s.byID {
		st := WorkerState{
			ID:          sl.id,
			Name:        sl.name,
			Description: sl.description,
			Status:      sl.status,
			JobID:       sl.jobID,
			ProjectPath: sl.projectPath,
			GitRemote:   sl.gitRemote,
			RepoID:      sl.repoID,
		}
		out.Workers = append(out.Workers, st)
		switch sl.status {
		case SlotIdle:
			out.Idle++
		case SlotBusy, SlotStarting:
			out.Busy++
		}
	}
	return out
}

// TryAcquire reserves an idle slot for jobID. Returns false if at capacity.
func (s *Slots) TryAcquire(jobID string, cancel func()) (workerID string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.jobToID[jobID]; exists {
		return "", fmt.Errorf("job %q already running", jobID)
	}
	for _, sl := range s.byID {
		if sl.status == SlotIdle {
			sl.status = SlotBusy
			sl.jobID = jobID
			sl.cancel = cancel
			s.jobToID[jobID] = sl.id
			return sl.id, nil
		}
	}
	return "", fmt.Errorf("at capacity (%d workers busy)", s.max)
}

// Release frees the slot for jobID.
func (s *Slots) Release(jobID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	wid, ok := s.jobToID[jobID]
	if !ok {
		return
	}
	delete(s.jobToID, jobID)
	if sl, ok := s.byID[wid]; ok {
		sl.status = SlotIdle
		sl.jobID = ""
		sl.cancel = nil
	}
}

// CancelJob invokes the cancel func for a job if present.
func (s *Slots) CancelJob(jobID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	wid, ok := s.jobToID[jobID]
	if !ok {
		return false
	}
	sl := s.byID[wid]
	if sl != nil && sl.cancel != nil {
		sl.cancel()
		return true
	}
	return false
}

// Counts returns idle and busy.
func (s *Slots) Counts() (idle, busy int) {
	snap := s.Snapshot()
	return snap.Idle, snap.Busy
}

// AddBoundWorker registers a project-bound worker. When start is false the worker
// is stopped (config only) until StartWorker.
func (s *Slots) AddBoundWorker(id, name, description, projectPath, gitRemote, repoID string, start bool) (WorkerState, error) {
	return s.AddBoundWorkerWithLLM(id, name, description, projectPath, gitRemote, repoID, start, nil)
}

// AddBoundWorkerWithLLM is AddBoundWorker plus the worker's own provider settings.
//
// The argument is a pointer so "no override" (nil) and "an empty override" stay
// distinguishable: nil inherits agentd's llm block, which is what every worker
// created before this field did.
func (s *Slots) AddBoundWorkerWithLLM(id, name, description, projectPath, gitRemote, repoID string, start bool, llm *WorkerLLMBody) (WorkerState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "" {
		return WorkerState{}, fmt.Errorf("worker id required")
	}
	if s.byID[id] != nil {
		return WorkerState{}, fmt.Errorf("worker %q already exists", id)
	}
	if name == "" {
		name = id
	}
	st := SlotStopped
	if start {
		st = SlotIdle
	}
	s.byID[id] = &slot{
		id:          id,
		name:        name,
		description: description,
		status:      st,
		projectPath: projectPath,
		gitRemote:   gitRemote,
		repoID:      repoID,
		llm:         llm,
	}
	s.max = len(s.byID)
	return WorkerState{
		ID: id, Name: name, Description: description, Status: st,
		ProjectPath: projectPath, GitRemote: gitRemote, RepoID: repoID,
	}, nil
}

// StartWorker moves a stopped worker to idle (accepting jobs).
func (s *Slots) StartWorker(id string) (WorkerState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sl, ok := s.byID[id]
	if !ok {
		return WorkerState{}, fmt.Errorf("unknown worker %q", id)
	}
	if sl.status == SlotIdle {
		return s.snapshotLocked(sl), nil
	}
	if sl.status != SlotStopped {
		return WorkerState{}, fmt.Errorf("worker %q is %s; only stopped workers can be started", id, sl.status)
	}
	sl.status = SlotIdle
	return s.snapshotLocked(sl), nil
}

// ShutdownWorker stops an idle worker (keeps config; refuses jobs).
func (s *Slots) ShutdownWorker(id string) (WorkerState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sl, ok := s.byID[id]
	if !ok {
		return WorkerState{}, fmt.Errorf("unknown worker %q", id)
	}
	if sl.status == SlotStopped {
		return s.snapshotLocked(sl), nil
	}
	if sl.status != SlotIdle {
		return WorkerState{}, fmt.Errorf("worker %q is %s; only idle workers can be shut down", id, sl.status)
	}
	sl.status = SlotStopped
	return s.snapshotLocked(sl), nil
}

func (s *Slots) snapshotLocked(sl *slot) WorkerState {
	return WorkerState{
		ID: sl.id, Name: sl.name, Description: sl.description,
		Status: sl.status, JobID: sl.jobID,
		ProjectPath: sl.projectPath, GitRemote: sl.gitRemote, RepoID: sl.repoID,
	}
}

// RemoveBoundWorker removes a stopped or idle worker slot.
func (s *Slots) RemoveBoundWorker(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sl, ok := s.byID[id]
	if !ok {
		return fmt.Errorf("unknown worker %q", id)
	}
	if sl.status != SlotIdle && sl.status != SlotStopped {
		return fmt.Errorf("worker %q is %s; shut down or wait until idle before remove", id, sl.status)
	}
	delete(s.byID, id)
	s.max = len(s.byID)
	return nil
}

// Get returns a worker snapshot.
func (s *Slots) Get(id string) (WorkerState, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sl, ok := s.byID[id]
	if !ok {
		return WorkerState{}, false
	}
	return s.snapshotLocked(sl), true
}
