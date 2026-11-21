//go:build integration

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

package store_test

import (
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/donaldgifford/boop/internal/store"
	"github.com/donaldgifford/boop/internal/store/storetest"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// migrated starts Postgres and applies the schema as boopd_owner.
func migrated(t *testing.T) *storetest.Server {
	t.Helper()
	srv := storetest.Start(t)
	if _, err := store.Migrate(t.Context(), srv.OwnerDSN(), quiet); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return srv
}

func connect(t *testing.T, dsn string) *pgx.Conn {
	t.Helper()
	conn, err := pgx.Connect(t.Context(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(t.Context()) })
	return conn
}

func TestMigrate_UpFromEmptyThenIdempotent(t *testing.T) {
	srv := storetest.Start(t)

	first, err := store.Migrate(t.Context(), srv.OwnerDSN(), quiet)
	if err != nil {
		t.Fatalf("first Migrate: %v", err)
	}
	if first.Version != store.SchemaVersion || len(first.Applied) != store.SchemaVersion {
		t.Errorf("first = %+v, want all %d applied", first, store.SchemaVersion)
	}

	second, err := store.Migrate(t.Context(), srv.OwnerDSN(), quiet)
	if err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	if second.Version != store.SchemaVersion || len(second.Applied) != 0 {
		t.Errorf("second = %+v, want nothing applied", second)
	}

	var table string
	if err := connect(t, srv.OwnerDSN()).QueryRow(t.Context(),
		"SELECT to_regclass('public.'||$1)::text", store.VersionTable).Scan(&table); err != nil || table != store.VersionTable {
		t.Errorf("version table = %q, %v", table, err)
	}
}

func TestRequireVersion(t *testing.T) {
	srv := migrated(t)
	pool, err := pgxpool.New(t.Context(), srv.RoleDSN(storetest.RoleRO))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	if err := store.RequireVersion(t.Context(), pool, store.SchemaVersion); err != nil {
		t.Errorf("RequireVersion(current) = %v", err)
	}
	if err := store.RequireVersion(t.Context(), pool, store.SchemaVersion+1); !errors.Is(err, store.ErrSchemaTooOld) {
		t.Errorf("RequireVersion(next) = %v, want ErrSchemaTooOld", err)
	}
}

// insufficientPrivilege is SQLSTATE 42501.
func insufficientPrivilege(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "42501"
}

func TestGrants_EventsAreAppendOnly(t *testing.T) {
	srv := migrated(t)
	ctx := t.Context()

	insert := `INSERT INTO events (key, occurred_at, kind, source) VALUES ($1, now(), 'test.kind', 'test')`

	for _, role := range []string{storetest.RoleWorker, storetest.RoleIngest} {
		t.Run(role, func(t *testing.T) {
			conn := connect(t, srv.RoleDSN(role))
			if _, err := conn.Exec(ctx, insert, "grant-test:"+role); err != nil {
				t.Fatalf("INSERT events as %s: %v", role, err)
			}
			if _, err := conn.Exec(ctx, "UPDATE events SET summary = 'x'"); !insufficientPrivilege(err) {
				t.Errorf("UPDATE events as %s = %v, want permission denied", role, err)
			}
			if _, err := conn.Exec(ctx, "DELETE FROM events"); !insufficientPrivilege(err) {
				t.Errorf("DELETE FROM events as %s = %v, want permission denied", role, err)
			}
			if _, err := conn.Exec(ctx, "DELETE FROM event_keys"); !insufficientPrivilege(err) {
				t.Errorf("DELETE FROM event_keys as %s = %v, want permission denied", role, err)
			}
		})
	}

	t.Run(storetest.RoleRO, func(t *testing.T) {
		conn := connect(t, srv.RoleDSN(storetest.RoleRO))
		var n int
		if err := conn.QueryRow(ctx, "SELECT count(*) FROM events").Scan(&n); err != nil {
			t.Fatalf("SELECT as boopd_ro: %v", err)
		}
		if _, err := conn.Exec(ctx, insert, "grant-test:ro"); err == nil {
			t.Error("INSERT events as boopd_ro succeeded, want it refused")
		}
		if _, err := conn.Exec(ctx, "INSERT INTO repositories (id, installation_id, owner, slug, updated_at) VALUES (1, 1, 'o', 'o/r', now())"); err == nil {
			t.Error("INSERT repositories as boopd_ro succeeded, want it refused")
		}
	})

	t.Run("ingest writes only its projections", func(t *testing.T) {
		conn := connect(t, srv.RoleDSN(storetest.RoleIngest))
		if _, err := conn.Exec(ctx, "INSERT INTO runs (id, repo_id, started_at, updated_at) VALUES ('r', 1, now(), now())"); !insufficientPrivilege(
			err,
		) {
			t.Errorf("INSERT runs as boopd_ingest = %v, want permission denied", err)
		}
		if _, err := conn.Exec(ctx, "INSERT INTO dashboard_items (repo_id, marker, branch, first_seen_at, last_seen_at) VALUES (1, 'approve', 'b', now(), now())"); err != nil {
			t.Errorf("INSERT dashboard_items as boopd_ingest: %v", err)
		}
	})
}

func TestPartitionFunctions(t *testing.T) {
	srv := migrated(t)
	ctx := t.Context()
	worker := connect(t, srv.RoleDSN(storetest.RoleWorker))

	// The migration made this month and three ahead; asking again is a no-op.
	var created int
	if err := worker.QueryRow(ctx, "SELECT ensure_partitions(3)").Scan(&created); err != nil {
		t.Fatalf("ensure_partitions(3): %v", err)
	}
	if created != 0 {
		t.Errorf("ensure_partitions(3) after migrate created %d, want 0", created)
	}
	if err := worker.QueryRow(ctx, "SELECT ensure_partitions(5)").Scan(&created); err != nil || created != 2 {
		t.Errorf("ensure_partitions(5) = %d, %v, want 2 new", created, err)
	}

	// An event far in the past lands in the default partition; dropping
	// before now removes it and its key, and no monthly partition yet ends
	// before the current month.
	old := time.Date(2020, 1, 15, 0, 0, 0, 0, time.UTC)
	if _, err := worker.Exec(ctx, "INSERT INTO event_keys (key, occurred_at) VALUES ('old', $1)", old); err != nil {
		t.Fatal(err)
	}
	if _, err := worker.Exec(ctx, "INSERT INTO events (key, occurred_at, kind, source) VALUES ('old', $1, 'test.kind', 'test')", old); err != nil {
		t.Fatal(err)
	}

	var dropped int
	if err := worker.QueryRow(ctx, "SELECT drop_partitions(date_trunc('month', now()))").Scan(&dropped); err != nil {
		t.Fatalf("drop_partitions: %v", err)
	}
	if dropped != 0 {
		t.Errorf("drop_partitions(start of month) dropped %d, want 0", dropped)
	}

	owner := connect(t, srv.OwnerDSN())
	var events, keys int
	if err := owner.QueryRow(ctx, "SELECT (SELECT count(*) FROM events), (SELECT count(*) FROM event_keys)").Scan(&events, &keys); err != nil {
		t.Fatal(err)
	}
	if events != 0 || keys != 0 {
		t.Errorf("after drop: %d events, %d keys, want 0 and 0", events, keys)
	}

	// Dropping a year ahead takes every monthly partition.
	if err := worker.QueryRow(ctx, "SELECT drop_partitions(now() + interval '1 year')").Scan(&dropped); err != nil ||
		dropped != 6 {
		t.Errorf("drop_partitions(+1y) = %d, %v, want 6", dropped, err)
	}
}
