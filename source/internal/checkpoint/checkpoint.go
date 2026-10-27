// Package checkpoint records what a run's tool calls are about to change, so
// the workspace can be put back afterwards.
//
// A checkpoint is per run, not per call. The first time a run touches a path
// that path's pre-image is written; later calls in the same run that touch it
// again are no-ops. That is what "before this run" means: restoring gives the
// workspace as it stood when the run began, not as it stood before the last
// write — which would leave the run's earlier edits behind.
//
// Capture is best-effort by design and its failure is not the call's failure.
// A backup that could not be written must not turn a successful edit into a
// reported error; the alternative is a tool that refuses to work because a
// directory is read-only, which is worse than a gap in the undo history.
//
// What is not captured: a shell command's redirection writes a file no
// argument names, so there is nothing to enumerate. This is the same boundary
// the rule matcher has, and it is stated in the UI rather than papered over.
package checkpoint

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// DefaultKeep and DefaultMaxAge bound an agent's checkpoint history. They are
// the defaults the public options document; zero there means these.
const (
	DefaultKeep   = 100
	DefaultMaxAge = 30 * 24 * time.Hour
)

// Entry is one path a run touched, as it stood before the run.
type Entry struct {
	// Path is workspace-relative and slash-separated, so a manifest moves with
	// the workspace rather than with the machine that wrote it.
	Path string `json:"path"`
	// Existed false is a create: restoring deletes the file.
	Existed bool  `json:"existed"`
	Size    int64 `json:"size,omitempty"`
	// SHA256 is of the captured bytes. It is a diagnostic, not an integrity
	// check that restore refuses on: a checkpoint is a convenience, and a hash
	// mismatch should not make an undo impossible.
	SHA256 string `json:"sha256,omitempty"`
	// PostSHA256 is of the file as the run left it, recorded by Finish when the
	// run ends. Restore refuses to overwrite a file whose current bytes differ
	// from it: the user changed it after the run. Empty means no post-state was
	// recorded (a checkpoint from before this field), and Restore warns instead.
	PostSHA256 string `json:"post_sha256,omitempty"`
	// AgentID and ChildRunID name the child that captured the path when it was not the
	// run's own agent (0.50+). Empty means the root agent captured it, which is every
	// entry written before trees existed. They are a record, not a key: a restore of
	// the root run puts back every entry whoever captured it, and the child's run id is
	// kept so a per-child restore remains possible later.
	AgentID    string `json:"agent_id,omitempty"`
	ChildRunID string `json:"child_run_id,omitempty"`
}

// Manifest is one run's record, stored beside its captured files.
type Manifest struct {
	AgentID   string    `json:"agent_id"`
	RunID     string    `json:"run_id"`
	CreatedAt time.Time `json:"created_at"`
	Files     []Entry   `json:"files"`
}

// Run is a manifest plus where it lives.
type Run struct {
	Manifest
	// Dir is the run's directory, absolute.
	Dir string `json:"-"`
}

// Restored is one path put back by Restore.
type Restored struct {
	Path string `json:"path"`
	// Deleted is true when the file did not exist before the run.
	Deleted bool `json:"deleted"`
	// Skipped is true when the file was left as it is because the user changed it
	// after the run. Reason says why.
	Skipped bool   `json:"skipped,omitempty"`
	Reason  string `json:"reason,omitempty"`
	// Warning is set when the file was restored, but no post-run state was
	// recorded, so restore could not tell whether the user had changed it.
	Warning string `json:"warning,omitempty"`
}

var errUnsafe = errors.New("checkpoint: unsafe name")

// safeName refuses a name that could leave the checkpoint root. Agent and run
// ids reach this from the wire, so they are checked rather than trusted.
func safeName(id string) error {
	if id == "" {
		return fmt.Errorf("%w: empty name", errUnsafe)
	}
	if id == "." || id == ".." || strings.ContainsAny(id, `/\:`+string(os.PathSeparator)) {
		return fmt.Errorf("%w: %q", errUnsafe, id)
	}
	return nil
}

func runDir(root, agentID, runID string) (string, error) {
	if err := safeName(agentID); err != nil {
		return "", err
	}
	if err := safeName(runID); err != nil {
		return "", err
	}
	return filepath.Join(root, agentID, runID), nil
}

func manifestPath(dir string) string { return filepath.Join(dir, "manifest.json") }

func loadManifest(dir, agentID, runID string) (Manifest, error) {
	data, err := os.ReadFile(manifestPath(dir))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Manifest{AgentID: agentID, RunID: runID, CreatedAt: time.Now().UTC()}, nil
		}
		return Manifest{}, fmt.Errorf("checkpoint: read manifest: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return Manifest{}, fmt.Errorf("checkpoint: parse manifest: %w", err)
	}
	if m.AgentID == "" {
		m.AgentID = agentID
	}
	if m.RunID == "" {
		m.RunID = runID
	}
	return m, nil
}

func saveManifest(dir string, m Manifest) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("checkpoint: create run dir: %w", err)
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("checkpoint: encode manifest: %w", err)
	}
	// Written beside the manifest and renamed over it, so a crash mid-write
	// cannot leave a manifest that names files it does not have.
	tmp := manifestPath(dir) + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("checkpoint: write manifest: %w", err)
	}
	if err := os.Rename(tmp, manifestPath(dir)); err != nil {
		return fmt.Errorf("checkpoint: install manifest: %w", err)
	}
	return nil
}

// Capture records absPath as it stands now, unless this run already captured
// it. relPath is workspace-relative and slash-separated.
//
// A directory is not captured: the tools that can change the workspace write
// files, and restoring a directory's listing would be a different feature.
func Capture(root, agentID, runID, relPath, absPath string) error {
	return capture(root, agentID, runID, "", "", relPath, absPath)
}

// CaptureFor is Capture for a child agent in a tree: the path is filed under the root's
// run (rootAgentID, rootRunID), so one restore of that run undoes the whole tree, and the
// entry records which child captured it and in which of its own runs. The first
// pre-image of a path is still the tree's pre-image, whichever agent reached it first.
func CaptureFor(root, rootAgentID, rootRunID, childAgentID, childRunID, relPath, absPath string) error {
	if err := safeName(childAgentID); err != nil {
		return err
	}
	if err := safeName(childRunID); err != nil {
		return err
	}
	return capture(root, rootAgentID, rootRunID, childAgentID, childRunID, relPath, absPath)
}

// manifestLocks serialises the read-modify-write of one run's manifest. A tree's Workers
// run in parallel in one process and all file into the root's run, so two captures of
// different paths would otherwise read the same manifest and the later write would drop
// the earlier entry; on Windows the rename also fails while another writer holds the file.
var manifestLocks sync.Map // run directory -> *sync.Mutex

func lockRun(dir string) func() {
	m, _ := manifestLocks.LoadOrStore(dir, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

func capture(root, agentID, runID, childAgentID, childRunID, relPath, absPath string) error {
	dir, err := runDir(root, agentID, runID)
	if err != nil {
		return err
	}
	defer lockRun(dir)()
	relPath = filepath.ToSlash(relPath)
	if relPath == "" || relPath == "." || strings.HasPrefix(relPath, "../") {
		return fmt.Errorf("%w: %q", errUnsafe, relPath)
	}

	m, err := loadManifest(dir, agentID, runID)
	if err != nil {
		return err
	}
	for _, entry := range m.Files {
		if entry.Path == relPath {
			// The first pre-image is the run's pre-image. A later capture would
			// record this run's own earlier write as the "before".
			return nil
		}
	}

	info, err := os.Stat(absPath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("checkpoint: stat %s: %w", relPath, err)
		}
		m.Files = append(m.Files, Entry{Path: relPath, Existed: false, AgentID: childAgentID, ChildRunID: childRunID})
		return saveManifest(dir, m)
	}
	if info.IsDir() {
		return nil
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		return fmt.Errorf("checkpoint: read %s: %w", relPath, err)
	}
	dest := filepath.Join(dir, "files", filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("checkpoint: create dir for %s: %w", relPath, err)
	}
	if err := os.WriteFile(dest, data, 0o644); err != nil {
		return fmt.Errorf("checkpoint: write %s: %w", relPath, err)
	}
	sum := sha256.Sum256(data)
	m.Files = append(m.Files, Entry{
		Path:    relPath,
		Existed: true,
		Size:    int64(len(data)),
		SHA256:  hex.EncodeToString(sum[:]),

		AgentID:    childAgentID,
		ChildRunID: childRunID,
	})
	return saveManifest(dir, m)
}

// List returns an agent's runs, newest first. A run without a readable
// manifest is skipped rather than failing the listing: one damaged directory
// must not hide every other one.
func List(root, agentID string) ([]Run, error) {
	if err := safeName(agentID); err != nil {
		return nil, err
	}
	children, err := os.ReadDir(filepath.Join(root, agentID))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("checkpoint: list: %w", err)
	}
	runs := make([]Run, 0, len(children))
	for _, child := range children {
		if !child.IsDir() {
			continue
		}
		dir := filepath.Join(root, agentID, child.Name())
		data, err := os.ReadFile(manifestPath(dir))
		if err != nil {
			continue
		}
		var m Manifest
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		if m.AgentID == "" {
			m.AgentID = agentID
		}
		if m.RunID == "" {
			m.RunID = child.Name()
		}
		runs = append(runs, Run{Manifest: m, Dir: dir})
	}
	sort.Slice(runs, func(i, j int) bool {
		if runs[i].CreatedAt.Equal(runs[j].CreatedAt) {
			return runs[i].RunID > runs[j].RunID
		}
		return runs[i].CreatedAt.After(runs[j].CreatedAt)
	})
	return runs, nil
}

// Get returns one run, or os.ErrNotExist when it was never captured.
func Get(root, agentID, runID string) (Run, error) {
	dir, err := runDir(root, agentID, runID)
	if err != nil {
		return Run{}, err
	}
	m, err := loadManifest(dir, agentID, runID)
	if err != nil {
		return Run{}, err
	}
	if _, err := os.Stat(manifestPath(dir)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Run{}, os.ErrNotExist
		}
		return Run{}, err
	}
	return Run{Manifest: m, Dir: dir}, nil
}

// Restore puts every path a run captured back the way it was.
//
// resolve is the caller's sandbox: it maps a workspace-relative path to an
// absolute one and refuses anything outside the workspace. This package never
// trusts a manifest path on its own, because a manifest is a file on disk that
// something else could have edited. Restoring stops at the first path that
// cannot be resolved or written; a half-applied restore is reported as an
// error rather than counted as success.
func Restore(root, agentID, runID string, resolve func(rel string) (string, error)) ([]Restored, error) {
	run, err := Get(root, agentID, runID)
	if err != nil {
		return nil, err
	}
	restored := make([]Restored, 0, len(run.Files))
	for _, entry := range run.Files {
		if entry.Path == "" {
			continue
		}
		abs, err := resolve(entry.Path)
		if err != nil {
			return restored, fmt.Errorf("checkpoint: restore %s: %w", entry.Path, err)
		}
		if !entry.Existed {
			if err := os.Remove(abs); err != nil && !errors.Is(err, os.ErrNotExist) {
				return restored, fmt.Errorf("checkpoint: delete %s: %w", entry.Path, err)
			}
			restored = append(restored, Restored{Path: entry.Path, Deleted: true})
			continue
		}
		// Decide before touching the file. If the run recorded what it left behind,
		// a current file that differs was changed by someone after the run, and it
		// is left alone so the user's edit is not overwritten.
		current, cerr := os.ReadFile(abs)
		exists := cerr == nil
		if exists && entry.PostSHA256 != "" {
			sum := sha256.Sum256(current)
			if hex.EncodeToString(sum[:]) != entry.PostSHA256 {
				restored = append(restored, Restored{
					Path:    entry.Path,
					Skipped: true,
					Reason:  "changed after the run",
				})
				continue
			}
		}
		data, err := os.ReadFile(filepath.Join(run.Dir, "files", filepath.FromSlash(entry.Path)))
		if err != nil {
			return restored, fmt.Errorf("checkpoint: read saved %s: %w", entry.Path, err)
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return restored, fmt.Errorf("checkpoint: create dir for %s: %w", entry.Path, err)
		}
		if err := os.WriteFile(abs, data, 0o644); err != nil {
			return restored, fmt.Errorf("checkpoint: write %s: %w", entry.Path, err)
		}
		item := Restored{Path: entry.Path}
		if exists && entry.PostSHA256 == "" {
			// No post-run state was recorded, so there is no way to tell whether
			// the user changed this file. It is restored, and the caller is told.
			item.Warning = "no post-run state was recorded; the file was restored without checking for later edits"
		}
		restored = append(restored, item)
	}
	return restored, nil
}

// Finish records the post-run state of every file a run captured: the hash of
// what each file holds now, after the run's writes. resolve maps a manifest path
// to its absolute path, the same way Restore does, so both agree on where a file
// is. A file the run deleted, or one that is no longer a regular file, records
// nothing and is restored without the check.
func Finish(root, agentID, runID string, resolve func(rel string) (string, error)) error {
	dir, err := runDir(root, agentID, runID)
	if err != nil {
		return err
	}
	defer lockRun(dir)()
	m, err := loadManifest(dir, agentID, runID)
	if err != nil {
		return err
	}
	for i := range m.Files {
		entry := &m.Files[i]
		if !entry.Existed || entry.Path == "" {
			continue
		}
		abs, err := resolve(entry.Path)
		if err != nil {
			return fmt.Errorf("checkpoint: finish %s: %w", entry.Path, err)
		}
		info, err := os.Stat(abs)
		if err != nil || info.IsDir() {
			continue
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			return fmt.Errorf("checkpoint: finish read %s: %w", entry.Path, err)
		}
		sum := sha256.Sum256(data)
		entry.PostSHA256 = hex.EncodeToString(sum[:])
	}
	return saveManifest(dir, m)
}

// Prune keeps the newest keep runs and deletes runs older than maxAge. Zero
// keep or maxAge means the package defaults; a negative one disables that
// bound. Only this agent's directory is touched.
func Prune(root, agentID string, keep int, maxAge time.Duration) error {
	if err := safeName(agentID); err != nil {
		return err
	}
	if keep == 0 {
		keep = DefaultKeep
	}
	if maxAge == 0 {
		maxAge = DefaultMaxAge
	}
	runs, err := List(root, agentID)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	var oldest time.Time
	if maxAge > 0 {
		oldest = now.Add(-maxAge)
	}
	kept := 0
	for _, run := range runs {
		drop := false
		if keep > 0 && kept >= keep {
			drop = true
		}
		if !oldest.IsZero() && run.CreatedAt.Before(oldest) {
			drop = true
		}
		if !drop {
			kept++
			continue
		}
		if err := os.RemoveAll(run.Dir); err != nil {
			return fmt.Errorf("checkpoint: prune %s: %w", run.RunID, err)
		}
	}
	return nil
}

// Remove deletes every checkpoint an agent has. It is what forgetting a
// session means for its undo history; missing is not an error.
func Remove(root, agentID string) error {
	if err := safeName(agentID); err != nil {
		return err
	}
	return os.RemoveAll(filepath.Join(root, agentID))
}
