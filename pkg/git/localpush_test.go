package git

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalPush_AllowsFastForwardWithUncommittedNonOverlappingChanges(t *testing.T) {
	repo := testRepo(t)

	// Create a base file on master in the main worktree.
	if err := os.WriteFile(filepath.Join(repo, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, repo, "add", "base.txt")
	gitCmd(t, repo, "commit", "-m", "base")

	// Create a worktree for a feature branch and advance it.
	featureWT := filepath.Join(t.TempDir(), "feature-wt")
	gitCmd(t, repo, "worktree", "add", "-b", "feature", featureWT)
	if err := os.WriteFile(filepath.Join(featureWT, "feature.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, featureWT, "add", "feature.txt")
	gitCmd(t, featureWT, "commit", "-m", "feature")

	// Dirty the main worktree with a non-overlapping change.
	if err := os.WriteFile(filepath.Join(repo, "dirty.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, repo, "add", "dirty.txt")
	if err := os.WriteFile(filepath.Join(repo, "dirty.txt"), []byte("dirty-modified\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	oldMaster := strings.TrimSpace(gitCmd(t, repo, "rev-parse", "master"))
	head := strings.TrimSpace(gitCmd(t, featureWT, "rev-parse", "HEAD"))

	// LocalPush should fast-forward master, even though the main worktree is dirty.
	if err := LocalPush(featureWT, "master"); err != nil {
		t.Fatalf("LocalPush returned error: %v", err)
	}

	newMaster := strings.TrimSpace(gitCmd(t, repo, "rev-parse", "master"))
	if newMaster != head {
		t.Fatalf("expected master to fast-forward to %s, got %s (old %s)", head, newMaster, oldMaster)
	}

	// The uncommitted change remains.
	st, err := Output(repo, "status", "--porcelain")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(st, "dirty.txt") {
		t.Fatalf("expected dirty.txt to remain dirty, status:\n%s", st)
	}
}

func TestLocalPush_FailsWhenUncommittedChangesWouldBeOverwritten(t *testing.T) {
	repo := testRepo(t)

	// Create a base file on master in the main worktree.
	if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, repo, "add", "file.txt")
	gitCmd(t, repo, "commit", "-m", "base")

	// Create a worktree for a feature branch and modify file.txt.
	featureWT := filepath.Join(t.TempDir(), "feature-wt")
	gitCmd(t, repo, "worktree", "add", "-b", "feature", featureWT)
	if err := os.WriteFile(filepath.Join(featureWT, "file.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, featureWT, "add", "file.txt")
	gitCmd(t, featureWT, "commit", "-m", "feature edit")

	oldMaster := strings.TrimSpace(gitCmd(t, repo, "rev-parse", "master"))
	head := strings.TrimSpace(gitCmd(t, featureWT, "rev-parse", "HEAD"))

	// Dirty the main worktree in a way that would be overwritten by the fast-forward.
	if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("dirty local\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := LocalPush(featureWT, "master")
	if err == nil {
		t.Fatalf("expected LocalPush to fail due to overlapping uncommitted changes")
	}

	// Branch ref should not move.
	newMaster := strings.TrimSpace(gitCmd(t, repo, "rev-parse", "master"))
	if newMaster != oldMaster {
		t.Fatalf("expected master to remain at %s, got %s (head %s)", oldMaster, newMaster, head)
	}

	// Local change remains.
	got, readErr := os.ReadFile(filepath.Join(repo, "file.txt"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != "dirty local\n" {
		t.Fatalf("expected dirty working tree content to remain, got %q", string(got))
	}
}
