package git

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/zippoxer/subtask/pkg/logging"
	"github.com/zippoxer/subtask/pkg/subtaskerr"
)

// Run runs a git command in the specified directory.
func Run(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if !logging.DebugEnabled() {
		if err := cmd.Run(); err != nil {
			return &Error{Dir: dir, Args: args, Cause: err}
		}
		return nil
	}
	start := time.Now()
	err := cmd.Run()
	d := time.Since(start)

	if err != nil {
		gitCmdBatcher.flushNow()
		logging.Debug("git", fmt.Sprintf("%s (%s)", strings.Join(args, " "), d.Round(time.Millisecond)))
		logging.Error("git", fmt.Sprintf("%s error: %s (%s)", strings.Join(args, " "), err.Error(), d.Round(time.Millisecond)))
		return &Error{Dir: dir, Args: args, Cause: err}
	}

	logGitCommandTiming(args, d)
	return nil
}

// RunWithStderrFilter runs a git command, streaming stdout and filtering stderr (on success only).
// This is useful for removing known-benign warnings that would otherwise pollute CLI output.
//
// If the command fails, stderr is not filtered.
func RunWithStderrFilter(dir string, stderrFilter func(string) string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	var (
		err error
		d   time.Duration
	)
	if logging.DebugEnabled() {
		start := time.Now()
		err = cmd.Run()
		d = time.Since(start)
		if err != nil {
			gitCmdBatcher.flushNow()
			logging.Debug("git", fmt.Sprintf("%s (%s)", strings.Join(args, " "), d.Round(time.Millisecond)))
			logging.Error("git", fmt.Sprintf("%s error: %s (%s)", strings.Join(args, " "), err.Error(), d.Round(time.Millisecond)))
		} else {
			logGitCommandTiming(args, d)
		}
	} else {
		err = cmd.Run()
	}
	if stderr.Len() == 0 {
		if err != nil {
			return &Error{Dir: dir, Args: args, Cause: err}
		}
		return nil
	}

	out := stderr.String()
	if err == nil && stderrFilter != nil {
		out = stderrFilter(out)
	}
	if out != "" {
		_, _ = os.Stderr.WriteString(out)
	}
	if err != nil {
		if isNotGitRepoOutput(out) {
			return subtaskerr.ErrNotGitRepo
		}
		return &Error{Dir: dir, Args: args, Stderr: out, Cause: err}
	}
	return nil
}

// FilterLineEndingWarnings removes common git line-ending conversion warnings.
func FilterLineEndingWarnings(stderr string) string {
	if stderr == "" {
		return ""
	}
	var kept []string
	for _, line := range strings.Split(stderr, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		lower := strings.ToLower(line)
		if strings.Contains(lower, "will be replaced by crlf") || strings.Contains(lower, "will be replaced by lf") {
			continue
		}
		kept = append(kept, line)
	}
	if len(kept) == 0 {
		return ""
	}
	return strings.Join(kept, "\n") + "\n"
}

// RunQuiet runs a git command without printing output.
func RunQuiet(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	var (
		err error
		d   time.Duration
	)
	if logging.DebugEnabled() {
		start := time.Now()
		err = cmd.Run()
		d = time.Since(start)
		logGitCommandTiming(args, d)
	} else {
		err = cmd.Run()
	}
	if err != nil {
		out := stderr.String()
		if isNotGitRepoOutput(out) {
			return subtaskerr.ErrNotGitRepo
		}
		return &Error{Dir: dir, Args: args, Stderr: out, Cause: err}
	}
	return nil
}

// RunSilent runs a git command, capturing output and only showing it on error.
func RunSilent(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var (
		out []byte
		err error
		d   time.Duration
	)
	if logging.DebugEnabled() {
		start := time.Now()
		out, err = cmd.CombinedOutput()
		d = time.Since(start)
		if err != nil {
			gitCmdBatcher.flushNow()
			logging.Debug("git", fmt.Sprintf("%s (%s)", strings.Join(args, " "), d.Round(time.Millisecond)))
			logging.Error("git", fmt.Sprintf("%s error: %s (%s)", strings.Join(args, " "), err.Error(), d.Round(time.Millisecond)))
		} else {
			logGitCommandTiming(args, d)
		}
	} else {
		out, err = cmd.CombinedOutput()
	}
	if err != nil {
		// Show the output only when there's an error
		_, _ = os.Stderr.Write(out)

		if isNotGitRepoOutput(string(out)) {
			return subtaskerr.ErrNotGitRepo
		}
		return &Error{Dir: dir, Args: args, Stderr: string(out), Cause: err}
	}
	return err
}

// Output runs a git command and returns its output.
func Output(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	var (
		err error
		d   time.Duration
	)
	if logging.DebugEnabled() {
		start := time.Now()
		err = cmd.Run()
		d = time.Since(start)
	} else {
		err = cmd.Run()
	}

	outStr := strings.TrimSpace(stdout.String())
	errStr := stderr.String()

	if err != nil {
		// Check for "not a git repo" first - this is an expected condition, not an error worth logging.
		if isNotGitRepoOutput(errStr) {
			return "", subtaskerr.ErrNotGitRepo
		}
		if logging.DebugEnabled() {
			gitCmdBatcher.flushNow()
			logging.Debug("git", fmt.Sprintf("%s (%s)", strings.Join(args, " "), d.Round(time.Millisecond)))
			logging.Error("git", fmt.Sprintf("%s error: %s (%s)", strings.Join(args, " "), strings.TrimSpace(errStr), d.Round(time.Millisecond)))
		} else {
			logging.Error("git", fmt.Sprintf("%s error: %s", strings.Join(args, " "), strings.TrimSpace(errStr)))
		}
		return "", &Error{Dir: dir, Args: args, Stderr: errStr, Cause: err}
	}

	if logging.DebugEnabled() {
		logGitCommandTiming(args, d)
	}
	return outStr, nil
}

// CommitExists returns whether rev resolves to a commit object.
func CommitExists(dir, rev string) bool {
	rev = strings.TrimSpace(rev)
	if rev == "" {
		return false
	}
	return RunQuiet(dir, "cat-file", "-e", rev+"^{commit}") == nil
}

// ShowDiffStat returns lines added/removed for a single commit (like `git show --numstat`).
// If the commit does not exist, ok is false and err is nil.
func ShowDiffStat(dir, commit string) (added, removed int, ok bool, err error) {
	commit = strings.TrimSpace(commit)
	if commit == "" {
		return 0, 0, false, nil
	}
	if !CommitExists(dir, commit) {
		return 0, 0, false, nil
	}

	out, err := Output(dir, "show", "--numstat", "--format=", commit)
	if err != nil {
		return 0, 0, false, err
	}
	if out == "" {
		return 0, 0, true, nil
	}

	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 2 {
			continue
		}
		// Skip binary files (shown as "-")
		if parts[0] == "-" || parts[1] == "-" {
			continue
		}
		a, err1 := strconv.Atoi(parts[0])
		r, err2 := strconv.Atoi(parts[1])
		if err1 != nil || err2 != nil {
			continue
		}
		added += a
		removed += r
	}

	return added, removed, true, nil
}

// HasRemote checks if the repository has a remote named "origin".
func HasRemote(dir string) bool {
	_, err := Output(dir, "remote", "get-url", "origin")
	return err == nil
}

// Fetch fetches from origin.
func Fetch(dir string) error {
	return RunSilent(dir, "fetch", "origin")
}

// Checkout checks out a branch.
func Checkout(dir, branch string) error {
	return RunSilent(dir, "checkout", branch)
}

// Switch creates and switches to a new branch from a start point.
func Switch(dir, branch, startPoint string) error {
	return RunSilent(dir, "switch", "-c", branch, startPoint)
}

// SetupBranch sets up the git branch for a task (local-first).
func SetupBranch(dir string, taskBranch string, baseBranch string, baseCommit string) error {
	// Prefer a pinned base commit when available (stable diffs, staleness detection).
	if baseCommit != "" {
		if err := Switch(dir, taskBranch, baseCommit); err == nil {
			return nil
		}
	}

	return Switch(dir, taskBranch, baseBranch)
}

// IsClean checks if the working directory is clean.
func IsClean(dir string) (bool, error) {
	out, err := Output(dir, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return out == "", nil
}

// IsPushed checks if the current branch is pushed to remote.
func IsPushed(dir string) (bool, error) {
	out, err := Output(dir, "status", "-sb")
	if err != nil {
		return false, err
	}
	return !strings.Contains(out, "ahead"), nil
}

// CurrentBranch returns the current branch name.
func CurrentBranch(dir string) (string, error) {
	return Output(dir, "rev-parse", "--abbrev-ref", "HEAD")
}

// CreateWorktree creates a git worktree.
func CreateWorktree(repoDir, worktreePath, branch string) error {
	if err := os.MkdirAll(filepath.Dir(worktreePath), 0755); err != nil {
		return err
	}
	return Run(repoDir, "worktree", "add", worktreePath, branch)
}

// ResetHard resets the worktree to HEAD, discarding all changes.
func ResetHard(dir string) error {
	if err := RunSilent(dir, "reset", "--hard", "HEAD"); err != nil {
		return err
	}
	return RunSilent(dir, "clean", "-fd")
}

// DiffStat returns lines added and removed compared to baseRef.
// Includes both committed changes on the current branch and uncommitted changes.
func DiffStat(dir, baseRef string) (added, removed int, err error) {
	// git diff --numstat <baseRef>
	// Output format: <added>\t<removed>\t<file>
	// Binary files show "-" for both counts
	out, err := Output(dir, "diff", "--numstat", baseRef)
	if err != nil {
		return 0, 0, err
	}

	if out == "" {
		return 0, 0, nil
	}

	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 2 {
			continue
		}
		// Skip binary files (shown as "-")
		if parts[0] == "-" || parts[1] == "-" {
			continue
		}
		a, _ := strconv.Atoi(parts[0])
		r, _ := strconv.Atoi(parts[1])
		added += a
		removed += r
	}

	return added, removed, nil
}

// RevListCount returns how many commits are reachable from headRef but not baseCommit.
// Equivalent to: git rev-list --count <baseCommit>..<headRef>
func RevListCount(dir, baseCommit, headRef string) (int, error) {
	out, err := Output(dir, "rev-list", "--count", baseCommit+".."+headRef)
	if err != nil {
		return 0, err
	}
	if out == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0, err
	}
	return n, nil
}

type CommitMeta struct {
	SHA         string
	Subject     string
	AuthorName  string
	AuthorEmail string
	AuthoredAt  int64 // unix seconds
}

// ListCommitsRange returns commits reachable from to but not from from (from..to),
// ordered from oldest to newest.
func ListCommitsRange(dir, from, to string) ([]CommitMeta, error) {
	from = strings.TrimSpace(from)
	to = strings.TrimSpace(to)
	if from == "" || to == "" {
		return nil, fmt.Errorf("commit range requires from and to")
	}

	const fieldSep = "\x1f"
	format := "%H" + fieldSep + "%an" + fieldSep + "%ae" + fieldSep + "%at" + fieldSep + "%s"
	out, err := Output(dir, "log", "--reverse", "--format="+format, from+".."+to)
	if err != nil {
		return nil, err
	}

	out = strings.TrimSpace(out)
	if out == "" {
		return nil, nil
	}

	lines := strings.Split(out, "\n")
	commits := make([]CommitMeta, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, fieldSep)
		if len(parts) < 5 {
			continue
		}
		authoredAt, _ := strconv.ParseInt(strings.TrimSpace(parts[3]), 10, 64)
		commits = append(commits, CommitMeta{
			SHA:         strings.TrimSpace(parts[0]),
			AuthorName:  strings.TrimSpace(parts[1]),
			AuthorEmail: strings.TrimSpace(parts[2]),
			AuthoredAt:  authoredAt,
			Subject:     strings.TrimSpace(parts[4]),
		})
	}
	return commits, nil
}

// CommitsBehind returns how many commits targetRef is ahead of baseCommit.
// Equivalent to: git rev-list --count <baseCommit>..<targetRef>
func CommitsBehind(dir, baseCommit, targetRef string) (int, error) {
	out, err := Output(dir, "rev-list", "--count", baseCommit+".."+targetRef)
	if err != nil {
		return 0, err
	}
	if out == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0, err
	}
	return n, nil
}

// OverlappingFiles returns files changed in both:
// - baseCommit..workerHead (worker changes)
// - baseCommit..targetRef (base branch changes)
//
// This is a heuristic for potential merge conflicts.
func OverlappingFiles(dir, baseCommit, workerHead, targetRef string) ([]string, error) {
	workerOut, err := Output(dir, "diff", "--name-only", baseCommit, workerHead)
	if err != nil {
		return nil, err
	}
	targetOut, err := Output(dir, "diff", "--name-only", baseCommit, targetRef)
	if err != nil {
		return nil, err
	}

	workerFiles := splitNonEmptyLines(workerOut)
	if len(workerFiles) == 0 || targetOut == "" {
		return nil, nil
	}

	workerSet := make(map[string]struct{}, len(workerFiles))
	for _, f := range workerFiles {
		workerSet[f] = struct{}{}
	}

	var overlap []string
	for _, f := range splitNonEmptyLines(targetOut) {
		if _, ok := workerSet[f]; ok {
			overlap = append(overlap, f)
		}
	}
	sort.Strings(overlap)
	return overlap, nil
}

func splitNonEmptyLines(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if line == "" {
			continue
		}
		out = append(out, line)
	}
	return out
}

// GetCommitSubjects returns commit subjects from base..HEAD.
func GetCommitSubjects(dir, base string) ([]string, error) {
	out, err := Output(dir, "log", "--format=%s", base+"..HEAD")
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

// MergeBase returns the merge-base commit between two refs.
func MergeBase(dir, ref1, ref2 string) (string, error) {
	return Output(dir, "merge-base", ref1, ref2)
}

// MergeBaseForkPoint returns the fork-point merge-base between upstream and commit.
//
// This is useful for finding a stable "PR base" even if the branch tip is already
// reachable from upstream (e.g., fast-forward merged), as long as upstream's reflog
// still contains the previous base tip.
func MergeBaseForkPoint(dir, upstream, commit string) (string, error) {
	upstream = strings.TrimSpace(upstream)
	commit = strings.TrimSpace(commit)
	if upstream == "" || commit == "" {
		return "", fmt.Errorf("upstream and commit are required")
	}
	return Output(dir, "merge-base", "--fork-point", upstream, commit)
}

// MergeConflictFiles returns the list of files that would conflict when merging headRef into targetRef.
//
// This is a non-mutating check intended for preflight/status displays. It uses `git merge-tree` to
// simulate the merge and only reports files when git reports a conflict.
func MergeConflictFiles(dir, targetRef, headRef string) ([]string, error) {
	targetRef = strings.TrimSpace(targetRef)
	headRef = strings.TrimSpace(headRef)
	if targetRef == "" || headRef == "" {
		return nil, fmt.Errorf("targetRef and headRef are required")
	}

	res, err := simulateMerge(dir, targetRef, headRef)
	if err != nil {
		return nil, err
	}
	if len(res.ConflictFiles) == 0 {
		return nil, nil
	}
	return res.ConflictFiles, nil
}

func mergeTreeNameOnlyConflictFiles(output string) []string {
	lines := strings.Split(output, "\n")
	if len(lines) < 2 {
		return nil
	}

	// Skip the first line (merge result tree SHA). Read file paths until the blank separator.
	var files []string
	for i := 1; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			break
		}
		files = append(files, line)
	}

	if len(files) == 0 {
		return nil
	}

	// De-dupe and sort.
	sort.Strings(files)
	files = compactStrings(files)
	return files
}

// extractMergeConflictFiles extracts file paths from merge conflict messages.
//
// This is a fallback for cases where merge-tree output doesn't include a name-only list.
func extractMergeConflictFiles(output string) []string {
	var files []string
	for _, line := range strings.Split(output, "\n") {
		if !strings.Contains(line, "CONFLICT") {
			continue
		}
		const needle = "Merge conflict in "
		if i := strings.Index(line, needle); i >= 0 {
			f := strings.TrimSpace(line[i+len(needle):])
			if f != "" {
				files = append(files, f)
			}
		}
	}
	if len(files) == 0 {
		return nil
	}
	sort.Strings(files)
	return compactStrings(files)
}

func compactStrings(sorted []string) []string {
	if len(sorted) == 0 {
		return nil
	}
	out := sorted[:1]
	for _, s := range sorted[1:] {
		if s == "" || s == out[len(out)-1] {
			continue
		}
		out = append(out, s)
	}
	return out
}

// SquashCommits soft-resets to mergeBase and commits with message.
// All changes since mergeBase are staged and committed as a single commit.
func SquashCommits(dir, mergeBase, message string) error {
	// Soft reset to merge base (keeps changes staged)
	if err := RunSilent(dir, "reset", "--soft", mergeBase); err != nil {
		return fmt.Errorf("soft reset failed: %w", err)
	}

	// Create squash commit
	if err := RunSilent(dir, "commit", "-m", message); err != nil {
		return fmt.Errorf("squash commit failed: %w", err)
	}

	return nil
}

// RebaseOnto rebases current branch onto target. Aborts and returns error on conflict.
// On conflict, returns extracted CONFLICT lines (if any).
func RebaseOnto(dir, target string) error {
	cmd := exec.Command("git", "rebase", target)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		// Abort the rebase on conflict
		_ = RunQuiet(dir, "rebase", "--abort")
		conflicts := extractConflictLines(string(out))
		if conflicts != "" {
			return fmt.Errorf("%s", conflicts)
		}
		return fmt.Errorf("rebase failed (no conflict details available)")
	}
	return nil
}

// extractConflictLines extracts lines containing "CONFLICT" from git output.
// Returns empty string if none found (e.g., localized git).
func extractConflictLines(output string) string {
	var lines []string
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, "CONFLICT") {
			lines = append(lines, "    "+line)
		}
	}
	return strings.Join(lines, "\n")
}

func worktreePathsForBranch(dir, branch string) ([]string, error) {
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return nil, nil
	}

	out, err := Output(dir, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return nil, nil
	}

	want := "refs/heads/" + branch

	var (
		currentPath   string
		currentBranch string
		paths         []string
	)
	flush := func() {
		if currentPath != "" && currentBranch == want {
			paths = append(paths, currentPath)
		}
		currentPath = ""
		currentBranch = ""
	}

	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			flush()
			continue
		}

		if strings.HasPrefix(line, "worktree ") {
			// New record (flush previous, then start).
			flush()
			currentPath = strings.TrimSpace(strings.TrimPrefix(line, "worktree "))
			continue
		}
		if strings.HasPrefix(line, "branch ") {
			currentBranch = strings.TrimSpace(strings.TrimPrefix(line, "branch "))
			continue
		}
	}
	flush()

	return paths, nil
}

// LocalPush fast-forwards targetBranch to current HEAD.
//
// If targetBranch is checked out in another worktree (e.g. the user's main worktree),
// update it using a fast-forward merge so local uncommitted changes are preserved when
// they don't overlap (git-like behavior).
//
// If targetBranch is not checked out anywhere, update only the ref.
func LocalPush(dir, targetBranch string) error {
	targetBranch = strings.TrimSpace(targetBranch)
	if targetBranch == "" {
		return fmt.Errorf("targetBranch is required")
	}

	head, err := Output(dir, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	head = strings.TrimSpace(head)
	if head == "" {
		return fmt.Errorf("failed to resolve HEAD")
	}

	old, err := Output(dir, "rev-parse", targetBranch)
	if err != nil {
		return err
	}
	old = strings.TrimSpace(old)
	if old == "" {
		return fmt.Errorf("failed to resolve %s", targetBranch)
	}

	ok, err := IsAncestor(dir, old, head)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("cannot fast-forward %s to %s (not a descendant)", targetBranch, head)
	}

	paths, err := worktreePathsForBranch(dir, targetBranch)
	if err != nil {
		return err
	}
	if len(paths) > 1 {
		return fmt.Errorf("cannot fast-forward %s: branch is checked out in multiple worktrees (%s)", targetBranch, strings.Join(paths, ", "))
	}
	if len(paths) == 1 {
		wt := paths[0]
		if err := RunSilent(wt, "merge", "--ff-only", head); err != nil {
			return fmt.Errorf("failed to fast-forward %s in %s: %w", targetBranch, wt, err)
		}
		return nil
	}

	// Ref-only update (not checked out anywhere). Use the expected old SHA to avoid races.
	return RunSilent(dir, "update-ref", "-m", "subtask merge", "refs/heads/"+targetBranch, head, old)
}

// IntegrationReason describes why a branch is considered integrated into target.
type IntegrationReason string

const (
	IntegratedSameCommit       IntegrationReason = "same_commit"        // Branch HEAD == target HEAD
	IntegratedAncestor         IntegrationReason = "ancestor"           // Branch is ancestor of target
	IntegratedNoAddedChanges   IntegrationReason = "no_changes"         // Three-dot diff is empty
	IntegratedTreesMatch       IntegrationReason = "trees_match"        // Same file tree, different history
	IntegratedMergeAddsNothing IntegrationReason = "merge_adds_nothing" // Merge simulation produces same tree
)

// IsIntegrated checks if branch's changes are integrated into target.
// Returns the reason if integrated, empty string if not.
// Checks are ordered by cost (cheapest first).
func IsIntegrated(dir, branch, target string) IntegrationReason {
	// Check if branch exists first
	if !BranchExists(dir, branch) {
		return ""
	}

	// 1. Same commit (cheapest - just compare SHAs)
	if isSameCommit(dir, branch, target) {
		return IntegratedSameCommit
	}

	// 2. Branch is ancestor of target (target moved past branch)
	if isAncestor(dir, branch, target) {
		return IntegratedAncestor
	}

	// 3. No added changes (empty three-dot diff from merge-base)
	if !hasAddedChanges(dir, branch, target) {
		return IntegratedNoAddedChanges
	}

	// 4. Trees match (same files, different history - handles rebase)
	if treesMatch(dir, branch, target) {
		return IntegratedTreesMatch
	}

	// 5. Merge adds nothing (most expensive - merge simulation)
	if !mergeAddsChanges(dir, branch, target) {
		return IntegratedMergeAddsNothing
	}

	return "" // Not integrated
}

// BranchExists checks if a branch exists in the repository.
func BranchExists(dir, branch string) bool {
	err := RunQuiet(dir, "rev-parse", "--verify", branch)
	return err == nil
}

// IsAncestor reports whether ancestor is reachable from descendant.
//
// This wraps `git merge-base --is-ancestor`:
// - returns (true, nil) if ancestor is an ancestor of descendant
// - returns (false, nil) if ancestor is NOT an ancestor of descendant
// - returns (false, err) for other git errors (missing commits, not a repo, etc.)
func IsAncestor(dir, ancestor, descendant string) (bool, error) {
	err := RunQuiet(dir, "merge-base", "--is-ancestor", ancestor, descendant)
	if err == nil {
		return true, nil
	}

	// Exit code 1 = not ancestor.
	var gitErr *Error
	if errors.As(err, &gitErr) {
		var exitErr *exec.ExitError
		if errors.As(gitErr.Cause, &exitErr) && exitErr.ExitCode() == 1 {
			return false, nil
		}
	}

	return false, err
}

// isSameCommit checks if two refs point to the same commit.
func isSameCommit(dir, ref1, ref2 string) bool {
	out, err := Output(dir, "rev-parse", ref1, ref2)
	if err != nil {
		return false
	}
	lines := strings.Split(out, "\n")
	if len(lines) < 2 {
		return false
	}
	return lines[0] == lines[1]
}

// isAncestor checks if base is an ancestor of head.
func isAncestor(dir, base, head string) bool {
	err := RunQuiet(dir, "merge-base", "--is-ancestor", base, head)
	return err == nil
}

// hasAddedChanges checks if branch has changes beyond the merge-base with target.
func hasAddedChanges(dir, branch, target string) bool {
	mergeBase, err := MergeBase(dir, branch, target)
	if err != nil {
		return true // Assume has changes on error
	}
	// Check if there are any changed files from merge-base to branch
	out, err := Output(dir, "diff", "--name-only", mergeBase+".."+branch)
	if err != nil {
		return true
	}
	return strings.TrimSpace(out) != ""
}

// treesMatch checks if two refs have the same file tree (same content, possibly different history).
func treesMatch(dir, ref1, ref2 string) bool {
	out, err := Output(dir, "rev-parse", ref1+"^{tree}", ref2+"^{tree}")
	if err != nil {
		return false
	}
	lines := strings.Split(out, "\n")
	if len(lines) < 2 {
		return false
	}
	return lines[0] == lines[1]
}

// mergeAddsChanges checks if merging branch into target would add any changes.
// Uses git merge-tree to simulate the merge without actually performing it.
func mergeAddsChanges(dir, branch, target string) bool {
	res, err := simulateMerge(dir, target, branch)
	if err != nil || len(res.ConflictFiles) > 0 || strings.TrimSpace(res.MergedTree) == "" {
		return true // Assume has changes on error (including conflicts)
	}

	// Get target's current tree
	targetTree, err := Output(dir, "rev-parse", target+"^{tree}")
	if err != nil {
		return true
	}

	// If merge result equals target tree, merging adds nothing
	return strings.TrimSpace(res.MergedTree) != strings.TrimSpace(targetTree)
}

// EffectiveTarget returns target or origin/target if origin is ahead.
//
// Note: This is used for *observation/detection* (e.g. noticing merges that happened
// upstream after a fetch), not for preferring remotes during user-facing operations.
func EffectiveTarget(dir, target string) string {
	if !HasRemote(dir) {
		return target
	}

	upstream := "origin/" + target

	// Check if upstream exists
	if !BranchExists(dir, upstream) {
		return target
	}

	// If local and upstream are same commit, prefer local
	if isSameCommit(dir, target, upstream) {
		return target
	}

	// If local is strictly behind upstream (local is ancestor of upstream),
	// use upstream as it may have PR merges not yet pulled into the local branch
	if isAncestor(dir, target, upstream) {
		return upstream
	}

	return target
}
