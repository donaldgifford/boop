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

package ingest

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/donaldgifford/boop/internal/observability"
)

// DefaultDrainTimeout bounds the drain of in-flight deliveries on stop.
// The chart's grace period is above it.
const DefaultDrainTimeout = 15 * time.Second

// ErrNoWebhookApp is a config with no App that has a webhook block:
// there is nothing to ingest.
var ErrNoWebhookApp = errors.New("ingest: no app has a webhook block")

// RunOptions configure the ingest role.
type RunOptions struct {
	Handler Options
	// TemporalReady is the readiness check against Temporal.
	TemporalReady func(context.Context) error
	// ListenAddr serves the webhook path, /healthz and /readyz;
	// MetricsAddr serves /metrics when MetricsHandler is set.
	ListenAddr     string
	MetricsAddr    string
	MetricsHandler http.Handler
	// DrainTimeout bounds the drain on stop; DefaultDrainTimeout when
	// zero.
	DrainTimeout time.Duration
	// Listening, when set, receives the bound listen address once the
	// server accepts connections (tests listen on :0).
	Listening func(addr string)
}

// Run is the ingest role until ctx ends: serve deliveries, then stop
// gracefully. On stop /readyz fails first, then the server drains the
// deliveries in flight within DrainTimeout.
func Run(ctx context.Context, o *RunOptions) error {
	cfg := o.Handler.Config
	if len(cfg.WebhookApps()) == 0 {
		return ErrNoWebhookApp
	}
	log := o.Handler.Logger
	if log == nil {
		log = slog.Default()
	}
	drain := o.DrainTimeout
	if drain <= 0 {
		drain = DefaultDrainTimeout
	}

	var stopping atomic.Bool
	health := observability.NewHealth()
	health.Add(observability.Check{Name: "temporal", Check: o.TemporalReady})
	health.Add(observability.Check{Name: "stopping", Check: func(context.Context) error {
		if stopping.Load() {
			return errors.New("stopping")
		}
		return nil
	}})
	mux := http.NewServeMux()
	mux.Handle("GET /healthz", health.Handler())
	mux.Handle("GET /readyz", health.Handler())
	mux.Handle(cfg.Ingest.Path, NewHandler(&o.Handler))

	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", o.ListenAddr)
	if err != nil {
		return fmt.Errorf("ingest: listen %s: %w", o.ListenAddr, err)
	}
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()

	var wg sync.WaitGroup
	metricsCtx, stopMetrics := context.WithCancel(context.WithoutCancel(ctx))
	defer func() {
		stopMetrics()
		wg.Wait()
	}()
	if o.MetricsHandler != nil && o.MetricsAddr != "" {
		mm := http.NewServeMux()
		mm.Handle("GET /metrics", o.MetricsHandler)
		wg.Go(func() {
			if err := observability.Serve(metricsCtx, o.MetricsAddr, mm); err != nil {
				log.Error("server failed", "server", "metrics", "addr", o.MetricsAddr, "error", err)
			}
		})
	}
	if o.Listening != nil {
		o.Listening(ln.Addr().String())
	}
	log.Info("ingest started", "addr", ln.Addr().String(), "path", cfg.Ingest.Path, "apps", len(cfg.WebhookApps()))

	select {
	case err := <-errc:
		return fmt.Errorf("ingest: serve: %w", err)
	case <-ctx.Done():
	}
	stopping.Store(true)
	log.Info("stopping: draining deliveries in flight", "drain_timeout", drain)
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), drain)
	defer cancel()
	if err := srv.Shutdown(sctx); err != nil {
		return fmt.Errorf("ingest: shutdown: %w", err)
	}
	if err := <-errc; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("ingest: serve: %w", err)
	}
	log.Info("ingest stopped")
	return nil
}
