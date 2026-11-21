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
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/donaldgifford/boop/internal/api"
	"github.com/donaldgifford/boop/internal/config"
	"github.com/donaldgifford/boop/internal/observability"
	"github.com/donaldgifford/boop/internal/store"
	"github.com/donaldgifford/boop/internal/temporal"
)

// apiRole is the Postgres role the API reads as.
const apiRole = "boopd_ro"

// errNoAPIAuth stops `boopd api` on a file without api.auth: the API
// never serves unauthenticated.
var errNoAPIAuth = errors.New("api.auth not configured")

// runAPI is `boopd api --config <file>`: the API role until SIGTERM or
// SIGINT, then a graceful stop (DESIGN-0004 § The API).
func runAPI(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("api", flag.ContinueOnError)
	fs.SetOutput(stderr)
	path := fs.String("config", defaultConfigPath, "config file")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return exitUsage
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	if err := runAPIRole(ctx, *path, stdout, stderr, nil); err != nil {
		return write(stderr, exitInvalid, "boopd api: %v\n", err)
	}
	return exitOK
}

// runAPIRole loads the config and the authz file, opens the read-only
// pool as boopd_ro, makes a lazy Temporal client, starts OIDC discovery
// in the background and serves. listening, when set, receives the bound
// listen address.
func runAPIRole(ctx context.Context, path string, stdout, stderr io.Writer, listening func(string)) error {
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
	if cfg.API.Auth == nil {
		return errNoAPIAuth
	}
	authz, err := api.LoadAuthzConfig(cfg.API.AuthzFile)
	if err != nil {
		return err
	}

	mp, metricsHandler, err := observability.NewMeterProvider()
	if err != nil {
		return err
	}
	defer func() {
		if err := mp.Shutdown(context.WithoutCancel(ctx)); err != nil {
			log.Warn("meter provider shutdown", "error", err)
		}
	}()
	metrics, err := observability.NewMetrics(mp)
	if err != nil {
		return err
	}
	log = log.With("pod", os.Getenv("POD_NAME"))

	dsn, err := cfg.ReadStoreDSN(cfg.Store.RODSNSecretRef, "store.ro_dsn_secret_ref")
	if err != nil {
		return err
	}
	pool, err := store.OpenReadOnly(ctx, dsn.Reveal())
	if err != nil {
		return err
	}
	defer pool.Close()
	st := store.New(pool, apiRole, metrics)

	tcfg, err := temporal.ConfigFromEnv()
	if err != nil {
		return err
	}
	c, err := temporal.Dial(ctx, &tcfg, temporal.DialOptions{Logger: log, MeterProvider: mp, Lazy: true})
	if err != nil {
		return err
	}
	defer c.Close()

	a := cfg.API.Auth
	authn := api.NewAuthenticator(&api.AuthnConfig{
		Issuer: a.Issuer, Audience: a.Audience, NameClaim: a.NameClaim, GroupsClaim: a.GroupsClaim,
	}, log, metrics)
	var wg sync.WaitGroup
	discoverCtx, stopDiscovery := context.WithCancel(ctx)
	defer func() {
		stopDiscovery()
		wg.Wait()
	}()
	wg.Go(func() { authn.Start(discoverCtx) })

	return api.Run(ctx, &api.RunOptions{
		Handler: api.Options{
			Reader:             st,
			Authn:              authn,
			Authz:              authz,
			Temporal:           c,
			StatusRequiresAuth: !cfg.API.StatusPublic,
			Logger:             log,
			Metrics:            metrics,
		},
		Ready: []observability.Check{
			{Name: "store", Check: st.SchemaReady},
			{Name: "oidc", Check: func(context.Context) error {
				if !authn.Ready() {
					return errors.New("issuer not discovered")
				}
				return nil
			}},
		},
		ListenAddr:     envOr("LISTEN_ADDR", defaultListenAddr),
		MetricsAddr:    envOr("METRICS_ADDR", defaultMetricsAddr),
		MetricsHandler: metricsHandler,
		Listening:      listening,
	})
}
