// Package sandbox runs the playground on the CLI's embedded OpenFGA server,
// persisted in a SQLite file.
package sandbox

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"

	"github.com/openfga/openfga/assets"
	"github.com/openfga/openfga/pkg/storage"
	"github.com/openfga/openfga/pkg/storage/sqlcommon"
	"github.com/openfga/openfga/pkg/storage/sqlite"
	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite" // pure-Go driver, registers "sqlite"
)

// ErrSchemaTooNew is returned (wrapped) by OpenDatastore when the database
// was migrated by a newer binary than this one. The file is left untouched.
var ErrSchemaTooNew = errors.New("database schema is newer than this binary supports")

// OpenDatastore opens (creating if needed) the SQLite database at path, applies
// the OpenFGA migrations embedded in this binary, and returns the SQLite
// datastore with metrics export off.
//
// The returned datastore owns the underlying *sql.DB; callers release it by
// calling the datastore's Close(). If the database's migration version is
// above the newest embedded migration, OpenDatastore returns an error wrapping
// ErrSchemaTooNew before running any migration, so the file is not modified.
func OpenDatastore(path string) (storage.OpenFGADatastore, error) {
	dsn, err := sqlite.PrepareDSN(path)
	if err != nil {
		return nil, fmt.Errorf("prepare sqlite dsn: %w", err)
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}
	if err := migrate(context.Background(), db); err != nil {
		_ = db.Close()
		return nil, err
	}
	ds, err := sqlite.NewWithDB(db, sqlcommon.NewConfig())
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("initialize sqlite datastore: %w", err)
	}
	return ds, nil
}

func migrate(ctx context.Context, db *sql.DB) error {
	migrations, err := fs.Sub(assets.EmbedMigrations, assets.SqliteMigrationDir)
	if err != nil {
		return fmt.Errorf("load embedded migrations: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, migrations)
	if err != nil {
		return fmt.Errorf("create migration provider: %w", err)
	}

	// A brand-new database has no version table yet; GetVersions reports that
	// as an error with current == -1, which is fine: nothing is too new.
	current, target, err := provider.GetVersions(ctx)
	if err == nil && current > target {
		return fmt.Errorf("%w (database at version %d, this binary supports up to %d)", ErrSchemaTooNew, current, target)
	}

	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("run sqlite migrations: %w", err)
	}
	return nil
}
