// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var workerIDSanitizer = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func sanitizeWorkerID(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	s = workerIDSanitizer.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-.")
	if s == "" {
		return "worker"
	}
	if len(s) > 64 {
		s = s[:64]
	}
	return s
}

func isGitRepo(dir string) bool {
	st, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil && st.IsDir()
}

func dirNonEmpty(dir string) bool {
	ents, err := os.ReadDir(dir)
	return err == nil && len(ents) > 0
}

func gitRemoteOrigin(dir string) (string, error) {
	cmd := exec.Command("git", "-C", dir, "remote", "get-url", "origin")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git remote: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// gitCurrentBranch returns the checked-out branch, or a short SHA when detached.
func gitCurrentBranch(dir string) string {
	if dir == "" || !isGitRepo(dir) {
		return ""
	}
	cmd := exec.Command("git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return ""
	}
	br := strings.TrimSpace(string(out))
	if br == "" {
		return ""
	}
	if br != "HEAD" {
		return br
	}
	cmd = exec.Command("git", "-C", dir, "rev-parse", "--short", "HEAD")
	out, err = cmd.CombinedOutput()
	if err != nil {
		return "HEAD"
	}
	sha := strings.TrimSpace(string(out))
	if sha == "" {
		return "HEAD"
	}
	return sha
}

func gitClone(url, dir string) error {
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}
	cmd := exec.Command("git", "clone", "--", url, dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git clone: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// prepareProjectPath validates/clones a project directory for a new worker.
// absPath must already be resolved inside the sandbox.
func prepareProjectPath(absPath, gitURL string) (remote string, err error) {
	gitURL = strings.TrimSpace(gitURL)
	st, err := os.Stat(absPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return "", err
		}
		if gitURL == "" {
			return "", fmt.Errorf("project_path does not exist; git_url is required to clone")
		}
		if err := gitClone(gitURL, absPath); err != nil {
			return "", err
		}
		return gitURL, nil
	}
	if !st.IsDir() {
		return "", fmt.Errorf("project_path is not a directory")
	}
	if isGitRepo(absPath) {
		remote, err = gitRemoteOrigin(absPath)
		if err != nil {
			remote = gitURL
		}
		if remote == "" && gitURL != "" {
			remote = gitURL
		}
		return remote, nil
	}
	if dirNonEmpty(absPath) {
		return "", fmt.Errorf("project_path has content but is not a git repository")
	}
	if gitURL == "" {
		return "", fmt.Errorf("empty project_path requires git_url")
	}
	cmd := exec.Command("git", "clone", "--", gitURL, absPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git clone: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return gitURL, nil
}

// AddProjectWorker prepares the path under sandbox, registers a repo, and opens a capacity slot.
func (s *Server) AddProjectWorker(workerID, name, description, projectPath, gitURL string) (WorkerState, error) {
	abs, rel, err := s.resolveInSandbox(projectPath)
	if err != nil {
		return WorkerState{}, err
	}
	remote, err := prepareProjectPath(abs, gitURL)
	if err != nil {
		return WorkerState{}, err
	}
	name = strings.TrimSpace(name)
	description = strings.TrimSpace(description)
	id := sanitizeWorkerID(workerID)
	if id == "" {
		id = sanitizeWorkerID(name)
	}
	if id == "" {
		id = sanitizeWorkerID(filepath.Base(abs))
	}
	if id == "" {
		id = "worker"
	}
	if name == "" {
		name = id
	}
	// ensure unique id
	base := id
	for i := 2; ; i++ {
		if _, ok := s.slots.Get(id); !ok && s.repoIndex(id) < 0 {
			break
		}
		id = fmt.Sprintf("%s-%d", base, i)
	}

	s.mu.Lock()
	s.addRepoLocked(id, abs)
	// Also allow jobs addressed by sandbox-relative folder (common UI mistake).
	if rel != "" && rel != id {
		s.addRepoLocked(rel, abs)
	}
	if base := filepath.Base(abs); base != "" && base != id && base != rel {
		s.addRepoLocked(base, abs)
	}
	s.cfg.MaxWorkers = len(s.cfg.Repos)
	s.mu.Unlock()

	// Add as stopped config; operator starts via StartWorker.
	ws, err := s.slots.AddBoundWorker(id, name, description, abs, remote, id, false)
	if err != nil {
		s.mu.Lock()
		s.removeReposForCWDLocked(abs)
		s.mu.Unlock()
		return WorkerState{}, err
	}
	s.mu.Lock()
	s.cfg.MaxWorkers = s.slots.Max()
	s.mu.Unlock()
	ws.ProjectRel = rel
	out := s.decorateWorker(ws)
	s.saveWorkersState()
	return out, nil
}

// StartProjectWorker enables a stopped worker to accept jobs.
func (s *Server) StartProjectWorker(workerID string) (WorkerState, error) {
	ws, err := s.slots.StartWorker(workerID)
	if err != nil {
		return WorkerState{}, err
	}
	out := s.decorateWorker(ws)
	s.saveWorkersState()
	return out, nil
}

// ShutdownProjectWorker stops an idle worker (keeps project config).
func (s *Server) ShutdownProjectWorker(workerID string) (WorkerState, error) {
	ws, err := s.slots.ShutdownWorker(workerID)
	if err != nil {
		return WorkerState{}, err
	}
	out := s.decorateWorker(ws)
	s.saveWorkersState()
	return out, nil
}

// RemoveProjectWorker removes an idle project worker and its repo allowlist entry.
func (s *Server) RemoveProjectWorker(workerID string) error {
	ws, ok := s.slots.Get(workerID)
	if !ok {
		return fmt.Errorf("unknown worker %q", workerID)
	}
	if err := s.slots.RemoveBoundWorker(workerID); err != nil {
		return err
	}
	s.mu.Lock()
	if ws.ProjectPath != "" {
		s.removeReposForCWDLocked(ws.ProjectPath)
	} else {
		repoID := ws.RepoID
		if repoID == "" {
			repoID = workerID
		}
		if i := s.repoIndexLocked(repoID); i >= 0 {
			s.cfg.Repos = append(s.cfg.Repos[:i], s.cfg.Repos[i+1:]...)
		}
	}
	s.cfg.MaxWorkers = s.slots.Max()
	s.mu.Unlock()
	s.saveWorkersState()
	return nil
}

func (s *Server) repoIndex(id string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.repoIndexLocked(id)
}

func (s *Server) repoIndexLocked(id string) int {
	for i, r := range s.cfg.Repos {
		if r.ID == id {
			return i
		}
	}
	return -1
}

func (s *Server) addRepoLocked(id, abs string) {
	id = strings.TrimSpace(id)
	if id == "" || abs == "" {
		return
	}
	if i := s.repoIndexLocked(id); i >= 0 {
		s.cfg.Repos[i].CWD = abs
		return
	}
	s.cfg.Repos = append(s.cfg.Repos, RepoEntry{ID: id, CWD: abs})
}

func (s *Server) removeReposForCWDLocked(abs string) {
	if abs == "" {
		return
	}
	out := s.cfg.Repos[:0]
	for _, r := range s.cfg.Repos {
		if r.CWD != abs {
			out = append(out, r)
		}
	}
	s.cfg.Repos = out
}
