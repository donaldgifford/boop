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

	"k8s.io/client-go/kubernetes"

	"github.com/donaldgifford/boop/internal/config"
	"github.com/donaldgifford/boop/internal/kube"
	"github.com/donaldgifford/boop/internal/observability"
	"github.com/donaldgifford/boop/internal/temporal"
	"github.com/donaldgifford/boop/internal/worker"
	"github.com/donaldgifford/boop/internal/workflows"
)

// Default listen addresses, matching the chart's config.port and
// config.metricsPort.
const (
	defaultListenAddr  = ":8080"
	defaultMetricsAddr = ":9090"
	defaultConfigPath  = "/etc/boopd/config.hcl"
)

// runWorker is `boopd worker --config <file>`: the worker role until
// SIGTERM or SIGINT, then a graceful stop.
func runWorker(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("worker", flag.ContinueOnError)
	fs.SetOutput(stderr)
	path := fs.String("config", defaultConfigPath, "config file")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return exitUsage
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	if err := runWorkerRole(ctx, *path, stdout, stderr); err != nil {
		return write(stderr, exitInvalid, "boopd worker: %v\n", err)
	}
	return exitOK
}

func runWorkerRole(ctx context.Context, path string, stdout, stderr io.Writer) error {
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
	if err := cfg.ReadSecrets(); err != nil {
		return err
	}

	mp, metricsHandler, err := observability.NewMeterProvider(temporal.MetricViews()...)
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
	if err := temporal.CheckServerVersion(ctx, c, temporal.MinServerVersion); err != nil {
		return err
	}
	wc, err := temporal.WorkerConfigFromEnv(&tcfg)
	if err != nil {
		return err
	}
	wc.StopTimeout = workflows.RunStartToClose

	rc, err := kube.RestConfig()
	if err != nil {
		return err
	}
	cs, err := kubernetes.NewForConfig(kube.Instrument(rc, metrics))
	if err != nil {
		return fmt.Errorf("kubernetes client: %w", err)
	}

	return worker.Run(ctx, &worker.Options{
		Config:         cfg,
		Temporal:       c,
		Namespace:      tcfg.Namespace,
		Worker:         wc,
		Clientset:      cs,
		Logger:         log.With("pod", os.Getenv("POD_NAME")),
		Metrics:        metrics,
		ListenAddr:     envOr("LISTEN_ADDR", defaultListenAddr),
		MetricsAddr:    envOr("METRICS_ADDR", defaultMetricsAddr),
		MetricsHandler: metricsHandler,
	})
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
