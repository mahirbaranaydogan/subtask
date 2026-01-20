package index

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	_ "modernc.org/sqlite"

	"github.com/zippoxer/subtask/pkg/task"
	"github.com/zippoxer/subtask/pkg/testutil"
)

func TestMigrateToV6_RunningToWorking(t *testing.T) {
	_ = testutil.NewTestEnv(t, 0)
	dbPath := task.IndexPath()

	require.NoError(t, os.MkdirAll(filepath.Dir(dbPath), 0o755))
	db, err := sql.Open("sqlite", dbPath)
	require.NoError(t, err)

	_, err = db.Exec(`
CREATE TABLE tasks (
  name TEXT PRIMARY KEY,
  worker_status TEXT
);
INSERT INTO tasks (name, worker_status) VALUES ('legacy/running', 'running');
PRAGMA user_version=5;
`)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	idx, err := Open(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })

	var got string
	require.NoError(t, idx.db.QueryRowContext(context.Background(), `SELECT worker_status FROM tasks WHERE name='legacy/running';`).Scan(&got))
	require.Equal(t, "working", got)
}
