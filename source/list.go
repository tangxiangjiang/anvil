package anvil

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/anvil-dev/anvil/internal/store"
)

// AgentInfo is operator-facing metadata for a persisted local agent.
type AgentInfo struct {
	ID         string
	CWD        string
	Provider   string
	Model      string
	CreatedAt  time.Time
	UpdatedAt  time.Time
	LastStatus string
	LastRunID  string
	Runs       int
	Usage      *Usage // cumulative across run_end records when present
}

// ListOptions configures List / Get. Only Local.CWD is required.
type ListOptions struct {
	Local LocalOptions
}

// List returns agents stored under opts.Local.CWD.
func List(ctx context.Context, opts ListOptions) ([]AgentInfo, error) {
	_ = ctx
	cwd, err := resolveListCWD(opts.Local.CWD)
	if err != nil {
		return nil, err
	}
	st := store.New(cwd)
	ids, err := st.ListIDs()
	if err != nil {
		return nil, newError("list agents", false, err)
	}
	out := make([]AgentInfo, 0, len(ids))
	for _, id := range ids {
		info, err := loadInfo(st, id)
		if err != nil {
			continue
		}
		out = append(out, info)
	}
	return out, nil
}

// Get returns metadata for one agent under opts.Local.CWD.
func Get(ctx context.Context, agentID string, opts ListOptions) (*AgentInfo, error) {
	_ = ctx
	if agentID == "" {
		return nil, newError("agent id is required", false, nil)
	}
	cwd, err := resolveListCWD(opts.Local.CWD)
	if err != nil {
		return nil, err
	}
	st := store.New(cwd)
	info, err := loadInfo(st, agentID)
	if err != nil {
		return nil, err
	}
	return &info, nil
}

// Usage returns cumulative token usage for this agent from the local store (best-effort).
func (a *Agent) Usage() (*Usage, error) {
	if a == nil || a.store == nil {
		return nil, newError("nil agent", false, nil)
	}
	info, err := loadInfo(a.store, a.id)
	if err != nil {
		return nil, err
	}
	return info.Usage, nil
}

func loadInfo(st *store.Store, agentID string) (AgentInfo, error) {
	recs, err := st.Load(agentID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return AgentInfo{}, newError("agent not found: "+agentID, false, nil)
		}
		return AgentInfo{}, newError("load agent store", false, err)
	}
	sum := store.Summarize(recs)
	if sum.ID == "" {
		sum.ID = agentID
	}
	info := AgentInfo{
		ID:         sum.ID,
		CWD:        sum.CWD,
		Provider:   sum.Provider,
		Model:      sum.Model,
		CreatedAt:  sum.CreatedAt,
		UpdatedAt:  sum.UpdatedAt,
		LastStatus: sum.LastStatus,
		LastRunID:  sum.LastRunID,
		Runs:       sum.Runs,
	}
	if sum.Usage != nil {
		info.Usage = &Usage{
			InputTokens:  sum.Usage.InputTokens,
			OutputTokens: sum.Usage.OutputTokens,
			TotalTokens:  sum.Usage.TotalTokens,
		}
	}
	return info, nil
}

func resolveListCWD(cwd string) (string, error) {
	if cwd == "" {
		cwd = "."
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return "", newError("resolve cwd", false, err)
	}
	return abs, nil
}
