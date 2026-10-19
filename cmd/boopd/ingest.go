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
	"github.com/donaldgifford/boop/internal/ingest"
	"github.com/donaldgifford/boop/internal/observability"
	"github.com/donaldgifford/boop/internal/temporal"
)

// runIngest is `boopd ingest --config <file>`: the ingest role until
// SIGTERM or SIGINT, then a graceful stop (DESIGN-0003 § The ingest
// role).
func runIngest(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("ingest", flag.ContinueOnError)
	fs.SetOutput(stderr)
	path := fs.String("config", defaultConfigPath, "config file")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return exitUsage
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	if err := runIngestRole(ctx, *path, stdout, stderr, nil); err != nil {
		return write(stderr, exitInvalid, "boopd ingest: %v\n", err)
	}
	return exitOK
}

// runIngestRole loads the config, reads only the webhook secrets (never
// an App key), dials Temporal and serves deliveries. listening, when
// set, receives the bound listen address.
func runIngestRole(ctx context.Context, path string, stdout, stderr io.Writer, listening func(string)) error {
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
	if len(cfg.WebhookApps()) == 0 {
		return ingest.ErrNoWebhookApp
	}
	if err := cfg.ReadWebhookSecrets(); err != nil {
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

	tcfg, err := temporal.ConfigFromEnv()
	if err != nil {
		return err
	}
	c, err := temporal.Dial(ctx, &tcfg, temporal.DialOptions{Logger: log, MeterProvider: mp})
	if err != nil {
		return err
	}
	defer c.Close()

	log = log.With("pod", os.Getenv("POD_NAME"))
	return ingest.Run(ctx, &ingest.RunOptions{
		Handler: ingest.Options{
			Config:    cfg,
			Temporal:  c,
			TaskQueue: tcfg.TaskQueue,
			Metrics:   metrics,
			Logger:    log,
		},
		TemporalReady:  func(ctx context.Context) error { return temporal.Ping(ctx, c) },
		ListenAddr:     envOr("LISTEN_ADDR", defaultListenAddr),
		MetricsAddr:    envOr("METRICS_ADDR", defaultMetricsAddr),
		MetricsHandler: metricsHandler,
		Listening:      listening,
	})
}
