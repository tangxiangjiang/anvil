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
	id     string
	status string
	jobID  string
	cancel func()
}

// NewSlots creates a registry with max concurrent busy slots.
func NewSlots(max int) *Slots {
	s := &Slots{
		max:     max,
		byID:    make(map[string]*slot),
		jobToID: make(map[string]string),
	}
	for i := 0; i < max; i++ {
		id := fmt.Sprintf("worker-%d", i+1)
		s.byID[id] = &slot{id: id, status: SlotIdle}
	}
	return s
}

// Snapshot returns list result.
func (s *Slots) Snapshot() WorkerListResultBody {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := WorkerListResultBody{MaxWorkers: s.max}
	for _, sl := range s.byID {
		st := WorkerState{ID: sl.id, Status: sl.status, JobID: sl.jobID}
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
