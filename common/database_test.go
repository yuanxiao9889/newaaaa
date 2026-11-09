package common

import (
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSQLiteDSNKeepsConcurrencyParamsForBarePaths(t *testing.T) {
	const params = "?_pragma=busy_timeout(30000)&_pragma=journal_mode(WAL)&_txlock=immediate"
	for _, tc := range []struct {
		name string
		path string
		want string
	}{
		{name: "default file name", path: "one-api.db", want: "one-api.db" + params},
		{name: "bare absolute path", path: "/data/new-api.db", want: "/data/new-api.db" + params},
		{name: "bare file URI", path: "file:/data/new-api.db", want: "file:/data/new-api.db" + params},
		{name: "in-memory database", path: ":memory:", want: ":memory:" + params},
		{name: "own query string", path: "/data/new-api.db?_pragma=busy_timeout(5000)", want: "/data/new-api.db?_pragma=busy_timeout(5000)"},
		{name: "file URI with query", path: "file:test?mode=memory&cache=shared", want: "file:test?mode=memory&cache=shared"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, sqliteDSN(tc.path))
		})
	}
}

func TestSQLiteBarePathAppliesConcurrencySettings(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(sqliteDSN(filepath.Join(t.TempDir(), "concurrency.db"))), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	var timeout int
	var journalMode string
	require.NoError(t, db.Raw("PRAGMA busy_timeout").Scan(&timeout).Error)
	require.NoError(t, db.Raw("PRAGMA journal_mode").Scan(&journalMode).Error)
	assert.Equal(t, 30000, timeout)
	assert.Equal(t, "wal", journalMode)
}
