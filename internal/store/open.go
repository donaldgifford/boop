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
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Open connects a pool on dsn and checks the schema is at least
// SchemaVersion, so a role never runs against a store it does not
// understand. The caller closes the pool.
func Open(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	return open(ctx, dsn, nil)
}

// ReadOnlyStatementTimeout bounds every statement on the API's pool, so
// one slow query cannot hold a connection the rest are waiting for.
const ReadOnlyStatementTimeout = 5 * time.Second

// OpenReadOnly is Open for the API role: every connection starts with
// default_transaction_read_only on and a ReadOnlyStatementTimeout
// statement_timeout, on top of boopd_ro's grants.
func OpenReadOnly(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	return open(ctx, dsn, map[string]string{
		"default_transaction_read_only": "on",
		"statement_timeout":             strconv.FormatInt(ReadOnlyStatementTimeout.Milliseconds(), 10),
	})
}

func open(ctx context.Context, dsn string, params map[string]string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse store DSN: %w", err)
	}
	for k, v := range params {
		cfg.ConnConfig.RuntimeParams[k] = v
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect store: %w", err)
	}
	if err := RequireVersion(ctx, pool, SchemaVersion); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}
