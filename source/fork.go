// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"errors"

	"github.com/anvil-dev/anvil/internal/id"
	"github.com/anvil-dev/anvil/internal/store"
)

// forkSuffix distinguishes a branch from the conversation it came from.
const forkSuffix = " (fork)"

// ForkAgent copies a stored conversation up to a finished run into a new agent.
//
// The run id is the branch point, and it has to be the end of a run: a fork made
// anywhere else would copy a `run_start` whose `run_end` never followed, and the
// new conversation would resume from a turn that, as far as the store can tell,
// never finished.
//
// The copy is a copy. Every record up to and including that `run_end` is written
// again in the same order, and the only field this function changes is the
// `meta` agent id — it has to name the file the records now live in. Nothing
// else is rewritten, no timestamp is touched, and the source file is not opened
// for writing at all, so the fork's history is the prefix's history.
//
// The title is the one thing appended rather than copied. A listing shows the
// first message as a session's name, which would make a fork and its source rows
// that read alike; a new `meta` record carrying the same name with " (fork)" on
// the end is how a rename already works, and it keeps the copy untouched.
//
// The new agent is not opened here. Opening a session is the caller's step — a
// client forking from a sidebar row has no tab to put it in yet — so this
// returns its id and stops.
func ForkAgent(ctx context.Context, agentID, runID string, opts ListOptions) (string, error) {
	_ = ctx
	if agentID == "" {
		return "", newError("agent id is required", false, nil)
	}
	if runID == "" {
		return "", newError("run id is required", false, nil)
	}
	cwd, err := resolveListCWD(opts.Local.CWD)
	if err != nil {
		return "", err
	}
	st, err := store.Open(cwd, opts.Local.Store)
	if err != nil {
		return "", newError("open store", false, err)
	}
	defer st.Close()

	records, err := st.Load(agentID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return "", newError("agent not found: "+agentID, false, nil)
		}
		return "", newError("load agent store", false, err)
	}
	boundary, started := -1, false
	for i, rec := range records {
		if rec.RunID != runID {
			continue
		}
		if rec.Type == store.TypeRunEnd && boundary < 0 {
			boundary = i
		}
		if rec.Type == store.TypeRunStart {
			started = true
		}
	}
	if boundary < 0 {
		// A run that began and has no end is a different answer from a run id
		// nothing knows: the first is a turn that is still going (or was killed
		// before it could record an end), and the second is a wrong id.
		if started {
			return "", newError("run has not ended: "+runID, false, nil)
		}
		return "", newError("run not found: "+runID, false, nil)
	}

	prefix := records[:boundary+1]
	newID := "agent-" + id.New()
	// A copy that fails halfway leaves a conversation holding part of a turn,
	// which is worse than no fork at all. The file is this function's own — the
	// id was just generated — so removing it is safe.
	fail := func(message string, cause error) (string, error) {
		_ = st.Delete(newID)
		return "", newError(message, false, cause)
	}
	for _, rec := range prefix {
		if rec.Type == store.TypeMeta {
			rec.AgentID = newID
			// A fork is a conversation of its own, not a child of the agent it copied.
			// Keeping ParentAgentID and Kind would make deleting the original parent
			// delete the fork too (audit #27).
			rec.ParentAgentID = ""
			rec.ParentRunID = ""
			rec.ParentToolCallID = ""
			rec.Kind = ""
		}
		if err := st.Append(newID, rec); err != nil {
			return fail("write the fork", err)
		}
	}
	if title := store.Summarize(prefix).Title; title != "" {
		named := store.Record{Type: store.TypeMeta, AgentID: newID, Title: store.TitleWithSuffix(title, forkSuffix)}
		if err := st.Append(newID, named); err != nil {
			return fail("name the fork", err)
		}
	}
	return newID, nil
}
