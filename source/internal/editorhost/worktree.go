// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package editorhost

import (
	"context"
	"encoding/json"
	"path/filepath"

	anvil "github.com/anvil-dev/anvil"
)

func (s *Server) handleWorktreePrepare(
	ctx context.Context,
	params json.RawMessage,
) (any, *RPCError) {
	p, err := decode[WorktreePrepareParams](params)
	if err != nil {
		return nil, errInvalidParams("worktree.prepare: " + err.Error())
	}
	worker, callErr := anvil.PrepareWorktreeWorker(ctx, anvil.WorktreePrepareOptions{
		Local:        anvil.LocalOptions{CWD: s.cwdPath(), Store: p.Store},
		WorktreeRoot: p.WorktreeRoot,
		AgentID:      p.AgentID,
		ArtifactID:   p.ArtifactID,
		WorkItemID:   p.WorkItemID,
		ExecutionID:  p.ExecutionID,
		Attempt:      p.Attempt,
		BaseRef:      p.BaseRef,
		OwnerHostID:  s.hostID,
		MaxWorkers:   p.MaxWorkers,
	})
	if callErr != nil {
		return nil, errHost(callErr)
	}
	return WorktreePrepareResult{Worker: worktreeWorkerInfo(worker)}, nil
}

func (s *Server) handleWorktreeList(
	ctx context.Context,
	params json.RawMessage,
) (any, *RPCError) {
	p, err := decode[WorktreeListParams](params)
	if err != nil {
		return nil, errInvalidParams("worktree.list: " + err.Error())
	}
	workers, callErr := anvil.ListWorktreeWorkers(ctx, anvil.WorktreeListOptions{
		Local:        anvil.LocalOptions{CWD: s.cwdPath(), Store: p.Store},
		WorktreeRoot: p.WorktreeRoot, AgentID: p.AgentID,
	})
	if callErr != nil {
		return nil, errHost(callErr)
	}
	result := WorktreeListResult{Workers: make([]WorktreeWorkerInfo, len(workers))}
	for i, worker := range workers {
		result.Workers[i] = worktreeWorkerInfo(worker)
	}
	return result, nil
}

func (s *Server) handleWorktreeReview(
	ctx context.Context,
	params json.RawMessage,
) (any, *RPCError) {
	p, err := decode[WorktreeReviewParams](params)
	if err != nil {
		return nil, errInvalidParams("worktree.review: " + err.Error())
	}
	review, callErr := anvil.ReviewWorktreeWorker(ctx, anvil.WorktreeReviewOptions{
		Local:        anvil.LocalOptions{CWD: s.cwdPath(), Store: p.Store},
		WorktreeRoot: p.WorktreeRoot, AgentID: p.AgentID,
		WorkerID: p.WorkerID, ExpectedRevision: p.ExpectedRevision,
	})
	if callErr != nil {
		return nil, errHost(callErr)
	}
	return WorktreeReviewResult{Review: worktreeReviewInfo(review)}, nil
}

func (s *Server) handleWorktreeIntegrate(
	ctx context.Context,
	params json.RawMessage,
) (any, *RPCError) {
	p, err := decode[WorktreeIntegrateParams](params)
	if err != nil {
		return nil, errInvalidParams("worktree.integrate: " + err.Error())
	}
	result, callErr := anvil.IntegrateWorktreeWorker(ctx, anvil.WorktreeIntegrateOptions{
		Local:        anvil.LocalOptions{CWD: s.cwdPath(), Store: p.Store},
		WorktreeRoot: p.WorktreeRoot, AgentID: p.AgentID,
		WorkerID: p.WorkerID, ExpectedRevision: p.ExpectedRevision,
		ExpectedBase: p.ExpectedBase, ExpectedHead: p.ExpectedHead,
		ExpectedPatchDigest: p.ExpectedPatchDigest, Strategy: p.Strategy,
	})
	if callErr != nil {
		return nil, errHost(callErr)
	}
	return WorktreeMutationResult{
		Worker: worktreeWorkerInfo(result.Worker), ResultCommit: result.ResultCommit,
	}, nil
}

func (s *Server) handleWorktreeDiscard(
	ctx context.Context,
	params json.RawMessage,
) (any, *RPCError) {
	p, err := decode[WorktreeDiscardParams](params)
	if err != nil {
		return nil, errInvalidParams("worktree.discard: " + err.Error())
	}
	worker, callErr := anvil.DiscardWorktreeWorker(ctx, anvil.WorktreeDiscardOptions{
		Local:        anvil.LocalOptions{CWD: s.cwdPath(), Store: p.Store},
		WorktreeRoot: p.WorktreeRoot, AgentID: p.AgentID,
		WorkerID: p.WorkerID, ExpectedRevision: p.ExpectedRevision,
		ExpectedPatchDigest: p.ExpectedPatchDigest,
	})
	if callErr != nil {
		return nil, errHost(callErr)
	}
	return WorktreeMutationResult{Worker: worktreeWorkerInfo(worker)}, nil
}

func (s *Server) handleWorktreeCleanup(
	ctx context.Context,
	params json.RawMessage,
) (any, *RPCError) {
	p, err := decode[WorktreeCleanupParams](params)
	if err != nil {
		return nil, errInvalidParams("worktree.cleanup: " + err.Error())
	}
	result, callErr := anvil.CleanupWorktreeWorker(ctx, anvil.WorktreeCleanupOptions{
		Local:        anvil.LocalOptions{CWD: s.cwdPath(), Store: p.Store},
		WorktreeRoot: p.WorktreeRoot, AgentID: p.AgentID,
		WorkerID: p.WorkerID, ExpectedRevision: p.ExpectedRevision,
		ExpectedReviewDigest: p.ExpectedReviewDigest, RetainBranch: p.RetainBranch,
	})
	if callErr != nil {
		return nil, errHost(callErr)
	}
	removed := make([]string, len(result.RemovedPaths))
	for i, path := range result.RemovedPaths {
		removed[i] = filepath.Base(path)
	}
	return WorktreeMutationResult{
		Worker:       worktreeWorkerInfo(result.Worker),
		RemovedPaths: removed, RetainedBranch: result.RetainedBranch,
	}, nil
}

func worktreeWorkerInfo(worker anvil.WorktreeWorker) WorktreeWorkerInfo {
	info := WorktreeWorkerInfo{
		ID: worker.ID, AgentID: worker.AgentID, ArtifactID: worker.ArtifactID,
		WorkItemID: worker.WorkItemID, ExecutionID: worker.ExecutionID,
		Revision: worker.Revision, State: string(worker.State),
		WorktreeLabel: filepath.Base(worker.WorktreeRoot),
		BaseCommit:    worker.BaseCommit, HeadCommit: worker.HeadCommit,
		IntegrationCommit: worker.IntegrationCommit, Failure: worker.Failure,
	}
	if worker.Review != nil {
		review := worktreeReviewInfo(*worker.Review)
		info.Review = &review
	}
	return info
}

func worktreeReviewInfo(review anvil.WorktreeReview) WorktreeReviewInfo {
	out := WorktreeReviewInfo{
		Schema: review.Schema, WorkerID: review.WorkerID,
		ManifestRevision: review.ManifestRevision,
		BaseCommit:       review.BaseCommit, CurrentBase: review.CurrentBase,
		HeadCommit: review.HeadCommit, DiffStat: review.DiffStat,
		PatchDigest:       review.PatchDigest,
		DirtyTracked:      append([]string(nil), review.DirtyTracked...),
		Untracked:         append([]string(nil), review.Untracked...),
		Ignored:           append([]string(nil), review.Ignored...),
		AllowedStrategies: append([]string(nil), review.AllowedStrategies...),
		Integrable:        review.Integrable,
		RefusalReasons:    append([]string(nil), review.RefusalReasons...),
		Interrupted:       review.Interrupted, ReviewedAt: review.ReviewedAt,
		Commits:      make([]WorktreeCommitInfo, len(review.Commits)),
		ChangedPaths: make([]WorktreeChangedPath, len(review.ChangedPaths)),
		Conflicts:    make([]WorktreeConflict, len(review.Conflicts)),
	}
	for i, commit := range review.Commits {
		out.Commits[i] = WorktreeCommitInfo{Hash: commit.Hash, Subject: commit.Subject}
	}
	for i, path := range review.ChangedPaths {
		out.ChangedPaths[i] = WorktreeChangedPath{
			Status: path.Status, Path: path.Path, OldPath: path.OldPath,
		}
	}
	for i, conflict := range review.Conflicts {
		out.Conflicts[i] = WorktreeConflict{
			Path: conflict.Path, Kind: conflict.Kind, Message: conflict.Message,
		}
	}
	return out
}
