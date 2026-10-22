// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/anvil-dev/anvil/internal/checkpoint"
	"github.com/anvil-dev/anvil/internal/tools"
)

// CheckpointRun is one run's record of what the workspace looked like before
// it: the files its tool calls were about to change.
type CheckpointRun struct {
	RunID     string
	CreatedAt time.Time
	Files     []CheckpointFile
}

// CheckpointFile is one path a run captured.
type CheckpointFile struct {
	// Path is workspace-relative, slash-separated.
	Path string
	// Existed false means the run created the file; restoring deletes it.
	Existed bool
	Size    int64
}

// RestoredFile is one path a restore put back.
type RestoredFile struct {
	Path    string
	Deleted bool
	// Skipped is true when the user changed the file after the run, so restore
	// left it as it is. Reason says why.
	Skipped bool
	Reason  string
	// Warning is set when the file was restored without a post-run state to check
	// against, so restore could not tell whether the user had changed it.
	Warning string
}

// checkpointRoot is where an agent's checkpoints live, and whether capture is
// on at all. A nil CheckpointOptions in Options means off; here the zero value
// of the options struct means the defaults, since a caller asking to list has
// already decided it wants them.
func checkpointRoot(cwd string, opts CheckpointOptions) string {
	if opts.Dir != "" {
		return opts.Dir
	}
	return filepath.Join(cwd, ".anvil", "checkpoints")
}

// pruneCheckpoints trims an agent's history to the configured bounds. It is
// best-effort: a failed prune leaves old checkpoints behind, which is a disk
// cost rather than a reason for a session not to open.
func pruneCheckpoints(cwd, agentID string, opts *CheckpointOptions) {
	if opts == nil || agentID == "" {
		return
	}
	_ = checkpoint.Prune(checkpointRoot(cwd, *opts), agentID, opts.Keep, opts.MaxAge)
}

// ListCheckpoints returns the runs of one agent that have a checkpoint, newest
// first.
//
// This reads only what was captured; it does not open the session store and it
// does not need the agent to be live. A run that captured nothing has no
// directory and does not appear — listing a run that cannot be restored would
// offer an undo that does nothing.
func ListCheckpoints(cwd, agentID string, opts CheckpointOptions) ([]CheckpointRun, error) {
	if agentID == "" {
		return nil, newError("agent id is required", false, nil)
	}
	runs, err := checkpoint.List(checkpointRoot(cwd, opts), agentID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, newError("list checkpoints", false, err)
	}
	out := make([]CheckpointRun, 0, len(runs))
	for _, run := range runs {
		files := make([]CheckpointFile, 0, len(run.Files))
		for _, entry := range run.Files {
			files = append(files, CheckpointFile{Path: entry.Path, Existed: entry.Existed, Size: entry.Size})
		}
		out = append(out, CheckpointRun{RunID: run.RunID, CreatedAt: run.CreatedAt, Files: files})
	}
	return out, nil
}

// RestoreCheckpoint puts every file a run captured back the way it was, and
// deletes the files that run created.
//
// Each path from the manifest is resolved through the same sandbox a tool
// argument goes through, so a manifest that names a path outside the workspace
// is refused rather than followed. Restoring stops at the first path it cannot
// write; the files already put back stay put and the error names the one that
// failed, because rolling those back would mean overwriting them with content
// this function does not have.
func RestoreCheckpoint(cwd, agentID, runID string, opts CheckpointOptions) ([]RestoredFile, error) {
	if agentID == "" {
		return nil, newError("agent id is required", false, nil)
	}
	if runID == "" {
		return nil, newError("run id is required", false, nil)
	}
	resolve := func(rel string) (string, error) {
		return tools.ResolveInRoot(cwd, filepath.FromSlash(rel))
	}
	restored, err := checkpoint.Restore(checkpointRoot(cwd, opts), agentID, runID, resolve)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, newError("no checkpoint for run "+runID, false, nil)
		}
		return nil, newError("restore checkpoint", false, err)
	}
	out := make([]RestoredFile, 0, len(restored))
	for _, file := range restored {
		out = append(out, RestoredFile{
			Path:    file.Path,
			Deleted: file.Deleted,
			Skipped: file.Skipped,
			Reason:  file.Reason,
			Warning: file.Warning,
		})
	}
	return out, nil
}
