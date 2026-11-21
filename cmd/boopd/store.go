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

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/donaldgifford/boop/internal/config"
	"github.com/donaldgifford/boop/internal/observability"
	"github.com/donaldgifford/boop/internal/store"
)

// runStore is `boopd store <command>`: the owner's maintenance commands.
// Today that is `rebuild --config <file>` (IMPL-0005 3.5).
func runStore(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "rebuild" {
		return write(stderr, exitUsage, "%s", usage)
	}
	fs := flag.NewFlagSet("store rebuild", flag.ContinueOnError)
	fs.SetOutput(stderr)
	path := fs.String("config", defaultConfigPath, "config file")
	batch := fs.Int("batch", store.DefaultRebuildBatch, "events read at a time")
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() != 0 {
		return exitUsage
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	if err := rebuild(ctx, *path, *batch, stdout, stderr); err != nil {
		return write(stderr, exitInvalid, "boopd store rebuild: %v\n", err)
	}
	return exitOK
}

// rebuild loads the config, connects with the owner DSN, checks the
// schema and replays the log into fresh projections.
func rebuild(ctx context.Context, path string, batch int, stdout, stderr io.Writer) error {
	log, err := observability.NewLogger(stdout, os.Getenv("LOG_LEVEL"))
	if err != nil {
		return err
	}
	cfg, diags := config.Load(path)
	if _, err := diags.WriteTo(stderr); err != nil {
		return err
	}
	if diags.HasErrors() {
		return fmt.Errorf("%s: invalid config", path)
	}
	dsn, err := cfg.ReadStoreDSN(cfg.Store.OwnerDSNSecretRef, "store.owner_dsn_secret_ref")
	if err != nil {
		return err
	}
	pool, err := pgxpool.New(ctx, dsn.Reveal())
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer pool.Close()
	if err := store.RequireVersion(ctx, pool, store.SchemaVersion); err != nil {
		return err
	}

	res, err := store.Rebuild(ctx, pool, batch, log)
	if err != nil {
		return err
	}
	log.InfoContext(ctx, "store rebuilt", "events", res.Events, "unknown_kinds", res.Unknown)
	return nil
}
