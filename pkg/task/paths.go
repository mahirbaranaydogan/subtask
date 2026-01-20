package task

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/zippoxer/subtask/internal/homedir"
	"github.com/zippoxer/subtask/pkg/git"
	"github.com/zippoxer/subtask/pkg/subtaskerr"
)

var projectDirCache struct {
	mu       sync.Mutex
	computed bool
	cwd      string
	rootAbs  string
	ok       bool
	err      error
}

// GlobalDir returns ~/.subtask.
func GlobalDir() string {
	if d := strings.TrimSpace(os.Getenv("SUBTASK_DIR")); d != "" {
		return filepath.Clean(d)
	}
	home, _ := homedir.Dir()
	return filepath.Join(home, ".subtask")
}

// ConfigPath returns ~/.subtask/config.json (global defaults).
func ConfigPath() string {
	return filepath.Join(GlobalDir(), "config.json")
}

// ProjectsDir returns ~/.subtask/projects.
func ProjectsDir() string {
	return filepath.Join(GlobalDir(), "projects")
}

// WorkspacesDir returns ~/.subtask/workspaces.
func WorkspacesDir() string {
	return filepath.Join(GlobalDir(), "workspaces")
}

// GitRootAbs returns the git project root (worktree-aware).
func GitRootAbs() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	cwd = canonicalPath(cwd)

	// Fast path: cache per-cwd.
	projectDirCache.mu.Lock()
	if projectDirCache.computed && projectDirCache.cwd == cwd {
		root := projectDirCache.rootAbs
		ok := projectDirCache.ok
		cachedErr := projectDirCache.err
		projectDirCache.mu.Unlock()
		if !ok || root == "" {
			if cachedErr != nil {
				return "", cachedErr
			}
			return "", subtaskerr.ErrNotGitRepo
		}
		return root, nil
	}
	projectDirCache.mu.Unlock()

	top, err := git.Output(cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		projectDirCache.mu.Lock()
		projectDirCache.computed = true
		projectDirCache.cwd = cwd
		projectDirCache.rootAbs = ""
		projectDirCache.ok = false
		projectDirCache.err = err
		projectDirCache.mu.Unlock()
		return "", err
	}
	if abs, err := filepath.Abs(top); err == nil {
		top = abs
	}
	top = canonicalPath(top)

	// If we're inside a Subtask-managed workspace, resolve the anchor worktree.
	wsRoot := canonicalPath(WorkspacesDir())
	if isWithinDir(top, wsRoot) {
		anchor, err := resolveAnchorFromWorktreeList(top, wsRoot)
		if err != nil {
			// Cache failure for this cwd, but don't poison other dirs.
			projectDirCache.mu.Lock()
			projectDirCache.computed = true
			projectDirCache.cwd = cwd
			projectDirCache.rootAbs = ""
			projectDirCache.ok = false
			projectDirCache.err = err
			projectDirCache.mu.Unlock()
			return "", err
		}
		top = canonicalPath(anchor)
	}

	projectDirCache.mu.Lock()
	projectDirCache.computed = true
	projectDirCache.cwd = cwd
	projectDirCache.rootAbs = top
	projectDirCache.ok = true
	projectDirCache.err = nil
	projectDirCache.mu.Unlock()
	return top, nil
}

func resolveAnchorFromWorktreeList(worktreeTop string, workspacesRoot string) (string, error) {
	out, err := git.Output(worktreeTop, "worktree", "list", "--porcelain")
	if err != nil {
		return "", err
	}
	var candidates []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		const prefix = "worktree "
		if strings.HasPrefix(line, prefix) {
			p := strings.TrimSpace(strings.TrimPrefix(line, prefix))
			if p == "" {
				continue
			}
			if abs, err := filepath.Abs(p); err == nil {
				p = abs
			}
			p = canonicalPath(p)
			if isWithinDir(p, workspacesRoot) {
				continue
			}
			candidates = append(candidates, p)
		}
	}
	if len(candidates) == 0 {
		return "", fmt.Errorf("%w\n\nDetected workspace root: %s\nTip: cd to your main repo and re-run.", subtaskerr.ErrNoAnchorFromWorkspace, worktreeTop)
	}

	// Prefer an anchor that already has subtask data (reduces ambiguity).
	for _, c := range candidates {
		if dirExists(filepath.Join(c, ".subtask", "tasks")) || fileExists(filepath.Join(c, ".subtask", "config.json")) {
			return c, nil
		}
	}
	return candidates[0], nil
}

func isWithinDir(child, parent string) bool {
	child = canonicalPath(child)
	parent = canonicalPath(parent)
	if child == "" || parent == "" {
		return false
	}
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	rel = filepath.Clean(rel)
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func dirExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

// ProjectDir returns .subtask relative to cwd (anchored at git root).
func ProjectDir() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ".subtask"
	}
	cwd = canonicalPath(cwd)
	root, err := GitRootAbs()
	if err != nil || root == "" {
		return ".subtask"
	}
	abs := filepath.Join(root, ".subtask")
	rel, err := filepath.Rel(cwd, abs)
	if err == nil && rel != "" {
		return rel
	}
	return abs
}

// ProjectDirAbs returns the absolute path to the project's .subtask directory.
// If not in git, it returns "<cwd>/.subtask".
func ProjectDirAbs() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ".subtask"
	}
	cwd = canonicalPath(cwd)
	root, err := GitRootAbs()
	if err != nil || root == "" {
		return filepath.Join(cwd, ".subtask")
	}
	return filepath.Join(root, ".subtask")
}

// ProjectRoot returns the absolute path to the git project root.
// If not in git, it returns the current working directory.
func ProjectRoot() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	root, err := GitRootAbs()
	if err != nil || root == "" {
		return canonicalPath(cwd)
	}
	return root
}

// TasksDir returns .subtask/tasks.
func TasksDir() string {
	return filepath.Join(ProjectDir(), "tasks")
}

// ProjectConfigPath returns the optional project override config path (<git-root>/.subtask/config.json),
// expressed relative to cwd when possible.
func ProjectConfigPath() string {
	return filepath.Join(ProjectDir(), "config.json")
}

// InternalDir returns the runtime internal directory for this repo:
// ~/.subtask/projects/<escaped-git-root>/internal
//
// If not in git, falls back to <cwd>/.subtask/internal (legacy behavior).
func InternalDir() string {
	root, err := GitRootAbs()
	if err == nil && root != "" {
		return filepath.Join(runtimeProjectDirAbs(root), "internal")
	}
	return filepath.Join(ProjectDir(), "internal")
}

// IndexPath returns the default index db path for this repo:
// ~/.subtask/projects/<escaped-git-root>/index.db
//
// If not in git, falls back to .subtask/index.db (legacy behavior).
func IndexPath() string {
	root, err := GitRootAbs()
	if err == nil && root != "" {
		return filepath.Join(runtimeProjectDirAbs(root), "index.db")
	}
	return filepath.Join(ProjectDir(), "index.db")
}

// EscapeName converts "fix/epoch-boundary" to "fix--epoch-boundary".
func EscapeName(name string) string {
	return strings.ReplaceAll(name, "/", "--")
}

// UnescapeName converts "fix--epoch-boundary" to "fix/epoch-boundary".
func UnescapeName(escaped string) string {
	return strings.ReplaceAll(escaped, "--", "/")
}

// Dir returns .subtask/tasks/<escaped-name>.
func Dir(name string) string {
	return filepath.Join(TasksDir(), EscapeName(name))
}

// Path returns the TASK.md path.
func Path(name string) string {
	return filepath.Join(Dir(name), "TASK.md")
}

// StatePath returns the state.json path.
func StatePath(name string) string {
	return filepath.Join(InternalDir(), EscapeName(name), "state.json")
}

// HistoryPath returns the history.jsonl path.
func HistoryPath(name string) string {
	return filepath.Join(Dir(name), "history.jsonl")
}

// EscapePath converts a path to a safe directory name.
// It resolves symlinks first to ensure consistency across different cwd resolutions.
func EscapePath(p string) string {
	// Resolve symlinks to get consistent paths (e.g., /var -> /private/var on macOS)
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		p = resolved
	}
	p = strings.ReplaceAll(p, string(os.PathSeparator), "-")
	// Windows drive letters and paths include ':' which is invalid in filenames.
	p = strings.ReplaceAll(p, ":", "-")
	// Additional Windows-invalid filename characters.
	p = strings.NewReplacer(
		"<", "-",
		">", "-",
		"\"", "-",
		"|", "-",
		"?", "-",
		"*", "-",
	).Replace(p)
	return p
}

func runtimeProjectDirAbs(repoRoot string) string {
	return filepath.Join(ProjectsDir(), EscapePath(repoRoot))
}
