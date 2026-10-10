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

package observability

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"
)

// Check is one readiness condition; nil means ready.
type Check struct {
	Name  string
	Check func(context.Context) error
}

// Health serves /healthz (the process is up) and /readyz (every check
// passes) on LISTEN_ADDR (DESIGN-0001 § Observability, Health).
type Health struct {
	mu      sync.Mutex
	checks  []Check
	timeout time.Duration
}

// NewHealth returns a Health with no checks yet.
func NewHealth() *Health { return &Health{timeout: 5 * time.Second} }

// Add registers a readiness check.
func (h *Health) Add(c Check) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.checks = append(h.checks, c)
}

// Handler serves /healthz and /readyz.
func (h *Health) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeOK(w)
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if err := h.Ready(r.Context()); err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		writeOK(w)
	})
	return mux
}

func writeOK(w http.ResponseWriter) {
	if _, err := io.WriteString(w, "ok\n"); err != nil {
		return // the client went away
	}
}

// Ready runs every check and joins their failures.
func (h *Health) Ready(ctx context.Context) error {
	h.mu.Lock()
	checks := append([]Check(nil), h.checks...)
	h.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, h.timeout)
	defer cancel()
	var errs []error
	for _, c := range checks {
		if err := c.Check(ctx); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", c.Name, err))
		}
	}
	return errors.Join(errs...)
}

// Serve serves h on addr until ctx ends, then shuts down within five
// seconds. It returns once the listener is closed.
func Serve(ctx context.Context, addr string, h http.Handler) error {
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("observability: listen %s: %w", addr, err)
	}
	return ServeListener(ctx, ln, h)
}

// ServeListener is Serve on an open listener.
func ServeListener(ctx context.Context, ln net.Listener, h http.Handler) error {
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(sctx); err != nil {
		return fmt.Errorf("observability: shutdown: %w", err)
	}
	if err := <-errc; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
