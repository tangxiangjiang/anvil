package tasks_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anvil-dev/anvil/internal/tasks"
)

func TestProcessHandleStaysWithOriginalManager(t *testing.T) {
	started := make(chan struct{})
	stopped := make(chan struct{})
	original := tasks.NewManager()
	info, err := original.StartAgent(tasks.AgentSpec{
		AgentID: "agent-1",
		Run: func(ctx context.Context) (string, string, error) {
			close(started)
			<-ctx.Done()
			close(stopped)
			return "cancelled", "", context.Cause(ctx)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	<-started

	restarted := tasks.NewManager()
	if len(restarted.List()) != 0 {
		t.Fatal("a restarted manager must not rehydrate process handles")
	}
	if _, err := restarted.Cancel(info.ID); err == nil || !strings.Contains(err.Error(), "unknown task") {
		t.Fatalf("restart cancel error=%v", err)
	}
	select {
	case <-stopped:
		t.Fatal("a manager without the original handle controlled the task")
	default:
	}
	if _, err := original.Cancel(info.ID); err != nil {
		t.Fatal(err)
	}
	<-stopped
}

func TestCancelCompletionRaceKeepsFirstTerminalState(t *testing.T) {
	mgr := tasks.NewManager()
	var states []tasks.Info
	mgr.SetPersistenceOnChange(func(info tasks.Info) error {
		states = append(states, info)
		return nil
	})
	release := make(chan struct{})
	info, err := mgr.StartAgent(tasks.AgentSpec{
		AgentID: "agent-1",
		Run: func(context.Context) (string, string, error) {
			<-release
			return "finished", "", nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		close(release)
	}()
	go func() {
		defer wg.Done()
		_, _ = mgr.Cancel(info.ID)
	}()
	wg.Wait()
	final, err := mgr.Get(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status == tasks.StatusRunning {
		t.Fatal("race left task running")
	}
	if len(states) != 2 || states[0].TaskSeq != 1 || states[1].TaskSeq != 2 {
		t.Fatalf("persisted states=%+v", states)
	}
}

func TestTaskPersistenceBoundaryFailurePreventsStart(t *testing.T) {
	mgr := tasks.NewManager()
	mgr.SetPersistenceOnChange(func(tasks.Info) error { return errors.New("disk full") })
	started := make(chan struct{}, 1)
	_, err := mgr.StartAgent(tasks.AgentSpec{
		AgentID: "agent-1",
		Run: func(context.Context) (string, string, error) {
			started <- struct{}{}
			return "finished", "", nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "persist task start") {
		t.Fatalf("StartAgent error=%v", err)
	}
	if len(mgr.List()) != 0 {
		t.Fatal("failed durable acceptance left a live task")
	}
	select {
	case <-started:
		t.Fatal("task ran before its running state was durable")
	default:
	}
}

func TestTaskStateSequence(t *testing.T) {
	mgr := tasks.NewManager()
	var seqs []uint64
	mgr.SetPersistenceOnChange(func(info tasks.Info) error {
		seqs = append(seqs, info.TaskSeq)
		return nil
	})
	release := make(chan struct{})
	info, err := mgr.StartAgent(tasks.AgentSpec{
		AgentID: "agent-1",
		Run: func(context.Context) (string, string, error) {
			<-release
			return "finished", "", nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.SetChildAgent(info.ID, "agent-child"); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Renew(); err != nil {
		t.Fatal(err)
	}
	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for {
		got, err := mgr.Get(info.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != tasks.StatusRunning {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("task did not finish")
		}
		time.Sleep(time.Millisecond)
	}
	if len(seqs) != 4 || seqs[0] != 1 || seqs[1] != 2 || seqs[2] != 3 || seqs[3] != 4 {
		t.Fatalf("task sequences=%v", seqs)
	}
}
