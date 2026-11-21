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

// Copied from repo-guardian internal/api/server.go at a72faf9.
// Changes: the Reader is boop's store; the otelhttp wrapper is gone
// (requestLog records the request metrics); the anonymous principal is
// no operator; there is no status cache or rechecker yet (Phase 10).

package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/donaldgifford/boop/internal/api/gen"
	"github.com/donaldgifford/boop/internal/store"
)

// BaseURL is where the API is mounted; it matches the spec's server URL.
const BaseURL = "/api/v1"

// Reader is the API's read side. *store.Store satisfies it.
//
// Every method is scoped in SQL by scope. Methods that read one resource
// return store.ErrNotFound when it is unknown or outside scope; the two
// are indistinguishable by design.
type Reader interface {
	Ping(ctx context.Context) error
	Owners(ctx context.Context, scope store.Scope) ([]string, error)
}

// Options configures the API handler.
type Options struct {
	Reader Reader

	// Authn verifies tokens. Nil disables authentication: every caller
	// is an anonymous principal who sees every owner and is no operator.
	Authn *Authenticator
	Authz *AuthzConfig

	// StatusRequiresAuth is api.status_public = false: /status then needs
	// a valid token like every other route, though no visible owner.
	StatusRequiresAuth bool

	Logger *slog.Logger
	// Metrics records the request and auth-failure metrics; nil records
	// nothing.
	Metrics Metrics
	// Now overrides the clock in tests.
	Now func() time.Time
}

// New returns the API handler, middleware outermost first (DESIGN-0004
// § The API):
//
//  1. request ID, access log and request metrics;
//  2. panic recovery;
//  3. authentication;
//  4. authorisation.
//
// The operations the spec declares with `security: []` are served
// around steps 3 and 4; every other route passes both. Authentication
// runs outside the generated wrapper so an anonymous request is refused
// 401 before parameter binding can answer 400.
func New(opts *Options) (http.Handler, error) {
	o := *opts
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	if o.Metrics == nil {
		o.Metrics = nopMetrics{}
	}
	if o.Authz == nil {
		o.Authz = &AuthzConfig{}
	}

	public, err := publicRoutes(o.StatusRequiresAuth)
	if err != nil {
		return nil, err
	}

	strict := gen.NewStrictHandlerWithOptions(&server{opts: o}, []gen.StrictMiddlewareFunc{requireVisibleOwners},
		gen.StrictHTTPServerOptions{
			RequestErrorHandlerFunc:  badRequest,
			ResponseErrorHandlerFunc: responseError(o.Logger),
		})

	inner := http.NewServeMux()
	inner.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { writeProblem(w, r, http.StatusNotFound, "") })

	routes := gen.HandlerWithOptions(strict, gen.StdHTTPServerOptions{
		BaseURL:          BaseURL,
		BaseRouter:       inner,
		Middlewares:      []gen.MiddlewareFunc{labelRoute},
		ErrorHandlerFunc: badRequest,
	})

	var protected http.Handler
	if o.Authn != nil {
		protected = o.Authn.Middleware(authorize(o.Authz)(routes))
	} else {
		protected = anonymous(routes)
	}

	outer := http.NewServeMux()
	outer.Handle("/", protected)
	for _, route := range public {
		outer.Handle(route, routes)
	}

	var h http.Handler = outer
	h = recoverer(o.Logger)(h)
	h = requestLog(o.Logger, o.Metrics)(h)
	return h, nil
}

// statusRoute is the status page's pattern, public unless
// api.status_public = false.
const statusRoute = "GET " + BaseURL + "/status"

// publicRoutes lists the spec's operations declared `security: []` as
// ServeMux patterns under BaseURL, less /status when it requires auth.
func publicRoutes(statusRequiresAuth bool) ([]string, error) {
	spec, err := gen.GetSpec()
	if err != nil {
		return nil, err
	}
	var out []string
	for path, item := range spec.Paths.Map() {
		for method, op := range item.Operations() {
			route := method + " " + BaseURL + path
			if op.Security != nil && len(*op.Security) == 0 && (route != statusRoute || !statusRequiresAuth) {
				out = append(out, route)
			}
		}
	}
	return out, nil
}

// anonymous is the principal of every route when authentication is off:
// it sees every owner and is no operator. Public routes carry no
// principal at all.
func anonymous(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := &Principal{Claims: Claims{Subject: "anonymous", Name: "anonymous"}, Visible: ScopeAll()}
		next.ServeHTTP(w, r.WithContext(withPrincipal(r.Context(), p)))
	})
}

// ownerFree operations answer a caller who sees no owner: /me says so,
// and the status page is aggregate-only.
var ownerFree = map[string]bool{"GetMe": true, "GetStatus": true}

// requireVisibleOwners refuses a principal that can see no owner on
// every operation but the ownerFree ones; /me tells the UI to show "ask
// for access".
func requireVisibleOwners(f gen.StrictHandlerFunc, operationID string) gen.StrictHandlerFunc {
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request, req any) (any, error) {
		if p, ok := principalFrom(ctx); ok && p.Visible.Empty() && !ownerFree[operationID] {
			return nil, statusError(http.StatusForbidden, "no visible owners")
		}
		return f(ctx, w, r, req)
	}
}

// httpError is a handler failure with a status the client should see.
type httpError struct {
	status int
	// typ is the problem type; empty is about:blank.
	typ    string
	detail string
}

func (e *httpError) Error() string { return e.detail }

func statusError(status int, detail string) error { return &httpError{status: status, detail: detail} }

// typedError is a problem whose type names the condition, so a client
// can tell one 409 from another.
func typedError(status int, typ, detail string) error {
	return &httpError{status: status, typ: typ, detail: detail}
}

// errNotFound is an unknown resource, or one outside the caller's
// scope: the two answer alike.
var errNotFound = statusError(http.StatusNotFound, "")

func badRequest(w http.ResponseWriter, r *http.Request, err error) {
	writeProblem(w, r, http.StatusBadRequest, err.Error())
}

// responseError writes a handler error as a problem. Only an httpError's
// detail reaches the client; anything else is logged and answered 500.
func responseError(logger *slog.Logger) func(http.ResponseWriter, *http.Request, error) {
	return func(w http.ResponseWriter, r *http.Request, err error) {
		var he *httpError
		if errors.As(err, &he) {
			writeTypedProblem(w, r, he.status, he.typ, he.detail)
			return
		}
		logger.ErrorContext(r.Context(), "api handler failed", "request_id", requestIDFrom(r.Context()), "error", err)
		writeProblem(w, r, http.StatusInternalServerError, "")
	}
}
