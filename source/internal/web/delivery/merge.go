// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package delivery

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Merge status values (0.39.15).
const (
	MergeNone    = ""
	MergePending = "pending"
	MergeMerged  = "merged"
	MergeError   = "error"
)

// MilestoneBranchName is the integration branch for a milestone id.
func MilestoneBranchName(msID string) string {
	msID = strings.TrimSpace(msID)
	if msID == "" {
		return "milestone/unknown"
	}
	return "milestone/" + msID
}

// Merger merges a milestone branch into main (injectable).
type Merger interface {
	MergeToMain(ctx context.Context, repoPath, branch string) (string, error)
}

// StubMerger records success without touching git.
type StubMerger struct{}

func (StubMerger) MergeToMain(_ context.Context, _, branch string) (string, error) {
	return "stub merge of " + branch + " into main (no git)", nil
}

// LocalGitMerger runs checkout main + merge --no-ff in repoPath.
type LocalGitMerger struct {
	GitBin string
}

func (m LocalGitMerger) bin() string {
	if m.GitBin != "" {
		return m.GitBin
	}
	return "git"
}

func (m LocalGitMerger) MergeToMain(ctx context.Context, repoPath, branch string) (string, error) {
	repoPath = strings.TrimSpace(repoPath)
	branch = strings.TrimSpace(branch)
	if repoPath == "" {
		return "", fmt.Errorf("repo path required")
	}
	if branch == "" {
		return "", fmt.Errorf("branch required")
	}
	abs, err := filepath.Abs(repoPath)
	if err != nil {
		return "", err
	}
	st, err := os.Stat(abs)
	if err != nil || !st.IsDir() {
		return "", fmt.Errorf("repo path not a directory: %s", abs)
	}
	run := func(args ...string) (string, error) {
		cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		cmd := exec.CommandContext(cctx, m.bin(), args...)
		cmd.Dir = abs
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		out := strings.TrimSpace(stdout.String())
		if err != nil {
			msg := strings.TrimSpace(stderr.String())
			if msg == "" {
				msg = err.Error()
			}
			return out, fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
		}
		return out, nil
	}
	if _, err := run("rev-parse", "--is-inside-work-tree"); err != nil {
		return "", fmt.Errorf("not a git work tree: %s", abs)
	}
	if _, err := run("checkout", "main"); err != nil {
		// try master
		if _, err2 := run("checkout", "master"); err2 != nil {
			return "", fmt.Errorf("checkout main/master: %v / %v", err, err2)
		}
	}
	msg := "delivery: merge " + branch
	out, err := run("merge", "--no-ff", "-m", msg, branch)
	if err != nil {
		return out, err
	}
	if out == "" {
		out = "merged " + branch + " into current branch"
	}
	return out, nil
}

// ResolveMergeRepo joins mergeRoot with projectRel when both set.
func ResolveMergeRepo(mergeRoot, projectRel string) string {
	mergeRoot = strings.TrimSpace(mergeRoot)
	projectRel = strings.TrimSpace(projectRel)
	if mergeRoot == "" {
		return ""
	}
	if projectRel == "" {
		return filepath.Clean(mergeRoot)
	}
	return filepath.Clean(filepath.Join(mergeRoot, projectRel))
}

// MergeMilestoneInput controls Accept→merge.
type MergeMilestoneInput struct {
	ForceStub bool
	Merger    Merger // optional override
	MergeRoot string // optional; from web config
}

// MergeMilestone merges after accept. Without a repo path, records a stub merge.
func (s *Store) MergeMilestone(msID string, in MergeMilestoneInput) (Milestone, error) {
	msID = strings.TrimSpace(msID)
	ms, err := s.getMilestone(msID)
	if err != nil {
		return Milestone{}, err
	}
	if ms.AcceptStatus != AcceptAccepted {
		return Milestone{}, fmt.Errorf("merge requires accept_status=accepted (got %q)", ms.AcceptStatus)
	}
	if ms.MergeStatus == MergeMerged {
		return ms, nil
	}
	branch := MilestoneBranchName(msID)
	project := ""
	if bind := parseMilestoneBind(msID); bind != nil {
		project = s.projectRelForBind(bind.EngID)
	}
	repo := ResolveMergeRepo(in.MergeRoot, project)
	merger := in.Merger
	note := ""
	status := MergeMerged
	if merger == nil {
		if in.ForceStub || repo == "" {
			merger = StubMerger{}
		} else {
			merger = LocalGitMerger{}
		}
	}
	out, err := merger.MergeToMain(context.Background(), repo, branch)
	if err != nil {
		status = MergeError
		note = err.Error()
		_, _ = s.db.Exec(`
UPDATE delivery_milestones SET merge_status=?, merge_note=?, merged_at=? WHERE id=?`,
			status, truncateRunes(note, 500), "", msID)
		return Milestone{}, fmt.Errorf("merge failed: %w", err)
	}
	note = out
	ts := nowRFC3339()
	_, err = s.db.Exec(`
UPDATE delivery_milestones SET merge_status=?, merge_note=?, merged_at=? WHERE id=?`,
		status, truncateRunes(note, 500), ts, msID)
	if err != nil {
		return Milestone{}, err
	}
	return s.getMilestone(msID)
}
