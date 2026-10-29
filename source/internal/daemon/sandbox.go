// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package daemon

import (
	"bytes"
	"context"
	"errors"
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
)

// sandboxGitTimeout bounds every git invocation. It is a variable so the tests
// can exercise the timeout path without waiting ten seconds.
var sandboxGitTimeout = 10 * time.Second

var errGitTimeout = errors.New("git operation timed out")

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
	resolved, err := resolveReparsePoints(abs)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(resolved)
}

// resolveReparsePoints rewrites every component that os.Readlink reports as a
// reparse point to its target. filepath.EvalSymlinks alone is not enough on
// Windows: when Go cannot open a junction (for example because another handle
// lacks sharing), os.Lstat reports the entry as ModeIrregular instead of
// ModeSymlink and EvalSymlinks leaves the path unresolved, which would let a
// junction escape the sandbox.
func resolveReparsePoints(name string) (string, error) {
	for depth := 0; depth < 40; depth++ {
		next, changed := resolveFirstReparse(name)
		if !changed {
			return next, nil
		}
		name = next
	}
	return "", fmt.Errorf("too many levels of links in %s", name)
}

func resolveFirstReparse(name string) (string, bool) {
	volume := filepath.VolumeName(name)
	rest := strings.TrimLeft(strings.TrimPrefix(name, volume), `\/`)
	parts := strings.Split(rest, string(filepath.Separator))
	current := volume + string(filepath.Separator)
	if volume == "" {
		current = string(filepath.Separator)
	}
	for i, part := range parts {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)
		target, err := os.Readlink(current)
		if err != nil {
			continue
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(current), target)
		}
		if i+1 < len(parts) {
			target = filepath.Join(target, filepath.Join(parts[i+1:]...))
		}
		return target, true
	}
	return name, false
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

// gitRepo is a repository whose metadata has been proven to live inside the
// worker's repository. Without that check a .git file or a linked worktree can
// point git at a host repository outside the sandbox and return its contents.
type gitRepo struct {
	root      string
	gitDir    string
	commonDir string
}

func (r *gitRepo) env() []string {
	// An inherited GIT_* variable could name a different repository, worktree,
	// index, object store, or config file, so none of them may reach the git
	// process that runs against the sandbox.
	env := make([]string, 0, len(os.Environ())+12)
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "GIT_") {
			continue
		}
		env = append(env, kv)
	}
	return append(env,
		"GIT_DIR="+r.gitDir,
		"GIT_WORK_TREE="+r.root,
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_SYSTEM="+os.DevNull,
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_ATTR_NOSYSTEM=1",
		"GIT_OPTIONAL_LOCKS=0",
		"GIT_PAGER=cat",
		"GIT_PROTOCOL_FROM_USER=0",
	)
}

func (r *gitRepo) run(args ...string) ([]byte, bool, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), sandboxGitTimeout)
	defer cancel()
	// The -c overrides outrank the repository's own config, so the keys that
	// make git run a program (fsmonitor, hooks, external diff, attribute file)
	// cannot be supplied by the repository under review.
	fixed := []string{
		"--no-pager",
		"-c", "color.ui=false",
		"-c", "core.quotepath=false",
		"-c", "core.fsmonitor=",
		"-c", "core.hooksPath=",
		"-c", "core.attributesFile=" + os.DevNull,
		"-c", "diff.external=",
		"-C", r.root,
	}
	cmd := exec.CommandContext(ctx, "git", append(fixed, args...)...)
	cmd.Env = r.env()
	stdout, stderr := newBoundedBuffer(sandboxMaxGit), newBoundedBuffer(16<<10)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		return nil, false, "", errGitTimeout
	}
	return stdout.buf.Bytes(), stdout.truncated, strings.TrimSpace(stderr.buf.String()), err
}

// executingConfig returns the first repository config key that makes git run a
// program named by the repository. Filter drivers are chosen through
// .gitattributes and their command comes from config, so unlike fsmonitor or
// diff.external the name is not known in advance and cannot be overridden on
// the command line; such a repository is refused instead of executed. Config
// includes and worktree config are read exactly as git reads them, while the
// global and system scopes are already disabled by env().
func (r *gitRepo) executingConfig() (string, error) {
	data, truncated, stderr, err := r.run("config", "--list", "--null")
	if err != nil {
		if errors.Is(err, errGitTimeout) || gitUnavailable(err) {
			return "", err
		}
		if stderr == "" {
			stderr = err.Error()
		}
		return "", fmt.Errorf("read repository config: %s", stderr)
	}
	if truncated {
		// An unscanned tail could hide an executing key, so refuse instead of
		// trusting a partial view of the config.
		return "", fmt.Errorf("repository config is too large to verify safely")
	}
	for _, record := range strings.Split(string(data), "\x00") {
		if record == "" {
			continue
		}
		key, value, _ := strings.Cut(record, "\n")
		lower := strings.ToLower(key)
		if value == "" {
			continue
		}
		if lower == "core.alternaterefscommand" {
			return key, nil
		}
		if !strings.HasPrefix(lower, "filter.") {
			continue
		}
		if strings.HasSuffix(lower, ".clean") || strings.HasSuffix(lower, ".smudge") || strings.HasSuffix(lower, ".process") {
			return key, nil
		}
	}
	return "", nil
}

// verifyLayout proves that the object store, refs, index, and config that git
// will read all live inside the repository. A junction or symlink on one of
// them would let a crafted repository read another repository's contents.
func (r *gitRepo) verifyLayout() error {
	dirs := []string{r.gitDir}
	if r.commonDir != "" {
		dirs = append(dirs, r.commonDir)
	}
	for _, dir := range dirs {
		for _, name := range []string{"objects", "refs", "HEAD", "index", "config", "packed-refs", "logs", "info"} {
			p := filepath.Join(dir, name)
			if _, err := os.Lstat(p); err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return err
			}
			canonical, err := canonicalExisting(p)
			if err != nil {
				return err
			}
			if !pathWithin(r.root, canonical) {
				return fmt.Errorf("repository metadata %s resolves outside the assigned repository", name)
			}
		}
	}
	objDir := r.gitDir
	if r.commonDir != "" {
		objDir = r.commonDir
	}
	data, err := os.ReadFile(filepath.Join(objDir, "objects", "info", "alternates"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !filepath.IsAbs(line) {
			line = filepath.Join(objDir, "objects", filepath.FromSlash(line))
		}
		canonical, err := canonicalExisting(line)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		if !pathWithin(r.root, canonical) {
			return fmt.Errorf("repository alternate object store resolves outside the assigned repository")
		}
	}
	return nil
}

func gitDirFromFile(root, content string) (string, error) {
	line, _, _ := strings.Cut(content, "\n")
	line = strings.TrimSpace(line)
	const prefix = "gitdir:"
	if len(line) < len(prefix) || !strings.EqualFold(line[:len(prefix)], prefix) {
		return "", fmt.Errorf("unrecognized .git file; expected a gitdir line")
	}
	target := strings.TrimSpace(line[len(prefix):])
	if target == "" {
		return "", fmt.Errorf(".git file names no git directory")
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(root, filepath.FromSlash(target))
	}
	return target, nil
}

// openGitRepo resolves and validates the repository before any git command
// runs, and refuses one whose config would execute a program.
func (s *Server) openGitRepo(request, workerID string) (*gitRepo, *ClusterSandboxResultBody) {
	fail := func(code string, err error) *ClusterSandboxResultBody {
		out := reviewError(request, code, err)
		return &out
	}
	root, err := s.reviewRoot(workerID)
	if err != nil {
		return nil, fail("invalid_scope", err)
	}
	dotgit := filepath.Join(root, ".git")
	info, err := os.Stat(dotgit)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fail("not_repository", fmt.Errorf("%s is not a git repository", root))
		}
		return nil, fail("invalid_repository", err)
	}
	gitDir := dotgit
	if !info.IsDir() {
		data, err := os.ReadFile(dotgit)
		if err != nil {
			return nil, fail("invalid_repository", err)
		}
		gitDir, err = gitDirFromFile(root, string(data))
		if err != nil {
			return nil, fail("invalid_repository", err)
		}
	}
	canonical, err := canonicalExisting(gitDir)
	if err != nil {
		return nil, fail("invalid_repository", err)
	}
	if !pathWithin(root, canonical) {
		return nil, fail("invalid_repository", fmt.Errorf("repository metadata resolves outside the assigned repository"))
	}
	repo := &gitRepo{root: root, gitDir: canonical}
	switch common, err := os.ReadFile(filepath.Join(canonical, "commondir")); {
	case err == nil:
		target := strings.TrimSpace(string(common))
		if target != "" {
			if !filepath.IsAbs(target) {
				target = filepath.Join(canonical, filepath.FromSlash(target))
			}
			commonDir, err := canonicalExisting(target)
			if err != nil {
				return nil, fail("invalid_repository", err)
			}
			if !pathWithin(root, commonDir) {
				return nil, fail("invalid_repository", fmt.Errorf("repository metadata resolves outside the assigned repository"))
			}
			repo.commonDir = commonDir
		}
	case !os.IsNotExist(err):
		return nil, fail("invalid_repository", err)
	}
	if err := repo.verifyLayout(); err != nil {
		return nil, fail("invalid_repository", err)
	}
	key, err := repo.executingConfig()
	if err != nil {
		return nil, fail(gitFailureCode(err, "invalid_repository"), err)
	}
	if key != "" {
		return nil, fail("unsafe_repository", fmt.Errorf("repository config %q runs an external program, which read-only review refuses to execute", key))
	}
	return repo, nil
}

func gitUnavailable(err error) bool {
	var execErr *exec.Error
	return errors.Is(err, exec.ErrNotFound) || errors.As(err, &execErr)
}

func gitFailureCode(err error, fallback string) string {
	if errors.Is(err, errGitTimeout) {
		return "git_timeout"
	}
	if gitUnavailable(err) {
		return "git_unavailable"
	}
	return fallback
}

// reviewPrivateGlobs keeps credential-bearing paths out of a whole-repository
// diff; an explicit path is already refused by reviewGitPath.
var reviewPrivateGlobs = []string{
	":(exclude,glob)**/.env",
	":(exclude,glob)**/.env.*",
	":(exclude,glob)**/.netrc",
	":(exclude,glob)**/.ssh",
	":(exclude,glob)**/.aws",
	":(exclude,glob)**/.gnupg",
	":(exclude,glob)**/credentials",
}

// GitStatusWorker returns bounded porcelain status for an assigned worker.
func (s *Server) GitStatusWorker(workerID string) ClusterSandboxResultBody {
	repo, failure := s.openGitRepo("git_status", workerID)
	if failure != nil {
		return *failure
	}
	data, truncated, stderr, err := repo.run("status", "--porcelain=v1", "-z", "--branch", "--untracked-files=all")
	if err != nil {
		if stderr == "" {
			stderr = err.Error()
		}
		return reviewError("git_status", gitFailureCode(err, "not_repository"), fmt.Errorf("%s", stderr))
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
		if credentialPath(item.Path) || credentialPath(item.OrigPath) {
			continue
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
	repo, failure := s.openGitRepo("git_diff", workerID)
	if failure != nil {
		return *failure
	}
	args := []string{"diff", "--no-ext-diff", "--no-textconv", "--src-prefix=a/", "--dst-prefix=b/"}
	if staged {
		args = append(args, "--cached")
	}
	relPath = strings.TrimSpace(relPath)
	if relPath != "" {
		var pathErr error
		_, relPath, pathErr = s.reviewGitPath(workerID, relPath)
		if pathErr != nil {
			return reviewError("git_diff", "invalid_path", pathErr)
		}
		args = append(args, "--", relPath)
	} else {
		// A whole-repository diff hides credential-bearing files too, so the
		// viewer and the status list agree on what is reviewable.
		args = append(args, "--")
		args = append(args, reviewPrivateGlobs...)
	}
	data, truncated, stderr, err := repo.run(args...)
	if err != nil {
		if stderr == "" {
			stderr = err.Error()
		}
		return reviewError("git_diff", gitFailureCode(err, "git_failed"), fmt.Errorf("%s", stderr))
	}
	out := ClusterSandboxResultBody{
		OK: true, Request: "git_diff", Path: relPath, Diff: string(data), Encoding: "utf-8", Truncated: truncated,
	}
	if !staged && relPath != "" && len(data) == 0 {
		status, _, _, statusErr := repo.run("status", "--porcelain=v1", "-z", "--untracked-files=all", "--", relPath)
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
