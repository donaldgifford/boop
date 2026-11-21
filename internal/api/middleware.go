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

// Copied from repo-guardian internal/api/middleware.go at a72faf9.
// Changes: requestLog also records boopd_api_requests_total and
// boopd_api_request_seconds by route and code (the route is the inner
// mux's pattern, reported back through the request's context), and
// authorize resolves owners and the operator flag (DESIGN-0004 OQ8).

package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"time"
)

const requestIDHeader = "X-Request-Id"

type (
	requestIDKey struct{}
	routeKey     struct{}
)

// validRequestID bounds an inbound X-Request-Id so a client cannot write
// arbitrary text into our logs.
var validRequestID = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

func requestIDFrom(ctx context.Context) string {
	if id, ok := ctx.Value(requestIDKey{}).(string); ok {
		return id
	}
	return ""
}

// unmatchedRoute labels a request no route matched, so a scan of
// arbitrary paths cannot grow the metric's label set.
const unmatchedRoute = "unmatched"

// routeLabel carries the matched pattern from the inner mux back to the
// access log.
type routeLabel struct{ pattern string }

// labelRoute records the inner mux's matched pattern for requestLog.
func labelRoute(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if l, ok := r.Context().Value(routeKey{}).(*routeLabel); ok && r.Pattern != "" {
			l.pattern = r.Pattern
		}
		next.ServeHTTP(w, r)
	})
}

// statusRecorder captures the response status for the access log.
type statusRecorder struct {
	http.ResponseWriter

	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

// requestLog assigns a request ID (reusing a well-formed inbound one),
// echoes it, writes one access-log line per request and records the
// request metrics.
func requestLog(logger *slog.Logger, metrics Metrics) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get(requestIDHeader)
			if !validRequestID.MatchString(id) {
				id = newRequestID()
			}
			w.Header().Set(requestIDHeader, id)

			rec := &statusRecorder{ResponseWriter: w}
			route := &routeLabel{pattern: unmatchedRoute}
			start := time.Now()
			ctx := context.WithValue(context.WithValue(r.Context(), requestIDKey{}, id), routeKey{}, route)
			next.ServeHTTP(rec, r.WithContext(ctx))
			elapsed := time.Since(start)
			if rec.status == 0 {
				rec.status = http.StatusOK
			}

			metrics.APIRequest(route.pattern, strconv.Itoa(rec.status), elapsed)
			logger.InfoContext(r.Context(), "api request",
				"request_id", id, "method", r.Method, "path", r.URL.Path, "route", route.pattern,
				"status", rec.status, "duration", elapsed)
		})
	}
}

func newRequestID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// recoverer turns a handler panic into a logged 500 problem.
func recoverer(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() { //nolint:contextcheck // the deferred recovery writes to r, which carries the context
				if v := recover(); v != nil {
					if v == http.ErrAbortHandler { //nolint:errorlint // sentinel compared as net/http does
						panic(v)
					}
					logger.ErrorContext(r.Context(), "api handler panic", "request_id", requestIDFrom(r.Context()), "panic", v)
					writeProblem(w, r, http.StatusInternalServerError, "")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// authorize resolves the authenticated claims to a Principal: its
// visible owners and whether it is an operator.
func authorize(authz *AuthzConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, ok := claimsFrom(r.Context())
			if !ok {
				writeProblem(w, r, http.StatusInternalServerError, "")
				return
			}
			p := &Principal{Claims: *claims, Visible: authz.Scope(claims), Operator: authz.Operator(claims)}
			next.ServeHTTP(w, r.WithContext(withPrincipal(r.Context(), p)))
		})
	}
}
