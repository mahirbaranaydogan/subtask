package gitredesign

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zippoxer/subtask/internal/filelock"
	"github.com/zippoxer/subtask/pkg/git"
	"github.com/zippoxer/subtask/pkg/logging"
	"github.com/zippoxer/subtask/pkg/task"
	"github.com/zippoxer/subtask/pkg/task/history"
	taskmigrate "github.com/zippoxer/subtask/pkg/task/migrate"
)

// TaskSchemaVersion is the task schema version that indicates the git redesign migration
// has been applied (best-effort) and can be skipped on subsequent runs.
//
// v0.1.1 tasks commonly have schema=1 (schema1 history.jsonl). This migration upgrades
// them to schema=2 by backfilling missing git redesign fields.
const TaskSchemaVersion = 2

const repoDoneMarkerName = "gitredesign-v1.done"

// Ensure performs a best-effort, idempotent migration to support the git redesign:
// - Backfills missing base_commit in the most recent task.opened event.
// - Backfills frozen change stats in task.merged / task.closed events when missing.
//
// It is safe to call multiple times; if tasks are already migrated it becomes a no-op.
func Ensure(repoDir string) error {
	repoDir = strings.TrimSpace(repoDir)
	if repoDir == "" {
		return nil
	}
	repoDir = canonicalRepoDir(repoDir)

	paths := repoMigrationPaths(repoDir)
	if markerExists(paths.doneMarkerPath) {
		return nil
	}

	// Best-effort: if we can lock, do the scan/migration once per repo and persist a marker.
	// If we cannot lock or create runtime state, fall back to the legacy behavior (scan tasks
	// every time) rather than failing the CLI.
	if err := os.MkdirAll(paths.projectDir, 0o755); err == nil {
		lockFile, err := os.OpenFile(paths.lockPath, os.O_CREATE|os.O_RDWR, 0o644)
		if err == nil {
			defer func() { _ = lockFile.Close() }()
			if err := filelock.LockExclusive(lockFile); err == nil {
				defer func() { _ = filelock.Unlock(lockFile) }()

				if markerExists(paths.doneMarkerPath) {
					return nil
				}

				hadErrors, err := migrateAllTasks(repoDir)
				if err != nil {
					return err
				}
				if !hadErrors {
					if err := writeDoneMarker(paths.doneMarkerPath); err != nil {
						logging.Error("migrate", fmt.Sprintf("gitredesign write marker err=%v", err))
					}
				}
				return nil
			}
		}
	}

	// Fallback path: legacy behavior without persistent marker/locking.
	_, err := migrateAllTasks(repoDir)
	return err
}

type repoPaths struct {
	projectDir     string
	lockPath       string
	doneMarkerPath string
}

func repoMigrationPaths(repoDir string) repoPaths {
	projectDir := filepath.Join(task.ProjectsDir(), task.EscapePath(repoDir))
	return repoPaths{
		projectDir:     projectDir,
		lockPath:       filepath.Join(projectDir, "migrate.lock"),
		doneMarkerPath: filepath.Join(projectDir, "migrations", repoDoneMarkerName),
	}
}

func canonicalRepoDir(repoDir string) string {
	repoDir = filepath.Clean(strings.TrimSpace(repoDir))
	if repoDir == "" {
		return ""
	}
	if abs, err := filepath.Abs(repoDir); err == nil {
		repoDir = abs
	}
	return repoDir
}

func markerExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

func writeDoneMarker(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return nil
		}
		return err
	}
	defer func() { _ = f.Close() }()

	_, _ = f.WriteString(time.Now().UTC().Format(time.RFC3339Nano) + "\n")
	return nil
}

func migrateAllTasks(repoDir string) (bool, error) {
	taskNames, err := task.List()
	if err != nil {
		return true, err
	}
	if len(taskNames) == 0 {
		return false, nil
	}

	hadErrors := false
	for _, name := range taskNames {
		// Fast path: schema already indicates the redesign migration has been applied.
		// This avoids per-task locks and full history parses on every CLI command.
		t, err := task.Load(name)
		if err == nil && t != nil && t.Schema >= TaskSchemaVersion {
			continue
		}

		// Ensure schema/history exist (locks internally).
		if err := taskmigrate.EnsureSchema(name); err != nil {
			logging.Error("migrate", fmt.Sprintf("gitredesign ensure schema task=%s err=%v", name, err))
			hadErrors = true
			continue
		}
		if err := migrateTask(repoDir, name); err != nil {
			logging.Error("migrate", fmt.Sprintf("gitredesign task=%s err=%v", name, err))
			hadErrors = true
			continue
		}

		// Mark as migrated so subsequent runs can skip this task entirely.
		if err := bumpTaskSchema(name, TaskSchemaVersion); err != nil {
			logging.Error("migrate", fmt.Sprintf("gitredesign bump schema task=%s err=%v", name, err))
			hadErrors = true
		}
	}

	return hadErrors, nil
}

func bumpTaskSchema(taskName string, version int) error {
	return task.WithLock(taskName, func() error {
		t, err := task.Load(taskName)
		if err != nil || t == nil {
			return nil
		}
		if t.Schema >= version {
			return nil
		}
		t.Schema = version
		return t.Save()
	})
}

func migrateTask(repoDir, taskName string) error {
	t, err := task.Load(taskName)
	if err != nil {
		return nil
	}

	return task.WithLock(taskName, func() error {
		events, err := history.Read(taskName, history.ReadOptions{})
		if err != nil {
			return err
		}
		if len(events) == 0 {
			return nil
		}

		dirty := false

		openedIdx := lastIndexOfType(events, "task.opened")
		openedData := map[string]any{}
		if openedIdx >= 0 {
			_ = json.Unmarshal(events[openedIdx].Data, &openedData)

			if strings.TrimSpace(getString(openedData, "base_commit")) == "" {
				baseBranch := strings.TrimSpace(getString(openedData, "base_branch"))
				if baseBranch == "" {
					baseBranch = strings.TrimSpace(t.BaseBranch)
				}
				baseCommit := inferBaseCommit(repoDir, taskName, baseBranch)
				if baseCommit != "" {
					openedData["base_commit"] = baseCommit
					openedData["base_ref"] = baseBranch
					if b, err := json.Marshal(openedData); err == nil {
						events[openedIdx].Data = b
						dirty = true
					}
				}
			}
		}

		// Best-effort: backfill frozen stats for merged tasks when missing.
		mergedIdx := lastIndexOfType(events, "task.merged")
		if mergedIdx >= 0 {
			data := map[string]any{}
			_ = json.Unmarshal(events[mergedIdx].Data, &data)
			if _, ok := data["changes_added"]; !ok {
				commit := strings.TrimSpace(getString(data, "commit"))
				added, removed, frozenErr := inferFrozenStatsForMerge(repoDir, commit)
				if frozenErr != "" {
					data["frozen_error"] = frozenErr
				} else {
					data["changes_added"] = added
					data["changes_removed"] = removed
				}
				if b, err := json.Marshal(data); err == nil {
					events[mergedIdx].Data = b
					dirty = true
				}
			}
		}

		// Best-effort: backfill frozen stats for closed tasks when missing.
		closedIdx := lastIndexOfType(events, "task.closed")
		if closedIdx >= 0 {
			data := map[string]any{}
			_ = json.Unmarshal(events[closedIdx].Data, &data)
			if _, ok := data["changes_added"]; !ok {
				baseCommit := strings.TrimSpace(getString(openedData, "base_commit"))
				if baseCommit == "" {
					baseBranch := strings.TrimSpace(t.BaseBranch)
					if baseBranch == "" {
						baseBranch = strings.TrimSpace(getString(openedData, "base_branch"))
					}
					mb := inferBaseCommit(repoDir, taskName, baseBranch)
					if mb != "" {
						baseCommit = mb
					}
				}

				branchHead := ""
				if git.BranchExists(repoDir, taskName) {
					if out, err := git.Output(repoDir, "rev-parse", taskName); err == nil {
						branchHead = strings.TrimSpace(out)
					}
				}
				added, removed, commitCount, frozenErr := inferFrozenStatsForClose(repoDir, baseCommit, branchHead)
				data["base_branch"] = strings.TrimSpace(t.BaseBranch)
				data["base_commit"] = baseCommit
				data["branch_head"] = branchHead
				if frozenErr != "" {
					data["frozen_error"] = frozenErr
				} else {
					data["changes_added"] = added
					data["changes_removed"] = removed
					data["commit_count"] = commitCount
				}
				if b, err := json.Marshal(data); err == nil {
					events[closedIdx].Data = b
					dirty = true
				}
			}
		}

		if !dirty {
			return nil
		}

		// Legacy histories sometimes have zero timestamps for terminal events. If we let
		// WriteAllLocked normalize them, they'll become "now" and break recency ordering.
		//
		// Best-effort: derive a stable timestamp from git metadata (preferred) or nearby
		// history events (fallback).
		backfillZeroTimestampsBestEffort(repoDir, taskName, events)

		return history.WriteAllLocked(taskName, events)
	})
}

func lastIndexOfType(events []history.Event, typ string) int {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type == typ {
			return i
		}
	}
	return -1
}

func getString(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	s, _ := v.(string)
	return s
}

func backfillZeroTimestampsBestEffort(repoDir, taskName string, events []history.Event) {
	if repoDir == "" || taskName == "" || len(events) == 0 {
		return
	}

	for i := range events {
		if !events[i].TS.IsZero() {
			continue
		}

		switch events[i].Type {
		case "task.merged":
			var d struct {
				Commit string `json:"commit"`
			}
			_ = json.Unmarshal(events[i].Data, &d)
			if ts, ok := commitDateUTC(repoDir, strings.TrimSpace(d.Commit)); ok {
				events[i].TS = ts
				continue
			}
			events[i].TS = fallbackEventTimestamp(events, i)
		case "task.closed":
			// Prefer an explicit branch_head (present in schema2) or fall back to the
			// current task branch head when possible.
			var d struct {
				BranchHead string `json:"branch_head"`
			}
			_ = json.Unmarshal(events[i].Data, &d)
			head := strings.TrimSpace(d.BranchHead)
			if head == "" && git.BranchExists(repoDir, taskName) {
				if out, err := git.Output(repoDir, "rev-parse", taskName); err == nil {
					head = strings.TrimSpace(out)
				}
			}
			if ts, ok := commitDateUTC(repoDir, head); ok {
				events[i].TS = ts
				continue
			}
			events[i].TS = fallbackEventTimestamp(events, i)
		default:
			events[i].TS = fallbackEventTimestamp(events, i)
		}
	}
}

func commitDateUTC(repoDir, commit string) (time.Time, bool) {
	commit = strings.TrimSpace(commit)
	if repoDir == "" || commit == "" {
		return time.Time{}, false
	}
	if !git.CommitExists(repoDir, commit) {
		return time.Time{}, false
	}
	out, err := git.Output(repoDir, "show", "-s", "--format=%cI", commit)
	if err != nil {
		return time.Time{}, false
	}
	ts, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(out))
	if err != nil {
		return time.Time{}, false
	}
	return ts.UTC(), true
}

func fallbackEventTimestamp(events []history.Event, idx int) time.Time {
	// Prefer to keep this event after earlier ones in the file.
	for i := idx - 1; i >= 0; i-- {
		if !events[i].TS.IsZero() {
			return events[i].TS.Add(time.Nanosecond)
		}
	}
	for i := idx + 1; i < len(events); i++ {
		if !events[i].TS.IsZero() {
			return events[i].TS.Add(-time.Nanosecond)
		}
	}
	return time.Unix(0, 0).UTC()
}

func inferBaseCommit(repoDir, taskName, baseBranch string) string {
	taskName = strings.TrimSpace(taskName)
	baseBranch = strings.TrimSpace(baseBranch)
	if taskName == "" || baseBranch == "" {
		return ""
	}

	// Prefer merge-base when the branch exists (this matches "based on base HEAD at creation time").
	if git.BranchExists(repoDir, taskName) && git.BranchExists(repoDir, baseBranch) {
		if mb, err := git.Output(repoDir, "merge-base", taskName, baseBranch); err == nil {
			return strings.TrimSpace(mb)
		}
	}

	// Draft-only tasks may have no branch yet; fall back to base branch HEAD.
	if git.BranchExists(repoDir, baseBranch) {
		if head, err := git.Output(repoDir, "rev-parse", baseBranch); err == nil {
			return strings.TrimSpace(head)
		}
	}

	return ""
}

func inferFrozenStatsForMerge(repoDir, mergedCommit string) (int, int, string) {
	mergedCommit = strings.TrimSpace(mergedCommit)
	if mergedCommit == "" {
		return 0, 0, "cannot compute frozen stats (missing merge commit)"
	}
	if !git.CommitExists(repoDir, mergedCommit) {
		return 0, 0, fmt.Sprintf("cannot compute frozen stats (missing merge commit %s)", mergedCommit)
	}
	parents, err := git.Output(repoDir, "show", "-s", "--format=%P", mergedCommit)
	if err != nil {
		return 0, 0, fmt.Sprintf("cannot compute frozen stats (failed to read parents): %v", err)
	}
	parent := ""
	for _, p := range strings.Fields(parents) {
		parent = strings.TrimSpace(p)
		break
	}
	if parent == "" {
		return 0, 0, "cannot compute frozen stats (no parent commit)"
	}
	if !git.CommitExists(repoDir, parent) {
		return 0, 0, fmt.Sprintf("cannot compute frozen stats (missing parent commit %s)", parent)
	}
	added, removed, err := git.DiffStatRange(repoDir, parent, mergedCommit)
	if err != nil {
		return 0, 0, fmt.Sprintf("cannot compute frozen stats: %v", err)
	}
	return added, removed, ""
}

func inferFrozenStatsForClose(repoDir, baseCommit, branchHead string) (int, int, int, string) {
	baseCommit = strings.TrimSpace(baseCommit)
	branchHead = strings.TrimSpace(branchHead)
	if baseCommit == "" || branchHead == "" {
		return 0, 0, 0, fmt.Sprintf("cannot compute frozen stats (missing base_commit=%t branch_head=%t)", baseCommit == "", branchHead == "")
	}
	if !git.CommitExists(repoDir, baseCommit) {
		return 0, 0, 0, fmt.Sprintf("cannot compute frozen stats (missing base_commit %s)", baseCommit)
	}
	if !git.CommitExists(repoDir, branchHead) {
		return 0, 0, 0, fmt.Sprintf("cannot compute frozen stats (missing branch_head %s)", branchHead)
	}
	added, removed, err := git.DiffStatRange(repoDir, baseCommit, branchHead)
	if err != nil {
		return 0, 0, 0, fmt.Sprintf("cannot compute frozen stats: %v", err)
	}
	commitCount, err := git.RevListCount(repoDir, baseCommit, branchHead)
	if err != nil {
		return 0, 0, 0, fmt.Sprintf("cannot compute commit_count: %v", err)
	}
	return added, removed, commitCount, ""
}
