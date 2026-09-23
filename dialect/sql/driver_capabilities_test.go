package sql

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/dialect"
)

// TestDriver_ServerCapabilities_MySQLProbesOnce pins that a MySQL driver
// asks the server for its version once, however many callers race for it,
// and that wrappers of the driver share the answer.
func TestDriver_ServerCapabilities_MySQLProbesOnce(t *testing.T) {
	for _, tt := range []struct {
		version string
		window  bool
	}{
		{"5.7.44-log", false},
		{"8.4.3", true},
		{"10.6.12-MariaDB", true},
	} {
		t.Run(tt.version, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			mock.ExpectQuery(`SELECT VERSION\(\)`).WillReturnRows(sqlmock.NewRows([]string{"VERSION()"}).AddRow(tt.version))
			drv := OpenDB(dialect.MySQL, db)
			var wg sync.WaitGroup
			for range 8 {
				wg.Go(func() {
					caps, err := drv.ServerCapabilities(context.Background())
					assert.NoError(t, err)
					assert.Equal(t, tt.window, caps.Has(dialect.CapWindowFunctions))
				})
			}
			wg.Wait()
			for _, d := range []dialect.Driver{NewStatsDriver(drv), NewLogDriver(drv), dialect.Debug(drv)} {
				caps, err := dialect.DriverCapabilities(context.Background(), d)
				require.NoError(t, err)
				assert.Equal(t, tt.window, caps.Has(dialect.CapWindowFunctions), "%T", d)
			}
			require.NoError(t, mock.ExpectationsWereMet(), "exactly one version probe")
		})
	}
}

// TestDriver_ServerCapabilities_ErrorIsNotCached pins that a failed probe
// is reported and retried, not remembered as "no capabilities".
func TestDriver_ServerCapabilities_ErrorIsNotCached(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	boom := errors.New("boom")
	mock.ExpectQuery(`SELECT VERSION\(\)`).WillReturnError(boom)
	mock.ExpectQuery(`SELECT VERSION\(\)`).WillReturnRows(sqlmock.NewRows([]string{"VERSION()"}).AddRow("8.0.36"))
	drv := OpenDB(dialect.MySQL, db)
	_, err = drv.ServerCapabilities(context.Background())
	require.ErrorIs(t, err, boom)
	caps, err := drv.ServerCapabilities(context.Background())
	require.NoError(t, err)
	assert.True(t, caps.Has(dialect.CapWindowFunctions))
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestDriver_ServerCapabilities_StaticDialectsDoNotQuery pins that a
// dialect whose capabilities do not depend on the version never pays for a
// probe.
func TestDriver_ServerCapabilities_StaticDialectsDoNotQuery(t *testing.T) {
	for _, name := range []string{dialect.Postgres, dialect.SQLite} {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		caps, err := OpenDB(name, db).ServerCapabilities(context.Background())
		require.NoError(t, err)
		assert.Equal(t, dialect.GetCapabilities(name), caps)
		require.NoError(t, mock.ExpectationsWereMet())
	}
}
