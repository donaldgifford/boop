/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

// SchemaVersion is the schema this build of boopd needs: the number of
// the newest migration in migrations/.
const SchemaVersion = 5

// VersionTable is goose's version table, named for boopd so it cannot
// collide with another tool's in the same database.
const VersionTable = "goose_boopd_version"

// ErrSchemaTooOld is returned by RequireVersion when the database is
// below the schema the caller needs.
var ErrSchemaTooOld = errors.New("store schema is older than this boopd needs")

//go:embed migrations/*.sql
var migrations embed.FS

// MigrateResult is what one Migrate call did.
type MigrateResult struct {
	// Applied lists the versions applied by this call, oldest first.
	Applied []int64
	// Version is the schema version after the call.
	Version int64
}

// Migrate applies every pending migration as the owner role. It holds a
// Postgres advisory lock for the whole run, so two migrate Jobs cannot
// interleave, and is a no-op when the schema is current.
func Migrate(ctx context.Context, ownerDSN string, logger *slog.Logger) (MigrateResult, error) {
	cfg, err := pgx.ParseConfig(ownerDSN)
	if err != nil {
		return MigrateResult{}, fmt.Errorf("parse owner DSN: %w", err)
	}

	db := stdlib.OpenDB(*cfg)
	defer func() {
		if cerr := db.Close(); cerr != nil {
			logger.WarnContext(ctx, "close migrate connection", slog.Any("error", cerr))
		}
	}()

	provider, err := newProvider(db, logger)
	if err != nil {
		return MigrateResult{}, err
	}

	results, err := provider.Up(ctx)
	if err != nil {
		return MigrateResult{}, fmt.Errorf("migrate up: %w", err)
	}

	applied := make([]int64, 0, len(results))
	for _, r := range results {
		applied = append(applied, r.Source.Version)
	}

	version, err := provider.GetDBVersion(ctx)
	if err != nil {
		return MigrateResult{}, fmt.Errorf("read schema version: %w", err)
	}

	return MigrateResult{Applied: applied, Version: version}, nil
}

func newProvider(db *sql.DB, logger *slog.Logger) (*goose.Provider, error) {
	sub, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return nil, fmt.Errorf("open embedded migrations: %w", err)
	}

	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return nil, fmt.Errorf("create advisory locker: %w", err)
	}

	provider, err := goose.NewProvider(goose.DialectPostgres, db, sub,
		goose.WithTableName(VersionTable),
		goose.WithSessionLocker(locker),
		goose.WithDisableGlobalRegistry(true),
		goose.WithSlog(logger),
	)
	if err != nil {
		return nil, fmt.Errorf("create migration provider: %w", err)
	}

	return provider, nil
}

// RequireVersion fails with ErrSchemaTooOld when the store's schema is
// below want. Any role can call it: store_meta is readable by all of
// them, unlike goose's version table.
func RequireVersion(ctx context.Context, pool *pgxpool.Pool, want int) error {
	var have int
	if err := pool.QueryRow(ctx, "SELECT schema_version FROM store_meta").Scan(&have); err != nil {
		return fmt.Errorf("read store_meta: %w", err)
	}

	if have < want {
		return fmt.Errorf("%w: have %d, need %d (run boopd migrate)", ErrSchemaTooOld, have, want)
	}

	return nil
}
