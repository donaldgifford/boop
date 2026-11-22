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

package api

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

// DefaultDrainTimeout bounds how long a stopping API waits for requests
// in flight.
const DefaultDrainTimeout = 10 * time.Second

// RunOptions configures Run.
type RunOptions struct {
	Handler Options
	// Ready are the readiness checks beyond "stopping": the store's
	// schema and OIDC discovery.
	Ready []observability.Check

	ListenAddr     string
	MetricsAddr    string
	MetricsHandler http.Handler
	// DrainTimeout bounds the graceful stop; zero is DefaultDrainTimeout.
	DrainTimeout time.Duration
	// Listening, when set, receives the bound listen address.
	Listening func(addr string)
}

// Run serves the API under BaseURL with /healthz and /readyz on
// ListenAddr, and /metrics on MetricsAddr, until ctx ends. It then fails
// readiness and drains requests in flight within DrainTimeout.
func Run(ctx context.Context, o *RunOptions) error {
	log := o.Handler.Logger
	if log == nil {
		log = slog.Default()
		o.Handler.Logger = log
	}
	drain := o.DrainTimeout
	if drain <= 0 {
		drain = DefaultDrainTimeout
	}
	h, err := New(&o.Handler)
	if err != nil {
		return fmt.Errorf("api: %w", err)
	}

	var stopping atomic.Bool
	health := newHealth(o.Ready, &stopping)
	mux := http.NewServeMux()
	mux.Handle("GET /healthz", health)
	mux.Handle("GET /readyz", health)
	mux.Handle(BaseURL+"/", h)

	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", o.ListenAddr)
	if err != nil {
		return fmt.Errorf("api: listen %s: %w", o.ListenAddr, err)
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
	if st := o.Handler.Status; st != nil {
		wg.Go(func() { st.Run(metricsCtx) })
	}
	if o.Listening != nil {
		o.Listening(ln.Addr().String())
	}
	log.Info("api started", "addr", ln.Addr().String(), "base", BaseURL)

	select {
	case err := <-errc:
		return fmt.Errorf("api: serve: %w", err)
	case <-ctx.Done():
	}
	stopping.Store(true)
	log.Info("stopping: draining requests in flight", "drain_timeout", drain)
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), drain)
	defer cancel()
	if err := srv.Shutdown(sctx); err != nil {
		return fmt.Errorf("api: shutdown: %w", err)
	}
	if err := <-errc; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("api: serve: %w", err)
	}
	log.Info("api stopped")
	return nil
}

// newHealth serves /healthz and /readyz: ready while every check passes
// and the API is not stopping.
func newHealth(checks []observability.Check, stopping *atomic.Bool) http.Handler {
	health := observability.NewHealth()
	for _, c := range checks {
		health.Add(c)
	}
	health.Add(observability.Check{Name: "stopping", Check: func(context.Context) error {
		if stopping.Load() {
			return errors.New("stopping")
		}
		return nil
	}})
	return health.Handler()
}
