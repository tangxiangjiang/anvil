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
	if err := st.Delete(agentID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return newError("agent not found: "+agentID, false, nil)
		}
		return newError("delete agent", false, err)
	}
	// The undo history goes with the conversation it belongs to; a checkpoint
	// for a session nobody can open again is only disk. A failure here is
	// reported rather than ignored, because the person asked for the agent to be
	// gone and this is what would still be there.
	root := filepath.Join(cwd, ".anvil", "checkpoints")
	if opts.Checkpoints != nil {
		root = checkpointRoot(cwd, *opts.Checkpoints)
	}
	if err := checkpoint.Remove(root, agentID); err != nil {
		return newError("delete agent checkpoints", false, err)
	}
	return nil
}
