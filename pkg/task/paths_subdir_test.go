package task

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zippoxer/subtask/pkg/subtaskerr"
)

func TestProjectDir_AnchorsAtGitRoot_FromSubdir(t *testing.T) {
	root := t.TempDir()
	initGitRepo(t, root)

	subdir := filepath.Join(root, "src", "pkg")
	require.NoError(t, os.MkdirAll(subdir, 0o755))

	orig, _ := os.Getwd()
	require.NoError(t, os.Chdir(subdir))
	t.Cleanup(func() { _ = os.Chdir(orig) })

	resetProjectCache()

	require.Equal(t, filepath.Join("..", "..", ".subtask"), ProjectDir())

	expectedRoot, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)
	require.Equal(t, expectedRoot, ProjectRoot())
	require.Equal(t, filepath.Join(expectedRoot, ".subtask"), ProjectDirAbs())
}

func TestGitRootAbs_NotGitRepo(t *testing.T) {
	dir := t.TempDir()

	orig, _ := os.Getwd()
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(orig) })

	resetProjectCache()
	_, err := GitRootAbs()
	require.ErrorIs(t, err, subtaskerr.ErrNotGitRepo)
}

func resetProjectCache() {
	projectDirCache.mu.Lock()
	projectDirCache.computed = false
	projectDirCache.cwd = ""
	projectDirCache.rootAbs = ""
	projectDirCache.ok = false
	projectDirCache.err = nil
	projectDirCache.mu.Unlock()
}

func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	run(t, dir, "git", "init")
}

func run(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", out)
}
