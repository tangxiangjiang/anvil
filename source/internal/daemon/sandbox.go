// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package daemon

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	sandboxMaxEntries = 2000
	sandboxMaxFile    = 1 << 20
	sandboxMaxGit     = 2 << 20
	sandboxGitTimeout = 10 * time.Second
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

func reviewError(request, code string, err error) ClusterSandboxResultBody {
	return ClusterSandboxResultBody{Request: request, Code: code, Error: err.Error()}
}

func relativeReviewPath(raw string, allowRoot bool) (string, error) {
	raw = strings.TrimSpace(raw)
	if strings.ContainsRune(raw, 0) {
		return "", fmt.Errorf("path contains NUL")
	}
	// The wire format always uses slash separators. Treat backslashes as
	// separators on every host so a Windows path cannot acquire a different
	// meaning when scheduler and agentd run on different operating systems.
	raw = strings.ReplaceAll(raw, "\\", "/")
	if strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") ||
		filepath.IsAbs(raw) || filepath.VolumeName(raw) != "" ||
		(len(raw) >= 2 && ((raw[0] >= 'A' && raw[0] <= 'Z') || (raw[0] >= 'a' && raw[0] <= 'z')) && raw[1] == ':') {
		return "", fmt.Errorf("absolute, drive, and UNC paths are not allowed")
	}
	clean := path.Clean(raw)
	if clean == "." {
		if allowRoot {
			return "", nil
		}
		return "", fmt.Errorf("path is required")
	}
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("path escapes the assigned repository")
	}
	return clean, nil
}

func pathWithin(root, candidate string) bool {
	rel, err := filepath.Rel(root, candidate)
	return err == nil && rel != ".." && !filepath.IsAbs(rel) &&
		!strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func canonicalExisting(name string) (string, error) {
	abs, err := filepath.Abs(name)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

func (s *Server) reviewRoot(workerID string) (string, error) {
	workerID = strings.TrimSpace(workerID)
	if workerID == "" {
		return "", fmt.Errorf("worker_id is required")
	}
	var project string
	for _, worker := range s.WorkerSnapshot().Workers {
		if worker.ID == workerID {
			project = worker.ProjectPath
			break
		}
	}
	if project == "" {
		return "", fmt.Errorf("worker %q is not assigned to a project", workerID)
	}
	sandbox, err := canonicalExisting(s.cfg.SandboxDir)
	if err != nil {
		return "", fmt.Errorf("canonicalize sandbox: %w", err)
	}
	root, err := canonicalExisting(project)
	if err != nil {
		return "", fmt.Errorf("canonicalize worker project: %w", err)
	}
	if !pathWithin(sandbox, root) {
		return "", fmt.Errorf("worker project is outside sandbox")
	}
	return root, nil
}

func (s *Server) reviewPath(workerID, raw string, allowRoot bool) (root, abs, rel string, err error) {
	root, err = s.reviewRoot(workerID)
	if err != nil {
		return "", "", "", err
	}
	rel, err = relativeReviewPath(raw, allowRoot)
	if err != nil {
		return "", "", "", err
	}
	abs = root
	if rel != "" {
		abs = filepath.Join(root, filepath.FromSlash(rel))
	}
	canonical, err := canonicalExisting(abs)
	if err != nil {
		return "", "", "", err
	}
	if !pathWithin(root, canonical) {
		return "", "", "", fmt.Errorf("path resolves outside the assigned repository")
	}
	return root, canonical, rel, nil
}

func (s *Server) reviewGitPath(workerID, raw string) (root, rel string, err error) {
	root, err = s.reviewRoot(workerID)
	if err != nil {
		return "", "", err
	}
	rel, err = relativeReviewPath(raw, false)
	if err != nil {
		return "", "", err
	}
	if credentialPath(rel) {
		return "", "", fmt.Errorf("credential-bearing paths are not reviewable")
	}
	// Deleted tracked files do not exist. Canonicalize the nearest existing
	// parent so a missing leaf remains usable without allowing a symlink parent
	// to redirect git or a later untracked-file read outside the repository.
	probe := filepath.Join(root, filepath.FromSlash(rel))
	for {
		canonical, evalErr := canonicalExisting(probe)
		if evalErr == nil {
			if !pathWithin(root, canonical) {
				return "", "", fmt.Errorf("path resolves outside the assigned repository")
			}
			break
		}
		parent := filepath.Dir(probe)
		if parent == probe || !pathWithin(root, parent) {
			return "", "", evalErr
		}
		probe = parent
	}
	return root, rel, nil
}

func credentialPath(rel string) bool {
	parts := strings.Split(strings.ToLower(rel), "/")
	for _, part := range parts {
		if part == ".git" || part == ".ssh" || part == ".aws" || part == ".gnupg" ||
			part == "credentials" || part == ".netrc" ||
			part == ".env" || strings.HasPrefix(part, ".env.") {
			return true
		}
	}
	return false
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

// ListWorkerSandbox lists a bounded directory relative to one assigned worker.
func (s *Server) ListWorkerSandbox(workerID, relPath string) ClusterSandboxResultBody {
	root, abs, rel, err := s.reviewPath(workerID, relPath, true)
	if err != nil {
		return reviewError("list", "invalid_path", err)
	}
	dir, err := os.Open(abs)
	if err != nil {
		return reviewError("list", "not_found", err)
	}
	defer dir.Close()
	st, err := dir.Stat()
	if err != nil {
		return reviewError("list", "read_failed", err)
	}
	if !st.IsDir() {
		return reviewError("list", "not_directory", fmt.Errorf("not a directory: %s", rel))
	}
	entries, readErr := dir.ReadDir(sandboxMaxEntries + 1)
	if readErr != nil && readErr != io.EOF {
		return reviewError("list", "permission_denied", readErr)
	}
	again, err := canonicalExisting(abs)
	if err != nil {
		return reviewError("list", "path_changed", err)
	}
	if !pathWithin(root, again) {
		return reviewError("list", "path_changed", fmt.Errorf("directory moved outside repository while opening"))
	}
	after, err := os.Stat(again)
	if err != nil || !os.SameFile(st, after) {
		return reviewError("list", "path_changed", fmt.Errorf("path changed while opening"))
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir() != entries[j].IsDir() {
			return entries[i].IsDir()
		}
		return strings.ToLower(entries[i].Name()) < strings.ToLower(entries[j].Name())
	})
	out := ClusterSandboxResultBody{OK: true, Request: "list", Path: rel}
	if len(entries) > sandboxMaxEntries {
		entries = entries[:sandboxMaxEntries]
		out.Truncated = true
	}
	out.Entries = make([]SandboxEntry, 0, len(entries))
	for _, entry := range entries {
		child := path.Join(rel, entry.Name())
		if credentialPath(child) {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			continue
		}
		item := SandboxEntry{Name: entry.Name(), Path: child, IsDir: info.IsDir(), Mtime: info.ModTime().Unix()}
		if !info.IsDir() {
			item.Size = info.Size()
		}
		out.Entries = append(out.Entries, item)
	}
	return out
}

// ReadWorkerSandbox reads bounded UTF-8 text without newline conversion.
func (s *Server) ReadWorkerSandbox(workerID, relPath string) ClusterSandboxResultBody {
	root, abs, rel, err := s.reviewPath(workerID, relPath, false)
	if err != nil {
		return reviewError("read", "invalid_path", err)
	}
	if credentialPath(rel) {
		return reviewError("read", "credential_path", fmt.Errorf("credential-bearing paths are not readable"))
	}
	f, err := os.Open(abs)
	if err != nil {
		return reviewError("read", "not_found", err)
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil {
		return reviewError("read", "read_failed", err)
	}
	if !before.Mode().IsRegular() {
		return reviewError("read", "not_file", fmt.Errorf("not a regular file: %s", rel))
	}
	// Re-resolve and compare the opened object to catch a link/rename swap
	// between canonicalization and open.
	again, err := canonicalExisting(abs)
	if err != nil {
		return reviewError("read", "path_changed", err)
	}
	if !pathWithin(root, again) {
		return reviewError("read", "path_changed", fmt.Errorf("file moved outside repository while opening"))
	}
	after, err := os.Stat(again)
	if err != nil || !os.SameFile(before, after) {
		return reviewError("read", "path_changed", fmt.Errorf("path changed while opening"))
	}
	data, err := io.ReadAll(io.LimitReader(f, sandboxMaxFile+1))
	if err != nil {
		return reviewError("read", "read_failed", err)
	}
	out := ClusterSandboxResultBody{OK: true, Request: "read", Path: rel, Size: before.Size(), Encoding: "utf-8"}
	if len(data) > sandboxMaxFile {
		data = data[:sandboxMaxFile]
		out.Truncated = true
	}
	if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
		out.Binary = true
		out.Encoding = "binary"
		return out
	}
	out.Content = string(data)
	return out
}

type boundedBuffer struct {
	buf       bytes.Buffer
	remaining int
	truncated bool
}

func newBoundedBuffer(limit int) *boundedBuffer { return &boundedBuffer{remaining: limit} }

func (b *boundedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	available := b.remaining
	if b.remaining > 0 {
		part := p
		if len(part) > b.remaining {
			part = part[:b.remaining]
		}
		_, _ = b.buf.Write(part)
		b.remaining -= len(part)
	}
	if n > available {
		b.truncated = true
	}
	return n, nil
}

func runReviewGit(root string, args ...string) ([]byte, bool, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), sandboxGitTimeout)
	defer cancel()
	fixed := []string{"-c", "color.ui=false", "-c", "core.quotepath=false", "-c", "diff.external=", "-C", root}
	cmd := exec.CommandContext(ctx, "git", append(fixed, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1")
	stdout, stderr := newBoundedBuffer(sandboxMaxGit), newBoundedBuffer(16<<10)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		return nil, false, "", fmt.Errorf("git operation timed out")
	}
	return stdout.buf.Bytes(), stdout.truncated, strings.TrimSpace(stderr.buf.String()), err
}

// GitStatusWorker returns bounded porcelain status for an assigned worker.
func (s *Server) GitStatusWorker(workerID string) ClusterSandboxResultBody {
	root, err := s.reviewRoot(workerID)
	if err != nil {
		return reviewError("git_status", "invalid_scope", err)
	}
	data, truncated, stderr, err := runReviewGit(root, "status", "--porcelain=v1", "-z", "--branch", "--untracked-files=all")
	if err != nil {
		if stderr == "" {
			stderr = err.Error()
		}
		return reviewError("git_status", "not_repository", fmt.Errorf("%s", stderr))
	}
	out := ClusterSandboxResultBody{OK: true, Request: "git_status", Truncated: truncated}
	records := bytes.Split(data, []byte{0})
	if truncated && len(records) > 0 && len(data) > 0 && data[len(data)-1] != 0 {
		records = records[:len(records)-1]
	}
	for i := 0; i < len(records); i++ {
		rec := string(records[i])
		if rec == "" {
			continue
		}
		if strings.HasPrefix(rec, "## ") {
			out.Branch = strings.TrimPrefix(rec, "## ")
			continue
		}
		if len(rec) < 4 {
			continue
		}
		item := GitStatusEntry{Index: rec[0:1], Worktree: rec[1:2], Path: rec[3:]}
		if (item.Index == "R" || item.Index == "C") && i+1 < len(records) {
			i++
			item.OrigPath = string(records[i])
		}
		out.Status = append(out.Status, item)
		if len(out.Status) == sandboxMaxEntries {
			out.Truncated = i+1 < len(records)
			break
		}
	}
	return out
}

// GitDiffWorker runs only a fixed read-only git diff, optionally for one safe path.
func (s *Server) GitDiffWorker(workerID, relPath string, staged bool) ClusterSandboxResultBody {
	root, err := s.reviewRoot(workerID)
	if err != nil {
		return reviewError("git_diff", "invalid_scope", err)
	}
	args := []string{"diff", "--no-ext-diff", "--no-textconv", "--src-prefix=a/", "--dst-prefix=b/"}
	if staged {
		args = append(args, "--cached")
	}
	if strings.TrimSpace(relPath) != "" {
		var pathErr error
		root, relPath, pathErr = s.reviewGitPath(workerID, relPath)
		if pathErr != nil {
			return reviewError("git_diff", "invalid_path", pathErr)
		}
		args = append(args, "--", relPath)
	}
	data, truncated, stderr, err := runReviewGit(root, args...)
	if err != nil {
		if stderr == "" {
			stderr = err.Error()
		}
		return reviewError("git_diff", "git_failed", fmt.Errorf("%s", stderr))
	}
	out := ClusterSandboxResultBody{
		OK: true, Request: "git_diff", Path: relPath, Diff: string(data), Encoding: "utf-8", Truncated: truncated,
	}
	if !staged && relPath != "" && len(data) == 0 {
		status, _, _, statusErr := runReviewGit(root, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--", relPath)
		if statusErr == nil && bytes.HasPrefix(status, []byte("?? ")) {
			read := s.ReadWorkerSandbox(workerID, relPath)
			if read.OK && read.Binary {
				out.Diff = fmt.Sprintf("diff --git a/%s b/%s\nnew file\nBinary file b/%s\n", relPath, relPath, relPath)
			} else if read.OK {
				var diff strings.Builder
				fmt.Fprintf(&diff, "diff --git a/%s b/%s\nnew file\n--- /dev/null\n+++ b/%s\n", relPath, relPath, relPath)
				lines := strings.Split(read.Content, "\n")
				fmt.Fprintf(&diff, "@@ -0,0 +1,%d @@\n", len(lines))
				for _, line := range lines {
					diff.WriteByte('+')
					diff.WriteString(line)
					diff.WriteByte('\n')
					if diff.Len() >= sandboxMaxGit {
						out.Truncated = true
						break
					}
				}
				out.Diff = diff.String()
				if len(out.Diff) > sandboxMaxGit {
					out.Diff = out.Diff[:sandboxMaxGit]
				}
				out.Truncated = out.Truncated || read.Truncated
			}
		}
	}
	return out
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
