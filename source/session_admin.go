// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"github.com/anvil-dev/anvil/internal/checkpoint"
	"github.com/anvil-dev/anvil/internal/store"
)

// RenameAgent gives a stored conversation a name of its own.
//
// The name is written as one more `meta` record, which is the whole reason this
// is a store operation rather than an edit: the file stays append-only, nothing
// already in it is rewritten, and a resume reads the history exactly as it did
// before. The rollup that produces listings takes the last title it sees, so a
// rename replaces the title derived from the first message — and a second
// rename replaces the first.
//
// An empty or whitespace-only title is refused rather than written as one: the
// way back to a derived title is not a blank name, it is having written none.
func RenameAgent(ctx context.Context, agentID, title string, opts ListOptions) error {
	_ = ctx
	if agentID == "" {
		return newError("agent id is required", false, nil)
	}
	name := strings.TrimSpace(title)
	if name == "" {
		return newError("a session title cannot be empty", false, nil)
	}
	cwd, err := resolveListCWD(opts.Local.CWD)
	if err != nil {
		return err
	}
	st, err := store.Open(cwd, opts.Local.Store)
	if err != nil {
		return newError("open store", false, err)
	}
	defer st.Close()
	// Appending to a file that is not there would create one holding only a
	// title: a session with no conversation, listed for ever. The rename is
	// about a session that exists, so a missing one is an error.
	if !st.Exists(agentID) {
		return newError("agent not found: "+agentID, false, nil)
	}
	if err := st.Append(agentID, store.Record{Type: store.TypeMeta, AgentID: agentID, Title: name}); err != nil {
		return newError("rename agent", false, err)
	}
	return nil
}

// DeleteAgent forgets a stored conversation.
//
// This removes the records themselves — one file for the JSONL store, one
// agent's rows for the SQLite one — and not a marker that hides them: a delete
// that leaves the bytes on disk is not what the person asking for it meant.
//
// A live session is a different thing and is not this function's concern: it
// holds the agent id, and its next run would write the file again. Callers that
// own sessions close the one holding the agent first.
//
// The agent's checkpoints go too. They are the undo history of the conversation
// being forgotten, and keeping them would leave behind the very thing the
// delete was about.
//
// A subagent's conversation is part of its parent's story, not a session of its
// own: deleting a session deletes every agent it created, transitively. The
// files are read for the parent link before anything is removed, because they
// are the only record of it.
func DeleteAgent(ctx context.Context, agentID string, opts ListOptions) error {
	_ = ctx
	if agentID == "" {
		return newError("agent id is required", false, nil)
	}
	cwd, err := resolveListCWD(opts.Local.CWD)
	if err != nil {
		return err
	}
	st, err := store.Open(cwd, opts.Local.Store)
	if err != nil {
		return newError("open store", false, err)
	}
	defer st.Close()
	descendants := subagentDescendants(st, agentID)
	if err := st.Delete(agentID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return newError("agent not found: "+agentID, false, nil)
		}
		return newError("delete agent", false, err)
	}
	for _, id := range descendants {
		if err := st.Delete(id); err != nil && !errors.Is(err, store.ErrNotFound) {
			return newError("delete subagent "+id, false, err)
		}
	}
	// The undo history goes with the conversation it belongs to; a checkpoint
	// for a session nobody can open again is only disk. A failure here is
	// reported rather than ignored, because the person asked for the agent to be
	// gone and this is what would still be there.
	root := filepath.Join(cwd, ".anvil", "checkpoints")
	if opts.Checkpoints != nil {
		root = checkpointRoot(cwd, *opts.Checkpoints)
	}
	for _, id := range append([]string{agentID}, descendants...) {
		if err := checkpoint.Remove(root, id); err != nil {
			return newError("delete agent checkpoints", false, err)
		}
	}
	return nil
}

// subagentDescendants walks the stored parent links and returns every agent
// under agentID, in no particular order. A file that cannot be read is skipped:
// a conversation that is already unreadable cannot be a reason to keep the rest.
func subagentDescendants(st store.Backend, agentID string) []string {
	ids, err := st.ListIDs()
	if err != nil {
		return nil
	}
	children := map[string][]string{}
	for _, id := range ids {
		if id == agentID {
			continue
		}
		recs, err := st.Load(id)
		if err != nil {
			continue
		}
		sum := store.Summarize(recs)
		if sum.ParentAgentID != "" {
			children[sum.ParentAgentID] = append(children[sum.ParentAgentID], id)
		}
	}
	var out []string
	queue := append([]string(nil), children[agentID]...)
	seen := map[string]bool{agentID: true}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
		queue = append(queue, children[id]...)
	}
	return out
}
