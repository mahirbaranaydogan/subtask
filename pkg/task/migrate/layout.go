package migrate

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/zippoxer/subtask/internal/filelock"
	"github.com/zippoxer/subtask/pkg/task"
)

var layoutOnce struct {
	mu   sync.Mutex
	done map[string]struct{}
}

// EnsureLayout performs best-effort, safe migration from legacy repo-local runtime/config
// into the new global layout. It is intended to be called once on process startup
// (before other domain code runs).
//
// It is idempotent and safe to call multiple times.
func EnsureLayout(repoRoot string) error {
	repoRoot = filepath.Clean(repoRoot)
	if repoRoot == "" || repoRoot == "." {
		return nil
	}
	if abs, err := filepath.Abs(repoRoot); err == nil {
		repoRoot = abs
	}

	layoutOnce.mu.Lock()
	if layoutOnce.done == nil {
		layoutOnce.done = make(map[string]struct{})
	}
	if _, ok := layoutOnce.done[repoRoot]; ok {
		layoutOnce.mu.Unlock()
		return nil
	}
	layoutOnce.done[repoRoot] = struct{}{}
	layoutOnce.mu.Unlock()

	destProject := filepath.Join(task.ProjectsDir(), task.EscapePath(repoRoot))
	if err := os.MkdirAll(destProject, 0o755); err != nil {
		return fmt.Errorf("subtask: failed to prepare runtime dir at %s: %w", destProject, err)
	}

	// Serialize migrations per repo to avoid cross-process races.
	lockPath := filepath.Join(destProject, "migrate.lock")
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("subtask: failed to open migrate lock at %s: %w", lockPath, err)
	}
	defer func() { _ = lockFile.Close() }()
	if err := filelock.LockExclusive(lockFile); err != nil {
		return fmt.Errorf("subtask: failed to lock migrate lock at %s: %w", lockPath, err)
	}
	defer func() { _ = filelock.Unlock(lockFile) }()

	// 1) Promote legacy project config to global defaults (if global missing).
	if err := promoteConfig(repoRoot); err != nil {
		return err
	}

	// 2) Migrate legacy runtime state into ~/.subtask/projects/<escaped>/.
	if err := migrateRuntime(repoRoot, destProject); err != nil {
		return err
	}

	return nil
}

func promoteConfig(repoRoot string) error {
	userCfg := task.ConfigPath()
	if fileExists(userCfg) {
		return nil
	}
	legacyProjectCfg := filepath.Join(repoRoot, ".subtask", "config.json")
	if !fileExists(legacyProjectCfg) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(userCfg), 0o755); err != nil {
		return fmt.Errorf("subtask: failed to create global config dir: %w", err)
	}
	if err := copyFileAtomic(legacyProjectCfg, userCfg); err != nil {
		return fmt.Errorf("subtask: failed to migrate legacy config %s -> %s: %w", legacyProjectCfg, userCfg, err)
	}
	return nil
}

func migrateRuntime(repoRoot, destProject string) error {
	legacySubtask := filepath.Join(repoRoot, ".subtask")
	legacyInternal := filepath.Join(legacySubtask, "internal")
	legacyIndex := filepath.Join(legacySubtask, "index.db")

	destInternal := filepath.Join(destProject, "internal")
	destIndex := filepath.Join(destProject, "index.db")

	if err := os.MkdirAll(destInternal, 0o755); err != nil {
		return fmt.Errorf("subtask: failed to create runtime internal dir: %w", err)
	}

	// Internal: merge contents (never overwrite).
	if dirExists(legacyInternal) {
		if err := mergeDirNoClobber(legacyInternal, destInternal); err != nil {
			return fmt.Errorf("subtask: failed to migrate legacy internal dir %s -> %s: %w", legacyInternal, destInternal, err)
		}
	}

	// Index db: copy if missing (index is rebuildable, so do not try to merge/overwrite).
	if fileExists(legacyIndex) && !fileExists(destIndex) {
		if err := copyFileAtomic(legacyIndex, destIndex); err != nil {
			return fmt.Errorf("subtask: failed to migrate legacy index %s -> %s: %w", legacyIndex, destIndex, err)
		}
		// Best-effort sqlite sidecars.
		_ = copyFileAtomic(legacyIndex+"-wal", destIndex+"-wal")
		_ = copyFileAtomic(legacyIndex+"-shm", destIndex+"-shm")
	}

	// Cleanup: legacy runtime state no longer belongs in the repo.
	if err := cleanupLegacyRuntime(repoRoot); err != nil {
		return err
	}

	return nil
}

func cleanupLegacyRuntime(repoRoot string) error {
	repoRoot = filepath.Clean(repoRoot)
	legacySubtask := filepath.Join(repoRoot, ".subtask")
	legacyInternal := filepath.Join(legacySubtask, "internal")
	legacyIndex := filepath.Join(legacySubtask, "index.db")

	// Safety: never remove paths that are not within the repo root.
	if rel, err := filepath.Rel(repoRoot, legacyInternal); err == nil {
		if rel != "." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && rel != ".." {
			if dirExists(legacyInternal) {
				if err := os.RemoveAll(legacyInternal); err != nil {
					return fmt.Errorf("subtask: migrated legacy runtime but failed to remove %s: %w", legacyInternal, err)
				}
			}
		}
	}

	if rel, err := filepath.Rel(repoRoot, legacyIndex); err == nil {
		if rel != "." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && rel != ".." {
			// Remove the main db and best-effort sqlite sidecars.
			if err := removeFileIfExists(legacyIndex); err != nil {
				return fmt.Errorf("subtask: migrated legacy runtime but failed to remove %s: %w", legacyIndex, err)
			}
			if err := removeFileIfExists(legacyIndex + "-wal"); err != nil {
				return fmt.Errorf("subtask: migrated legacy runtime but failed to remove %s-wal: %w", legacyIndex, err)
			}
			if err := removeFileIfExists(legacyIndex + "-shm"); err != nil {
				return fmt.Errorf("subtask: migrated legacy runtime but failed to remove %s-shm: %w", legacyIndex, err)
			}
		}
	}

	return nil
}

func removeFileIfExists(path string) error {
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return nil
}

func mergeDirNoClobber(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	for _, e := range entries {
		srcPath := filepath.Join(src, e.Name())
		dstPath := filepath.Join(dst, e.Name())
		if e.IsDir() {
			if err := mergeDirNoClobber(srcPath, dstPath); err != nil {
				return err
			}
			continue
		}
		if fileExists(dstPath) {
			continue
		}
		if err := copyFileAtomic(srcPath, dstPath); err != nil {
			return err
		}
	}
	return nil
}

func copyFileAtomic(src, dst string) error {
	if !fileExists(src) {
		return nil
	}
	if fileExists(dst) {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}

	tmp := fmt.Sprintf("%s.tmp-%d", dst, time.Now().UnixNano())
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}

	_, copyErr := io.Copy(out, in)
	syncErr := out.Sync()
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return copyErr
	}
	if syncErr != nil {
		_ = os.Remove(tmp)
		return syncErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	return os.Rename(tmp, dst)
}

func dirExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}
