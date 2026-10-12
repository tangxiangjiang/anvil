// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func (s *Server) sandboxRoot() (string, error) {
	root := strings.TrimSpace(s.cfg.SandboxDir)
	if root == "" {
		return "", fmt.Errorf("sandbox_dir is not configured on this agentd")
	}
	return root, nil
}

// resolveInSandbox maps a relative or absolute path into the sandbox.
// Returns absolute path and slash-normalized relative path.
func (s *Server) resolveInSandbox(projectPath string) (absPath, rel string, err error) {
	root, err := s.sandboxRoot()
	if err != nil {
		return "", "", err
	}
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		return "", "", fmt.Errorf("project_path is required")
	}
	var candidate string
	if filepath.IsAbs(projectPath) {
		candidate, err = filepath.Abs(projectPath)
		if err != nil {
			return "", "", err
		}
	} else {
		candidate = filepath.Join(root, filepath.FromSlash(projectPath))
		candidate, err = filepath.Abs(candidate)
		if err != nil {
			return "", "", err
		}
	}
	rel, err = filepath.Rel(root, candidate)
	if err != nil {
		return "", "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("project_path must be inside sandbox_dir %s", root)
	}
	return candidate, filepath.ToSlash(rel), nil
}

// ListSandbox lists directory entries under a sandbox-relative path.
func (s *Server) ListSandbox(relPath string) (ClusterSandboxResultBody, error) {
	root, err := s.sandboxRoot()
	if err != nil {
		return ClusterSandboxResultBody{}, err
	}
	relPath = strings.Trim(strings.ReplaceAll(relPath, "\\", "/"), "/")
	abs := root
	if relPath != "" {
		abs, _, err = s.resolveInSandbox(relPath)
		if err != nil {
			return ClusterSandboxResultBody{}, err
		}
	}
	st, err := os.Stat(abs)
	if err != nil {
		return ClusterSandboxResultBody{}, err
	}
	if !st.IsDir() {
		return ClusterSandboxResultBody{}, fmt.Errorf("not a directory: %s", relPath)
	}
	ents, err := os.ReadDir(abs)
	if err != nil {
		return ClusterSandboxResultBody{}, err
	}
	out := ClusterSandboxResultBody{
		OK:         true,
		Request:    "list",
		SandboxDir: root,
		Path:       relPath,
		Entries:    make([]SandboxEntry, 0, len(ents)),
	}
	for _, e := range ents {
		name := e.Name()
		childRel := name
		if relPath != "" {
			childRel = relPath + "/" + name
		}
		se := SandboxEntry{Name: name, Path: childRel, IsDir: e.IsDir()}
		if info, err := e.Info(); err == nil {
			se.Mtime = info.ModTime().Unix()
			if !e.IsDir() {
				se.Size = info.Size()
			}
		}
		if e.IsDir() {
			se.IsGit = isGitRepo(filepath.Join(abs, name))
		}
		out.Entries = append(out.Entries, se)
	}
	return out, nil
}

// MkdirSandbox creates a directory (and parents) under the sandbox.
func (s *Server) MkdirSandbox(relPath string) (ClusterSandboxResultBody, error) {
	root, err := s.sandboxRoot()
	if err != nil {
		return ClusterSandboxResultBody{}, err
	}
	relPath = strings.Trim(strings.ReplaceAll(relPath, "\\", "/"), "/")
	if relPath == "" {
		return ClusterSandboxResultBody{}, fmt.Errorf("path is required")
	}
	abs, rel, err := s.resolveInSandbox(relPath)
	if err != nil {
		return ClusterSandboxResultBody{}, err
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return ClusterSandboxResultBody{}, err
	}
	return ClusterSandboxResultBody{
		OK:         true,
		Request:    "mkdir",
		SandboxDir: root,
		Path:       rel,
	}, nil
}

func (s *Server) decorateWorker(ws WorkerState) WorkerState {
	if ws.ProjectPath == "" {
		return ws
	}
	if s.cfg.SandboxDir != "" {
		if rel, err := filepath.Rel(s.cfg.SandboxDir, ws.ProjectPath); err == nil &&
			rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			ws.ProjectRel = filepath.ToSlash(rel)
		}
	}
	if br := gitCurrentBranch(ws.ProjectPath); br != "" {
		ws.GitBranch = br
	}
	return ws
}
