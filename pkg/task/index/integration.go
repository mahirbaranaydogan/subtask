package index

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/zippoxer/subtask/pkg/git"
	"github.com/zippoxer/subtask/pkg/logging"
	"github.com/zippoxer/subtask/pkg/task"
	"github.com/zippoxer/subtask/pkg/task/history"
)

type integrationTask struct {
	name       string
	baseBranch string
	taskStatus task.TaskStatus

	lastBranchHead string
	integrated     string

	integratedBranchHead string
}

type integrationUpdate struct {
	name string

	setLastHead bool
	lastHead    sql.NullString

	setIntegrated bool
	reason        sql.NullString
	branchHead    sql.NullString
	targetHead    sql.NullString
	checkedAtNS   sql.NullInt64
}

func (i *Index) refreshIntegration(ctx context.Context, p GitPolicy) error {
	if !p.IncludeIntegration {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	debug := logging.DebugEnabled()
	var start time.Time
	if debug {
		start = time.Now()
		logging.Debug("integration", fmt.Sprintf("start mode=%s tasks=%d", gitModeString(p.Mode), len(p.Tasks)))
	}

	// Load tasks (from DB) and prior snapshot.
	var step time.Time
	if debug {
		step = time.Now()
	}
	tasks, err := i.integrationTasks(ctx)
	if err != nil {
		return err
	}
	if debug {
		logging.Debug("integration", fmt.Sprintf("integrationTasks n=%d (%s)", len(tasks), time.Since(step).Round(time.Millisecond)))
		step = time.Now()
	}
	prevSnap, err := i.loadRefsSnapshot(ctx)
	if err != nil {
		return err
	}
	if debug {
		logging.Debug("integration", fmt.Sprintf("loadRefsSnapshot ok hasSnapshot=%t (%s)", strings.TrimSpace(prevSnap.Hash) != "", time.Since(step).Round(time.Millisecond)))
	}

	// Build a repo-wide view of refs (single git call), then compute a stable snapshot
	// for the refs we care about.
	if debug {
		step = time.Now()
	}
	allRefs, err := git.ListRefs(".", "refs/heads", "refs/remotes/origin")
	if err != nil {
		return err
	}
	nextSnap, desiredRefs := buildRefsSnapshot(tasks, allRefs)
	if debug {
		logging.Debug("integration", fmt.Sprintf("git.ListRefs refs=%d desiredRefs=%d (%s)", len(allRefs), len(desiredRefs), time.Since(step).Round(time.Millisecond)))
	}

	// Decide whether to run a repair pass.
	forceTasks := p.Mode == GitTasks && len(p.Tasks) > 0
	noSnapshot := prevSnap.Hash == ""
	snapshotMismatch := prevSnap.Hash != "" && prevSnap.Hash != nextSnap.Hash

	if !forceTasks && !snapshotMismatch && !noSnapshot {
		// Snapshot matches and we're not being asked to recompute a specific task.
		// Keep list/show fast.
		if debug {
			logging.Debug("integration", fmt.Sprintf("skip snapshot match (%s)", time.Since(start).Round(time.Millisecond)))
		}
		return nil
	}

	repairPass := noSnapshot && !forceTasks
	if debug {
		logging.Debug("integration", fmt.Sprintf("snapshot noSnapshot=%t mismatch=%t forceTasks=%t", noSnapshot, snapshotMismatch, forceTasks))
	}
	if forceTasks && snapshotMismatch {
		// If we're being asked to refresh a specific task, we still must not "paper over"
		// unrelated external ref changes by blindly updating the snapshot.
		//
		// If the snapshot changed only due to the requested task refs (and its base refs),
		// proceed with a targeted refresh. Otherwise, fall back to a repair pass.
		prevRefs, ok := parseRefsSnapshotJSON(prevSnap.JSON)
		if !ok {
			repairPass = true
		} else {
			allowedRefs := allowedRefsForForcedTasks(tasks, p.Tasks)
			for _, ref := range desiredRefs {
				if _, ok := allowedRefs[ref]; ok {
					continue
				}
				if strings.TrimSpace(prevRefs[ref]) != strings.TrimSpace(nextSnap.Refs[ref]) {
					repairPass = true
					break
				}
			}
		}
	}

	var targetTasks []integrationTask
	if repairPass {
		// Repair pass: recompute integration for all tasks that are not durable-merged.
		//
		// This is the "manual changes happened" path (external merges, force-pushes, etc.).
		// It can be slower, but it guarantees correctness (including clearing stale
		// integration results).
		for _, t := range tasks {
			if t.taskStatus == task.TaskStatusMerged {
				continue
			}
			targetTasks = append(targetTasks, t)
		}
	} else if forceTasks {
		allow := make(map[string]struct{}, len(p.Tasks))
		for _, n := range p.Tasks {
			allow[n] = struct{}{}
		}
		for _, t := range tasks {
			if _, ok := allow[t.name]; ok {
				targetTasks = append(targetTasks, t)
			}
		}
	} else if snapshotMismatch {
		prevRefs, ok := parseRefsSnapshotJSON(prevSnap.JSON)
		if !ok {
			repairPass = true
			for _, t := range tasks {
				if t.taskStatus == task.TaskStatusMerged {
					continue
				}
				targetTasks = append(targetTasks, t)
			}
		} else {
			// Snapshot changed: only recompute tasks whose relevant refs changed.
			//
			// - task branch head changed -> recompute that task.
			// - base branch (local or origin) changed -> recompute all tasks using that base.
			changedTaskNames := make(map[string]struct{})
			changedBaseBranches := make(map[string]struct{})

			for _, ref := range desiredRefs {
				prev := strings.TrimSpace(prevRefs[ref])
				next := strings.TrimSpace(nextSnap.Refs[ref])
				if prev == next {
					continue
				}

				if strings.HasPrefix(ref, "refs/heads/") {
					name := strings.TrimPrefix(ref, "refs/heads/")
					if strings.TrimSpace(name) != "" {
						changedTaskNames[name] = struct{}{}
						changedBaseBranches[name] = struct{}{}
					}
					continue
				}
				if strings.HasPrefix(ref, "refs/remotes/origin/") {
					base := strings.TrimPrefix(ref, "refs/remotes/origin/")
					if strings.TrimSpace(base) != "" {
						changedBaseBranches[base] = struct{}{}
					}
					continue
				}
			}

			seen := make(map[string]struct{})
			for _, t := range tasks {
				if t.taskStatus == task.TaskStatusMerged {
					continue
				}

				_, taskChanged := changedTaskNames[t.name]
				base := strings.TrimSpace(t.baseBranch)
				_, baseChanged := changedBaseBranches[base]

				if !taskChanged && !baseChanged {
					continue
				}
				if _, ok := seen[t.name]; ok {
					continue
				}
				seen[t.name] = struct{}{}
				targetTasks = append(targetTasks, t)
			}

			// The snapshot may include refs for tasks that no longer exist in the DB
			// (e.g. after deleting a task). In that case there can be no target tasks.
			// Still persist the updated snapshot so list/show stays fast.
			if len(targetTasks) == 0 {
				if debug {
					logging.Debug("integration", fmt.Sprintf("snapshot updated (no target tasks) (%s)", time.Since(start).Round(time.Millisecond)))
				}
				return i.persistSnapshotOnly(ctx, nextSnap)
			}
		}
	}
	if debug {
		reason := "targeted"
		if repairPass {
			reason = "repair-pass"
		} else if forceTasks {
			reason = "force-tasks"
		} else if snapshotMismatch {
			reason = "snapshot-diff"
		}
		logging.Debug("integration", fmt.Sprintf("targetTasks n=%d reason=%s", len(targetTasks), reason))
	}

	// Group by base branch to amortize target tree lookups.
	type targetInfo struct {
		refName string
		headSHA string
		treeSHA string
	}
	targetByBase := make(map[string]targetInfo)
	baseBranches := make([]string, 0, len(targetTasks))
	seenBase := make(map[string]struct{})
	for _, t := range targetTasks {
		b := strings.TrimSpace(t.baseBranch)
		if b == "" {
			continue
		}
		if _, ok := seenBase[b]; ok {
			continue
		}
		seenBase[b] = struct{}{}
		baseBranches = append(baseBranches, b)
	}
	sort.Strings(baseBranches)

	if debug {
		step = time.Now()
	}
	for _, base := range baseBranches {
		ref := git.EffectiveTarget(".", base)
		refName := refToFullRefName(ref)
		head := strings.TrimSpace(nextSnap.Refs[refName])
		if head == "" {
			// Fallback: try resolving directly.
			h, err := git.Output(".", "rev-parse", ref)
			if err != nil {
				continue
			}
			head = strings.TrimSpace(h)
		}
		tree, err := git.Output(".", "rev-parse", ref+"^{tree}")
		if err != nil {
			continue
		}
		targetByBase[base] = targetInfo{refName: refName, headSHA: head, treeSHA: strings.TrimSpace(tree)}
	}
	if debug {
		logging.Debug("integration", fmt.Sprintf("baseBranches n=%d (%s)", len(baseBranches), time.Since(step).Round(time.Millisecond)))
	}

	nowNS := i.now().UnixNano()
	now := i.now().UTC()
	updates := make([]integrationUpdate, 0, len(targetTasks))
	var closedToMerged []integrationUpdate

	// Precompute current branch heads for all tasks from desired refs.
	for _, t := range targetTasks {
		branchRef := "refs/heads/" + t.name
		head := strings.TrimSpace(nextSnap.Refs[branchRef])
		if head == "" && strings.TrimSpace(t.lastBranchHead) != "" {
			head = strings.TrimSpace(t.lastBranchHead)
		}

		u := integrationUpdate{name: t.name}
		if strings.TrimSpace(nextSnap.Refs[branchRef]) != "" {
			u.setLastHead = true
			u.lastHead = sql.NullString{String: head, Valid: head != ""}
		}

		if head == "" {
			// No known head: do not modify integration status.
			updates = append(updates, u)
			continue
		}

		ti, ok := targetByBase[strings.TrimSpace(t.baseBranch)]
		if !ok || ti.headSHA == "" || ti.treeSHA == "" {
			// No known base head/tree: do not modify integration status.
			updates = append(updates, u)
			continue
		}

		// 1) Ancestor check (guarantee for history-preserving merges).
		if git.RunQuiet(".", "merge-base", "--is-ancestor", head, ti.headSHA) == nil {
			u.setIntegrated = true
			u.reason = sql.NullString{String: string(git.IntegratedAncestor), Valid: true}
			u.branchHead = sql.NullString{String: head, Valid: true}
			u.targetHead = sql.NullString{String: ti.headSHA, Valid: true}
			u.checkedAtNS = sql.NullInt64{Int64: nowNS, Valid: true}
			if t.taskStatus == task.TaskStatusClosed {
				closedToMerged = append(closedToMerged, u)
			}
			updates = append(updates, u)
			continue
		}

		// 2) No-op merge check (guarantee for content integration).
		mergeTree, err := git.Output(".", "merge-tree", "--write-tree", ti.headSHA, head)
		if err == nil && strings.TrimSpace(mergeTree) == ti.treeSHA {
			u.setIntegrated = true
			u.reason = sql.NullString{String: string(git.IntegratedMergeAddsNothing), Valid: true}
			u.branchHead = sql.NullString{String: head, Valid: true}
			u.targetHead = sql.NullString{String: ti.headSHA, Valid: true}
			u.checkedAtNS = sql.NullInt64{Int64: nowNS, Valid: true}
			if t.taskStatus == task.TaskStatusClosed {
				closedToMerged = append(closedToMerged, u)
			}
		} else if err == nil && strings.TrimSpace(t.integrated) != "" {
			// We have enough info to decide "not integrated". Clear any stale cached integration.
			u.setIntegrated = true
			u.reason = sql.NullString{Valid: false}
			u.branchHead = sql.NullString{Valid: false}
			u.targetHead = sql.NullString{Valid: false}
			u.checkedAtNS = sql.NullInt64{Valid: false}
		}

		updates = append(updates, u)
	}

	// Persist updates + snapshot in one short transaction.
	tx, err := i.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("index integration refresh: begin tx: %w", err)
	}
	defer tx.Rollback()

	if err := upsertIntegrationUpdates(ctx, tx, updates); err != nil {
		return err
	}
	if err := saveRefsSnapshot(ctx, tx, nextSnap.Hash, nextSnap.JSON, i.now()); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("index integration refresh: commit: %w", err)
	}

	if err := i.promoteClosedTasksToMerged(closedToMerged, now); err != nil {
		return err
	}

	if debug {
		logging.Debug("integration", fmt.Sprintf("done updates=%d (%s)", len(updates), time.Since(start).Round(time.Millisecond)))
	}
	return nil
}

func (i *Index) promoteClosedTasksToMerged(updates []integrationUpdate, now time.Time) error {
	if len(updates) == 0 {
		return nil
	}

	for _, u := range updates {
		if !u.setIntegrated || !u.reason.Valid || !u.targetHead.Valid {
			continue
		}

		taskName := strings.TrimSpace(u.name)
		if taskName == "" {
			continue
		}

		locked, err := task.TryWithLock(taskName, func() error {
			tail, err := history.Tail(taskName)
			if err != nil {
				return err
			}
			if tail.TaskStatus != task.TaskStatusClosed {
				return nil
			}
			// Guardrail: don't auto-promote "merged" if the last worker run is still in-flight
			// (or was killed mid-run without a worker.finished event).
			if !tail.RunningSince.IsZero() {
				return nil
			}

			// Guardrail: if the task branch never advanced beyond the recorded base commit,
			// treat it as "no commits" and don't mark as merged via detection.
			if tail.BaseCommit != "" && u.branchHead.Valid &&
				strings.TrimSpace(u.branchHead.String) == strings.TrimSpace(tail.BaseCommit) {
				return nil
			}

			data, _ := json.Marshal(map[string]any{
				"commit":            strings.TrimSpace(u.targetHead.String),
				"into":              strings.TrimSpace(tail.BaseBranch),
				"branch":            taskName,
				"via":               "detected",
				"integrated_reason": strings.TrimSpace(u.reason.String),
				"branch_head":       strings.TrimSpace(u.branchHead.String),
				"target_head":       strings.TrimSpace(u.targetHead.String),
			})
			_ = history.AppendLocked(taskName, history.Event{
				Type: "task.merged",
				Data: data,
				TS:   now,
			})
			return nil
		})
		if err != nil {
			return err
		}
		if !locked {
			// Best-effort: task is busy; we'll retry on the next refresh.
			continue
		}
	}

	return nil
}

func (i *Index) persistSnapshotOnly(ctx context.Context, snap computedSnapshot) error {
	tx, err := i.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("index snapshot: begin tx: %w", err)
	}
	defer tx.Rollback()
	if err := saveRefsSnapshot(ctx, tx, snap.Hash, snap.JSON, i.now()); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("index snapshot: commit: %w", err)
	}
	return nil
}

func upsertIntegrationUpdates(ctx context.Context, tx *sql.Tx, updates []integrationUpdate) error {
	if len(updates) == 0 {
		return nil
	}
	stmt, err := tx.PrepareContext(ctx, `
UPDATE tasks SET
	git_last_branch_head = CASE WHEN ? THEN ? ELSE git_last_branch_head END,
	git_integrated_reason = CASE WHEN ? THEN ? ELSE git_integrated_reason END,
	git_integrated_branch_head = CASE WHEN ? THEN ? ELSE git_integrated_branch_head END,
	git_integrated_target_head = CASE WHEN ? THEN ? ELSE git_integrated_target_head END,
	git_integrated_checked_at_ns = CASE WHEN ? THEN ? ELSE git_integrated_checked_at_ns END
WHERE name = ?;`)
	if err != nil {
		return fmt.Errorf("index integration refresh: prepare update: %w", err)
	}
	defer stmt.Close()

	for _, u := range updates {
		if _, err := stmt.ExecContext(ctx,
			boolToInt(u.setLastHead),
			nullableNullString(u.lastHead),
			boolToInt(u.setIntegrated),
			nullableNullString(u.reason),
			boolToInt(u.setIntegrated),
			nullableNullString(u.branchHead),
			boolToInt(u.setIntegrated),
			nullableNullString(u.targetHead),
			boolToInt(u.setIntegrated),
			nullableNullInt64(u.checkedAtNS),
			u.name,
		); err != nil {
			return fmt.Errorf("index integration refresh: update %q: %w", u.name, err)
		}
	}
	return nil
}

func nullableNullString(s sql.NullString) any {
	if !s.Valid {
		return nil
	}
	return s.String
}

func nullableNullInt64(n sql.NullInt64) any {
	if !n.Valid {
		return nil
	}
	return n.Int64
}

func (i *Index) integrationTasks(ctx context.Context) ([]integrationTask, error) {
	rows, err := i.db.QueryContext(ctx, `
SELECT name, base_branch, task_status, git_last_branch_head, git_integrated_reason, git_integrated_branch_head
FROM tasks;`)
	if err != nil {
		return nil, fmt.Errorf("index integration refresh: query tasks: %w", err)
	}
	defer rows.Close()

	var out []integrationTask
	for rows.Next() {
		var (
			t              integrationTask
			ts             string
			lastHead       sql.NullString
			integrated     sql.NullString
			integratedHead sql.NullString
		)
		if err := rows.Scan(&t.name, &t.baseBranch, &ts, &lastHead, &integrated, &integratedHead); err != nil {
			return nil, fmt.Errorf("index integration refresh: scan task: %w", err)
		}
		t.taskStatus = task.TaskStatus(ts)
		if lastHead.Valid {
			t.lastBranchHead = lastHead.String
		}
		if integrated.Valid {
			t.integrated = integrated.String
		}
		if integratedHead.Valid {
			t.integratedBranchHead = integratedHead.String
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("index integration refresh: iterate tasks: %w", err)
	}
	return out, nil
}

type computedSnapshot struct {
	Hash string
	JSON string
	Refs map[string]string
}

func buildRefsSnapshot(tasks []integrationTask, allRefs map[string]string) (computedSnapshot, []string) {
	desired := make(map[string]struct{})
	for _, t := range tasks {
		desired["refs/heads/"+t.name] = struct{}{}
		b := strings.TrimSpace(t.baseBranch)
		if b == "" {
			continue
		}
		desired["refs/heads/"+b] = struct{}{}
		desired["refs/remotes/origin/"+b] = struct{}{}
	}

	refs := make(map[string]string, len(desired))
	desiredList := make([]string, 0, len(desired))
	for r := range desired {
		desiredList = append(desiredList, r)
	}
	sort.Strings(desiredList)

	var b strings.Builder
	for _, r := range desiredList {
		sha := strings.TrimSpace(allRefs[r])
		refs[r] = sha
		b.WriteString(r)
		b.WriteByte('\x00')
		b.WriteString(sha)
		b.WriteByte('\n')
	}
	sum := sha256.Sum256([]byte(b.String()))
	hash := hex.EncodeToString(sum[:])

	js, _ := json.Marshal(refs)

	return computedSnapshot{
		Hash: hash,
		JSON: string(js),
		Refs: refs,
	}, desiredList
}

func refToFullRefName(ref string) string {
	ref = strings.TrimSpace(ref)
	if strings.HasPrefix(ref, "origin/") {
		return "refs/remotes/" + ref
	}
	if strings.HasPrefix(ref, "refs/") {
		return ref
	}
	return "refs/heads/" + ref
}

func parseRefsSnapshotJSON(js string) (map[string]string, bool) {
	js = strings.TrimSpace(js)
	if js == "" {
		return map[string]string{}, true
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(js), &m); err != nil {
		return nil, false
	}
	return m, true
}

func allowedRefsForForcedTasks(allTasks []integrationTask, forcedTaskNames []string) map[string]struct{} {
	allowed := make(map[string]struct{})

	forced := make(map[string]struct{}, len(forcedTaskNames))
	for _, n := range forcedTaskNames {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		forced[n] = struct{}{}
		allowed["refs/heads/"+n] = struct{}{}
	}

	for _, t := range allTasks {
		if _, ok := forced[t.name]; !ok {
			continue
		}
		b := strings.TrimSpace(t.baseBranch)
		if b == "" {
			continue
		}
		allowed["refs/heads/"+b] = struct{}{}
		allowed["refs/remotes/origin/"+b] = struct{}{}
	}

	return allowed
}
