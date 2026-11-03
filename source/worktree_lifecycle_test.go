// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/anvil-dev/anvil/internal/store"
)

func TestWorktreeLifecyclePrepareDiscoverCleanup(t *testing.T) {
	base, managed := newWorktreeTestRepository(t)
	beforeHead := worktreeTestGit(t, base, "rev-parse", "HEAD")
	worker, err := PrepareWorktreeWorker(context.Background(), worktreeTestOptions(base, managed))
	if err != nil {
		t.Fatal(err)
	}
	if worker.State != WorktreeReady || worker.Revision != 3 ||
		worker.HeadCommit != beforeHead || !strings.HasPrefix(worker.Branch, "anvil/") {
		t.Fatalf("worker=%+v", worker)
	}
	canonicalManaged, managedErr := canonicalExistingPath(managed)
	if canonical, err := canonicalExistingPath(worker.WorktreeRoot); err != nil ||
		!sameWorktreePath(canonical, worker.WorktreeRoot) ||
		managedErr != nil ||
		!sameWorktreePath(filepath.Dir(worker.WorktreeRoot), canonicalManaged) {
		t.Fatalf(
			"worktree root=%q canonical=%q managed=%q canonicalManaged=%q err=%v managedErr=%v",
			worker.WorktreeRoot, canonical, managed, canonicalManaged, err, managedErr,
		)
	}
	if got := strings.TrimSpace(worktreeTestGit(t, worker.WorktreeRoot, "rev-parse", "HEAD")); got != beforeHead {
		t.Fatalf("worktree HEAD=%q want=%q", got, beforeHead)
	}
	owners, err := ListWorktreeWorkers(context.Background(), WorktreeListOptions{
		Local: LocalOptions{CWD: base}, WorktreeRoot: managed, AgentID: worker.AgentID,
	})
	if err != nil || len(owners) != 1 || owners[0].ID != worker.ID {
		t.Fatalf("owners=%+v err=%v", owners, err)
	}
	if runtime.GOOS != "windows" {
		for _, path := range []string{
			filepath.Join(managed, ".anvil", "owner.key"),
			worktreeOwnerPath(managed, worker.ID),
		} {
			info, statErr := os.Stat(path)
			if statErr != nil {
				t.Fatalf("ownership path %s: %v", path, statErr)
			}
			if info.Mode().Perm()&0o077 != 0 {
				t.Fatalf("non-private ownership path %s mode=%v", path, info.Mode())
			}
		}
	}
	if temporary, readErr := os.ReadDir(filepath.Join(managed, ".anvil", "tmp")); readErr != nil || len(temporary) != 0 {
		t.Fatalf("ownership temp files remain: %v err=%v", temporary, readErr)
	}

	result, err := CleanupWorktreeWorker(context.Background(), WorktreeCleanupOptions{
		Local: LocalOptions{CWD: base}, WorktreeRoot: managed,
		AgentID: worker.AgentID, WorkerID: worker.ID, ExpectedRevision: worker.Revision,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Worker.State != WorktreeRemoved || result.RetainedBranch ||
		len(result.RemovedPaths) != 1 {
		t.Fatalf("cleanup=%+v", result)
	}
	if _, err := os.Lstat(worker.WorktreeRoot); !os.IsNotExist(err) {
		t.Fatalf("worktree path remains: %v", err)
	}
	if branchExists, err := (worktreeGitRunner{}).branchExists(
		context.Background(), base, worker.Branch,
	); err != nil || branchExists {
		t.Fatalf("branch exists=%v err=%v", branchExists, err)
	}
	if after := strings.TrimSpace(worktreeTestGit(t, base, "rev-parse", "HEAD")); after != beforeHead {
		t.Fatalf("primary HEAD changed: %s -> %s", beforeHead, after)
	}
	if status := strings.TrimSpace(worktreeTestGit(
		t, base, "status", "--porcelain=v1", "--untracked-files=all",
	)); status != "" {
		t.Fatalf("primary worktree changed: %q", status)
	}

	again, err := CleanupWorktreeWorker(context.Background(), WorktreeCleanupOptions{
		Local: LocalOptions{CWD: base}, WorktreeRoot: managed,
		AgentID: worker.AgentID, WorkerID: worker.ID, ExpectedRevision: worker.Revision,
	})
	if err != nil || again.Worker.State != WorktreeRemoved {
		t.Fatalf("idempotent cleanup=%+v err=%v", again, err)
	}
}

func TestWorktreeLifecycleSQLiteOwnershipParity(t *testing.T) {
	base, managed := newWorktreeTestRepository(t)
	options := worktreeTestOptions(base, managed)
	options.Local.Store = store.KindSQLite
	worker, err := PrepareWorktreeWorker(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := latestStoredWorktree(
		options.Local, base, worker.AgentID, worker.ID,
	)
	if err != nil || !sameWorktreeManifest(worker, stored) {
		t.Fatalf("stored=%+v err=%v", stored, err)
	}
	result, err := CleanupWorktreeWorker(context.Background(), WorktreeCleanupOptions{
		Local: options.Local, WorktreeRoot: managed, AgentID: worker.AgentID,
		WorkerID: worker.ID, ExpectedRevision: worker.Revision,
	})
	if err != nil || result.Worker.State != WorktreeRemoved {
		t.Fatalf("cleanup=%+v err=%v", result, err)
	}
}

func TestWorktreeSecurityRejectsEscapesAndIneligibleRepositories(t *testing.T) {
	t.Run("cancelled before mutation", func(t *testing.T) {
		base, managed := newWorktreeTestRepository(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := PrepareWorktreeWorker(ctx, worktreeTestOptions(base, managed))
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err=%v", err)
		}
		if _, statErr := os.Lstat(managed); !os.IsNotExist(statErr) {
			t.Fatalf("managed root was created after cancellation: %v", statErr)
		}
	})

	t.Run("dirty base before managed mutation", func(t *testing.T) {
		base, managed := newWorktreeTestRepository(t)
		if err := os.WriteFile(filepath.Join(base, "dirty.txt"), []byte("dirty"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := PrepareWorktreeWorker(context.Background(), worktreeTestOptions(base, managed))
		assertWorktreeErrorCode(t, err, "dirty")
		if _, statErr := os.Lstat(managed); !os.IsNotExist(statErr) {
			t.Fatalf("managed root was created for dirty base: %v", statErr)
		}
	})

	t.Run("managed root inside base", func(t *testing.T) {
		base, _ := newWorktreeTestRepository(t)
		managed := filepath.Join(base, "nested", "workers")
		_, err := PrepareWorktreeWorker(context.Background(), worktreeTestOptions(base, managed))
		assertWorktreeErrorCode(t, err, "security")
		if _, statErr := os.Lstat(managed); !os.IsNotExist(statErr) {
			t.Fatalf("inside-base root was created: %v", statErr)
		}
	})

	t.Run("untrusted ids never become paths", func(t *testing.T) {
		base, managed := newWorktreeTestRepository(t)
		options := worktreeTestOptions(base, managed)
		options.WorkItemID = "../../C:\\absolute\\escape"
		worker, err := PrepareWorktreeWorker(context.Background(), options)
		if err != nil {
			t.Fatal(err)
		}
		canonicalManaged, err := canonicalExistingPath(managed)
		if err != nil || !pathWithinWorktree(canonicalManaged, worker.WorktreeRoot) ||
			!sameWorktreePath(filepath.Dir(worker.WorktreeRoot), canonicalManaged) {
			t.Fatalf("untrusted id escaped: worker=%+v err=%v", worker, err)
		}
	})

	t.Run("symbolic link root", func(t *testing.T) {
		base, managed := newWorktreeTestRepository(t)
		target := filepath.Join(filepath.Dir(managed), "real-workers")
		if err := os.Mkdir(target, 0o700); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(filepath.Dir(managed), "linked-workers")
		if err := os.Symlink(target, link); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		_, err := PrepareWorktreeWorker(context.Background(), worktreeTestOptions(base, link))
		assertWorktreeErrorCode(t, err, "security")
		entries, readErr := os.ReadDir(target)
		if readErr != nil || len(entries) != 0 {
			t.Fatalf("symlink target mutated: entries=%v err=%v", entries, readErr)
		}
	})

	t.Run("linked checkout cannot be the primary base", func(t *testing.T) {
		base, managed := newWorktreeTestRepository(t)
		linked := filepath.Join(t.TempDir(), "linked")
		worktreeTestGit(t, base, "worktree", "add", "--detach", linked, "HEAD")
		otherRoot := filepath.Join(filepath.Dir(managed), "linked-managed")
		_, err := PrepareWorktreeWorker(context.Background(), worktreeTestOptions(linked, otherRoot))
		assertWorktreeErrorCode(t, err, "security")
	})

	t.Run("executable local Git config", func(t *testing.T) {
		base, managed := newWorktreeTestRepository(t)
		worktreeTestGit(t, base, "config", "filter.danger.clean", "danger-command")
		_, err := PrepareWorktreeWorker(context.Background(), worktreeTestOptions(base, managed))
		assertWorktreeErrorCode(t, err, "unsupported")
	})

	t.Run("submodules are refused", func(t *testing.T) {
		base, managed := newWorktreeTestRepository(t)
		if err := os.WriteFile(filepath.Join(base, ".gitmodules"), []byte("[submodule \"x\"]\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := PrepareWorktreeWorker(context.Background(), worktreeTestOptions(base, managed))
		assertWorktreeErrorCode(t, err, "unsupported")
	})

	if runtime.GOOS == "windows" {
		t.Run("junction root", func(t *testing.T) {
			base, managed := newWorktreeTestRepository(t)
			target := filepath.Join(filepath.Dir(managed), "junction-target")
			if err := os.Mkdir(target, 0o700); err != nil {
				t.Fatal(err)
			}
			junction := filepath.Join(filepath.Dir(managed), "junction-workers")
			command := exec.Command("cmd", "/c", "mklink", "/J", junction, target)
			if out, err := command.CombinedOutput(); err != nil {
				t.Skipf("junction unavailable: %v (%s)", err, out)
			}
			_, err := PrepareWorktreeWorker(
				context.Background(), worktreeTestOptions(base, junction),
			)
			assertWorktreeErrorCode(t, err, "security")
			entries, readErr := os.ReadDir(target)
			if readErr != nil || len(entries) != 0 {
				t.Fatalf("junction target mutated: entries=%v err=%v", entries, readErr)
			}
		})

		t.Run("case variant still detects inside base", func(t *testing.T) {
			base, _ := newWorktreeTestRepository(t)
			managed := strings.ToUpper(filepath.Join(base, "workers"))
			_, err := PrepareWorktreeWorker(context.Background(), worktreeTestOptions(base, managed))
			assertWorktreeErrorCode(t, err, "security")
		})
	}
}

func TestWorktreeOwnershipRejectsForgeryCollisionQuotaAndLockContention(t *testing.T) {
	t.Run("forged recovery record", func(t *testing.T) {
		base, managed := newWorktreeTestRepository(t)
		worker, err := PrepareWorktreeWorker(context.Background(), worktreeTestOptions(base, managed))
		if err != nil {
			t.Fatal(err)
		}
		path := worktreeOwnerPath(managed, worker.ID)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		data = append(data, byte(' '))
		var envelope map[string]any
		if json.Unmarshal(data, &envelope) != nil {
			t.Fatal("fixture owner record was not JSON")
		}
		envelope["signature"] = strings.Repeat("0", 64)
		data, _ = json.Marshal(envelope)
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		_, err = ListWorktreeWorkers(context.Background(), WorktreeListOptions{
			Local: LocalOptions{CWD: base}, WorktreeRoot: managed,
		})
		assertWorktreeErrorCode(t, err, "ownership")
		_, err = CleanupWorktreeWorker(context.Background(), WorktreeCleanupOptions{
			Local: LocalOptions{CWD: base}, WorktreeRoot: managed,
			AgentID: worker.AgentID, WorkerID: worker.ID, ExpectedRevision: worker.Revision,
		})
		assertWorktreeErrorCode(t, err, "ownership")
		if _, statErr := os.Stat(worker.WorktreeRoot); statErr != nil {
			t.Fatalf("forged cleanup removed data: %v", statErr)
		}
	})

	t.Run("branch collision", func(t *testing.T) {
		base, managed := newWorktreeTestRepository(t)
		restore := fixedWorktreeIDs(t, "a")
		defer restore()
		id := "w-" + strings.Repeat("a", 24)
		branch := generatedWorktreeBranch("agent-one", "item-one", 1, id)
		worktreeTestGit(t, base, "branch", branch, "HEAD")
		_, err := PrepareWorktreeWorker(context.Background(), worktreeTestOptions(base, managed))
		assertWorktreeErrorCode(t, err, "collision")
		if got := strings.TrimSpace(worktreeTestGit(t, base, "rev-parse", branch)); got == "" {
			t.Fatal("existing branch was modified")
		}
		if _, statErr := os.Lstat(managed); !os.IsNotExist(statErr) {
			t.Fatalf("managed root was created for branch collision: %v", statErr)
		}
	})

	t.Run("destination collision", func(t *testing.T) {
		base, managed := newWorktreeTestRepository(t)
		restore := fixedWorktreeIDs(t, "b")
		defer restore()
		if err := os.MkdirAll(filepath.Join(managed, "wt-w-"+strings.Repeat("b", 24)), 0o700); err != nil {
			t.Fatal(err)
		}
		_, err := PrepareWorktreeWorker(context.Background(), worktreeTestOptions(base, managed))
		assertWorktreeErrorCode(t, err, "collision")
	})

	t.Run("quota", func(t *testing.T) {
		base, managed := newWorktreeTestRepository(t)
		first := worktreeTestOptions(base, managed)
		first.MaxWorkers = 1
		if _, err := PrepareWorktreeWorker(context.Background(), first); err != nil {
			t.Fatal(err)
		}
		second := first
		second.Attempt = 2
		_, err := PrepareWorktreeWorker(context.Background(), second)
		assertWorktreeErrorCode(t, err, "quota")
	})

	t.Run("one owner per work item attempt", func(t *testing.T) {
		base, managed := newWorktreeTestRepository(t)
		options := worktreeTestOptions(base, managed)
		first, err := PrepareWorktreeWorker(context.Background(), options)
		if err != nil {
			t.Fatal(err)
		}
		_, err = PrepareWorktreeWorker(context.Background(), options)
		assertWorktreeErrorCode(t, err, "ownership")
		owners, listErr := ListWorktreeWorkers(context.Background(), WorktreeListOptions{
			Local: options.Local, WorktreeRoot: managed,
		})
		if listErr != nil || len(owners) != 1 || owners[0].ID != first.ID {
			t.Fatalf("owners=%+v err=%v", owners, listErr)
		}
	})

	t.Run("lock contention", func(t *testing.T) {
		base, managed := newWorktreeTestRepository(t)
		worker, err := PrepareWorktreeWorker(context.Background(), worktreeTestOptions(base, managed))
		if err != nil {
			t.Fatal(err)
		}
		release, err := acquireWorktreeLifecycleLock(managed)
		if err != nil {
			t.Fatal(err)
		}
		defer release()
		_, err = CleanupWorktreeWorker(context.Background(), WorktreeCleanupOptions{
			Local: LocalOptions{CWD: base}, WorktreeRoot: managed,
			AgentID: worker.AgentID, WorkerID: worker.ID, ExpectedRevision: worker.Revision,
		})
		assertWorktreeErrorCode(t, err, "busy")
		if _, statErr := os.Stat(worker.WorktreeRoot); statErr != nil {
			t.Fatalf("contended cleanup removed data: %v", statErr)
		}
	})

	t.Run("interrupted preparation is discoverable", func(t *testing.T) {
		_, managed := newWorktreeTestRepository(t)
		if err := createManagedWorktreeRoot(managed); err != nil {
			t.Fatal(err)
		}
		key, err := initializeWorktreeManager(managed)
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC()
		worker := WorktreeWorker{
			Schema: worktreeManifestSchema, Revision: 2,
			ID: "w-" + strings.Repeat("c", 24), AgentID: "agent-one",
			ArtifactID: "artifact-one", WorkItemID: "item-one",
			ExecutionID: "exec-" + strings.Repeat("c", 24), Attempt: 1,
			State: WorktreePreparing, WorktreeRoot: filepath.Join(managed, "wt-crashed"),
			OwnerHostID: "host-one", CreatedAt: now, UpdatedAt: now,
		}
		if err := writeInitialWorktreeOwner(managed, key, worker); err != nil {
			t.Fatal(err)
		}
		workers, err := ListWorktreeWorkers(context.Background(), WorktreeListOptions{
			WorktreeRoot: managed,
		})
		if err != nil || len(workers) != 1 || workers[0].State != WorktreePreparing {
			t.Fatalf("workers=%+v err=%v", workers, err)
		}
	})
}

func TestWorktreeCleanupFailsClosedForDirtyOrMismatchedState(t *testing.T) {
	t.Run("traversal worker id", func(t *testing.T) {
		base, managed := newWorktreeTestRepository(t)
		worker, err := PrepareWorktreeWorker(context.Background(), worktreeTestOptions(base, managed))
		if err != nil {
			t.Fatal(err)
		}
		_, err = CleanupWorktreeWorker(context.Background(), WorktreeCleanupOptions{
			Local: LocalOptions{CWD: base}, WorktreeRoot: managed,
			AgentID: worker.AgentID, WorkerID: "../" + worker.ID,
			ExpectedRevision: worker.Revision,
		})
		assertWorktreeErrorCode(t, err, "invalid")
		if _, statErr := os.Stat(worker.WorktreeRoot); statErr != nil {
			t.Fatalf("traversal cleanup removed worktree: %v", statErr)
		}
	})

	t.Run("dirty worker", func(t *testing.T) {
		base, managed := newWorktreeTestRepository(t)
		worker, err := PrepareWorktreeWorker(context.Background(), worktreeTestOptions(base, managed))
		if err != nil {
			t.Fatal(err)
		}
		dirty := filepath.Join(worker.WorktreeRoot, "untracked.txt")
		if err := os.WriteFile(dirty, []byte("keep me"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err = CleanupWorktreeWorker(context.Background(), WorktreeCleanupOptions{
			Local: LocalOptions{CWD: base}, WorktreeRoot: managed,
			AgentID: worker.AgentID, WorkerID: worker.ID, ExpectedRevision: worker.Revision,
		})
		assertWorktreeErrorCode(t, err, "dirty")
		if data, readErr := os.ReadFile(dirty); readErr != nil || string(data) != "keep me" {
			t.Fatalf("dirty data was not preserved: %q err=%v", data, readErr)
		}
		if status := strings.TrimSpace(worktreeTestGit(
			t, base, "status", "--porcelain=v1", "--untracked-files=all",
		)); status != "" {
			t.Fatalf("primary worktree changed: %q", status)
		}
	})

	t.Run("session ownership mismatch", func(t *testing.T) {
		base, managed := newWorktreeTestRepository(t)
		worker, err := PrepareWorktreeWorker(context.Background(), worktreeTestOptions(base, managed))
		if err != nil {
			t.Fatal(err)
		}
		forged := worker
		forged.Revision++
		forged.Branch = "anvil/forged"
		payload, _ := json.Marshal(forged)
		backend, err := store.Open(base, store.KindJSONL)
		if err != nil {
			t.Fatal(err)
		}
		err = backend.Append(worker.AgentID, store.Record{
			Type: store.TypeWorktree, Time: time.Now().UTC(),
			AgentID: worker.AgentID, Worktree: payload,
		})
		_ = backend.Close()
		if err != nil {
			t.Fatal(err)
		}
		_, err = CleanupWorktreeWorker(context.Background(), WorktreeCleanupOptions{
			Local: LocalOptions{CWD: base}, WorktreeRoot: managed,
			AgentID: worker.AgentID, WorkerID: worker.ID, ExpectedRevision: worker.Revision,
		})
		assertWorktreeErrorCode(t, err, "ownership")
		if _, statErr := os.Stat(worker.WorktreeRoot); statErr != nil {
			t.Fatalf("mismatched ownership removed worktree: %v", statErr)
		}
	})
}

func TestWorktreeCleanupRetainsBranchOnlyWhenRequested(t *testing.T) {
	base, managed := newWorktreeTestRepository(t)
	worker, err := PrepareWorktreeWorker(context.Background(), worktreeTestOptions(base, managed))
	if err != nil {
		t.Fatal(err)
	}
	result, err := CleanupWorktreeWorker(context.Background(), WorktreeCleanupOptions{
		Local: LocalOptions{CWD: base}, WorktreeRoot: managed,
		AgentID: worker.AgentID, WorkerID: worker.ID,
		ExpectedRevision: worker.Revision, RetainBranch: true,
	})
	if err != nil || !result.RetainedBranch ||
		result.Worker.CleanupPolicy != "retain_branch" {
		t.Fatalf("cleanup=%+v err=%v", result, err)
	}
	exists, err := (worktreeGitRunner{}).branchExists(
		context.Background(), base, worker.Branch,
	)
	if err != nil || !exists {
		t.Fatalf("retained branch exists=%v err=%v", exists, err)
	}
	again, err := CleanupWorktreeWorker(context.Background(), WorktreeCleanupOptions{
		Local: LocalOptions{CWD: base}, WorktreeRoot: managed,
		AgentID: worker.AgentID, WorkerID: worker.ID,
		ExpectedRevision: worker.Revision,
	})
	if err != nil || !again.RetainedBranch {
		t.Fatalf("idempotent retained cleanup=%+v err=%v", again, err)
	}
}

func TestWorktreeCleanupRecoversInterruptedRemoval(t *testing.T) {
	base, managed := newWorktreeTestRepository(t)
	worker, err := PrepareWorktreeWorker(context.Background(), worktreeTestOptions(base, managed))
	if err != nil {
		t.Fatal(err)
	}
	key, err := readWorktreeOwnerKey(managed)
	if err != nil {
		t.Fatal(err)
	}
	worker.State = WorktreeCleaning
	worker.CleanupPolicy = "delete_branch"
	worker.Revision++
	worker.UpdatedAt = time.Now().UTC()
	if err := persistWorktreeTransition(
		LocalOptions{CWD: base}, managed, key, worker,
	); err != nil {
		t.Fatal(err)
	}
	worktreeTestGit(t, base, "worktree", "remove", worker.WorktreeRoot)

	result, err := CleanupWorktreeWorker(context.Background(), WorktreeCleanupOptions{
		Local: LocalOptions{CWD: base}, WorktreeRoot: managed,
		AgentID: worker.AgentID, WorkerID: worker.ID, ExpectedRevision: worker.Revision,
	})
	if err != nil || result.Worker.State != WorktreeRemoved || result.RetainedBranch {
		t.Fatalf("cleanup=%+v err=%v", result, err)
	}
	exists, err := (worktreeGitRunner{}).branchExists(
		context.Background(), base, worker.Branch,
	)
	if err != nil || exists {
		t.Fatalf("branch exists=%v err=%v", exists, err)
	}
}

func TestWorktreeCleanupReconcilesRemovedOwnerAfterStoreFailure(t *testing.T) {
	base, managed := newWorktreeTestRepository(t)
	worker, err := PrepareWorktreeWorker(context.Background(), worktreeTestOptions(base, managed))
	if err != nil {
		t.Fatal(err)
	}
	key, err := readWorktreeOwnerKey(managed)
	if err != nil {
		t.Fatal(err)
	}
	worker.State = WorktreeCleaning
	worker.CleanupPolicy = "delete_branch"
	worker.Revision++
	worker.UpdatedAt = time.Now().UTC()
	if err := persistWorktreeTransition(
		LocalOptions{CWD: base}, managed, key, worker,
	); err != nil {
		t.Fatal(err)
	}
	worktreeTestGit(t, base, "worktree", "remove", worker.WorktreeRoot)
	worktreeTestGit(t, base, "branch", "-d", worker.Branch)
	worker.State = WorktreeRemoved
	worker.Revision++
	worker.LeaseUntil = time.Time{}
	worker.UpdatedAt = time.Now().UTC()
	if err := writeWorktreeOwner(managed, key, worker); err != nil {
		t.Fatal(err)
	}

	result, err := CleanupWorktreeWorker(context.Background(), WorktreeCleanupOptions{
		Local: LocalOptions{CWD: base}, WorktreeRoot: managed,
		AgentID: worker.AgentID, WorkerID: worker.ID, ExpectedRevision: worker.Revision,
	})
	if err != nil || result.Worker.State != WorktreeRemoved {
		t.Fatalf("cleanup=%+v err=%v", result, err)
	}
	stored, err := latestStoredWorktree(
		LocalOptions{CWD: base}, base, worker.AgentID, worker.ID,
	)
	if err != nil || stored.Revision != worker.Revision || stored.State != WorktreeRemoved {
		t.Fatalf("stored=%+v err=%v", stored, err)
	}
}

func newWorktreeTestRepository(t *testing.T) (base, managed string) {
	t.Helper()
	root := t.TempDir()
	base = filepath.Join(root, "base")
	managed = filepath.Join(root, "workers")
	if err := os.Mkdir(base, 0o700); err != nil {
		t.Fatal(err)
	}
	worktreeTestGit(t, base, "init", "-b", "main")
	worktreeTestGit(t, base, "config", "user.name", "Anvil Test")
	worktreeTestGit(t, base, "config", "user.email", "anvil@example.invalid")
	if err := os.WriteFile(filepath.Join(base, ".gitignore"), []byte(".anvil/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "README.md"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	worktreeTestGit(t, base, "add", ".gitignore", "README.md")
	worktreeTestGit(t, base, "commit", "-m", "base")
	base, _ = canonicalExistingPath(base)
	managed, _ = filepath.Abs(managed)
	return base, managed
}

func worktreeTestOptions(base, managed string) WorktreePrepareOptions {
	return WorktreePrepareOptions{
		Local: LocalOptions{CWD: base}, WorktreeRoot: managed,
		AgentID: "agent-one", ArtifactID: "artifact-one", WorkItemID: "item-one",
		Attempt: 1, BaseRef: "HEAD", OwnerHostID: "host-one", MaxWorkers: 4,
	}
}

func worktreeTestGit(t *testing.T, cwd string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", cwd}, args...)...)
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func assertWorktreeErrorCode(t *testing.T, err error, code string) {
	t.Helper()
	var lifecycleErr *WorktreeLifecycleError
	if !errors.As(err, &lifecycleErr) || lifecycleErr.Code != code {
		t.Fatalf("err=%v code=%q want=%q", err, lifecycleErrCode(lifecycleErr), code)
	}
}

func lifecycleErrCode(err *WorktreeLifecycleError) string {
	if err == nil {
		return ""
	}
	return err.Code
}

func fixedWorktreeIDs(t *testing.T, digit string) func() {
	t.Helper()
	previous := worktreeIDGenerator
	worktreeIDGenerator = func(prefix string) (string, error) {
		return prefix + "-" + strings.Repeat(digit, 24), nil
	}
	return func() { worktreeIDGenerator = previous }
}
