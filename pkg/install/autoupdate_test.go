package install

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAutoUpdateIfInstalled_DoesNotCreateWhenMissing(t *testing.T) {
	base := t.TempDir()

	res, err := AutoUpdateIfInstalled(base)
	require.NoError(t, err)
	require.False(t, res.UpdatedSkill)

	_, err = os.Stat(SkillPath(base))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestAutoUpdateIfInstalled_RepairsDrift(t *testing.T) {
	base := t.TempDir()

	_, _, err := InstallTo(base)
	require.NoError(t, err)

	// Drift.
	require.NoError(t, os.WriteFile(SkillPath(base), []byte("different"), 0o644))

	res, err := AutoUpdateIfInstalled(base)
	require.NoError(t, err)
	require.True(t, res.UpdatedSkill)

	got, err := os.ReadFile(SkillPath(base))
	require.NoError(t, err)
	require.Equal(t, Embedded(), got)
}
