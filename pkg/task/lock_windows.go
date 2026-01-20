//go:build windows

package task

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/zippoxer/subtask/internal/filelock"
)

func lockPath(taskName string) string {
	return filepath.Join(InternalDir(), EscapeName(taskName), "op.lock")
}

// WithLock acquires an exclusive per-task lock and runs fn while holding it.
func WithLock(taskName string, fn func() error) error {
	path := lockPath(taskName)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return err
	}
	if err := filelock.LockExclusive(f); err != nil {
		_ = f.Close()
		return err
	}
	defer func() {
		_ = filelock.Unlock(f)
		_ = f.Close()
	}()

	return fn()
}

// TryWithLock attempts to acquire an exclusive per-task lock without blocking.
// If the lock is already held by another process, it returns (false, nil).
func TryWithLock(taskName string, fn func() error) (bool, error) {
	path := lockPath(taskName)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return false, err
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return false, err
	}

	locked, err := filelock.TryLockExclusive(f)
	if err != nil {
		_ = f.Close()
		return false, fmt.Errorf("failed to lock task %q: %w", taskName, err)
	}
	if !locked {
		_ = f.Close()
		return false, nil
	}

	defer func() {
		_ = filelock.Unlock(f)
		_ = f.Close()
	}()

	if err := fn(); err != nil {
		return false, err
	}
	return true, nil
}
