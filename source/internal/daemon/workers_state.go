// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package daemon

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

func (s *Server) stateLog() *log.Logger {
	if s != nil && s.log != nil {
		return s.log
	}
	return log.Default()
}

const workersStateVersion = 1

// WorkersStateFile is the on-disk project-worker registry under sandbox/.anvil/.
type WorkersStateFile struct {
	Version int               `json:"version"`
	Workers []PersistedWorker `json:"workers"`
}

// PersistedWorker is the durable desired state of one project worker.
type PersistedWorker struct {
	ID          string `json:"id"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	ProjectRel  string `json:"project_rel"`
	GitURL      string `json:"git_url,omitempty"`
	Enabled     bool   `json:"enabled"` // true → start (idle) after restore
	// LLM is the worker's provider override (0.46). Absent means inherit agentd's.
	//
	// The key is not here on purpose: this file is plain JSON on disk under the
	// sandbox, and a provider key does not belong in it. APIKeyEnv names the
	// variable instead.
	LLM *WorkerLLMBody `json:"llm,omitempty"`
}

func (s *Server) workersStatePath() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.workersStatePathLocked()
}

func (s *Server) workersStatePathLocked() string {
	root := strings.TrimSpace(s.cfg.SandboxDir)
	if root == "" {
		return ""
	}
	return filepath.Join(root, ".anvil", "workers.json")
}

// restoreProjectWorkers loads sandbox/.anvil/workers.json into slots (best-effort).
func (s *Server) restoreProjectWorkers() {
	path := s.workersStatePath()
	if path == "" {
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			s.stateLog().Printf("workers state read %s: %v", path, err)
		}
		return
	}
	var file WorkersStateFile
	if err := json.Unmarshal(raw, &file); err != nil {
		s.stateLog().Printf("workers state parse %s: %v", path, err)
		return
	}
	s.mu.Lock()
	s.restoring = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.restoring = false
		s.mu.Unlock()
	}()

	nOK := 0
	for _, w := range file.Workers {
		id := strings.TrimSpace(w.ID)
		rel := strings.TrimSpace(w.ProjectRel)
		if id == "" || rel == "" {
			continue
		}
		if _, ok := s.slots.Get(id); ok {
			continue
		}
		ws, err := s.AddProjectWorker(id, w.Name, w.Description, rel, w.GitURL, false, "", "", "", w.LLM)
		if err != nil {
			s.stateLog().Printf("workers restore %q (%s): %v", id, rel, err)
			continue
		}
		gotID := ws.ID
		if w.Enabled {
			if _, err := s.StartProjectWorker(gotID); err != nil {
				s.stateLog().Printf("workers restore start %q: %v", gotID, err)
			}
		}
		nOK++
	}
	s.stateLog().Printf("workers restored from %s (%d/%d ok)", path, nOK, len(file.Workers))
}

// saveWorkersState writes the current project-worker registry atomically.
func (s *Server) saveWorkersState() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.restoring {
		s.mu.Unlock()
		return
	}
	path := s.workersStatePathLocked()
	sandbox := s.cfg.SandboxDir
	s.mu.Unlock()
	if path == "" {
		return
	}

	snap := s.slots.Snapshot()
	file := WorkersStateFile{Version: workersStateVersion}
	for _, w := range snap.Workers {
		rel := strings.TrimSpace(w.ProjectRel)
		if rel == "" && w.ProjectPath != "" && sandbox != "" {
			if r, err := filepath.Rel(sandbox, w.ProjectPath); err == nil &&
				r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator)) {
				rel = filepath.ToSlash(r)
			}
		}
		if rel == "" {
			rel = filepath.ToSlash(filepath.Base(w.ProjectPath))
		}
		if strings.TrimSpace(w.ID) == "" || rel == "" {
			continue
		}
		enabled := w.Status != SlotStopped
		file.Workers = append(file.Workers, PersistedWorker{
			ID:          w.ID,
			Name:        w.Name,
			Description: w.Description,
			ProjectRel:  rel,
			GitURL:      w.GitRemote,
			Enabled:     enabled,
		})
	}

	if err := writeWorkersStateFile(path, file); err != nil {
		s.stateLog().Printf("workers state save %s: %v", path, err)
	}
}

func writeWorkersStateFile(path string, file WorkersStateFile) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}
