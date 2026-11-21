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

	"github.com/donaldgifford/boop/internal/config"
	"github.com/donaldgifford/boop/internal/observability"
	"github.com/donaldgifford/boop/internal/store"
)

// runMigrate is `boopd migrate --config <file>`: apply the store's
// schema as the owner role, then exit (DESIGN-0004 § Commands and roles).
func runMigrate(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	path := fs.String("config", defaultConfigPath, "config file")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return exitUsage
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	if err := migrate(ctx, *path, stdout, stderr); err != nil {
		return write(stderr, exitInvalid, "boopd migrate: %v\n", err)
	}
	return exitOK
}

// migrate loads the config, reads only the owner DSN and applies every
// pending migration under the advisory lock.
func migrate(ctx context.Context, path string, stdout, stderr io.Writer) error {
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

	res, err := store.Migrate(ctx, dsn.Reveal(), log)
	if err != nil {
		return err
	}
	log.InfoContext(ctx, "store migrated", "applied", res.Applied, "version", res.Version)
	return nil
}
