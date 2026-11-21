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

// Package api is boopd's API role (DESIGN-0004 § The API): an
// oapi-codegen strict server for api/openapi.yaml behind OIDC
// authentication and owner-scoped authorisation over the store's
// read-only role. Every read is scoped in SQL by the caller's visible
// owners (installation account logins), and an owner or repository
// outside them answers 404, exactly like an unknown one. Actions also
// need an operator group, else 403.
//
// The middleware order is request ID and access log (with the
// boopd_api_requests_total and boopd_api_request_seconds metrics), panic
// recovery, authentication, then authorisation; the operations the spec
// declares with `security: []` skip the last two.
//
// Lists page by keyset: the cursor is base64url JSON of the last row's
// sort key plus a hash of the request's filters, so a cursor replayed
// under different filters is refused 400.
//
// problem.go, cursor.go, middleware.go, authn.go, server.go, apitest and
// oidctest are copied from repo-guardian at a72faf9 (each file says what
// changed); boop does not import repo-guardian.
package api

import (
	"context"
)

// Claims are the verified token claims the API uses.
type Claims struct {
	Subject string
	// Client is the azp of the token; the authz clients mapping keys on it.
	Client string
	Name   string
	Groups []string
}

// Principal is an authenticated, authorised caller.
type Principal struct {
	Claims

	// Visible is the owners the caller may see.
	Visible Scope
	// Operator is whether the caller may POST actions.
	Operator bool
}

type (
	claimsKey    struct{}
	principalKey struct{}
)

func withClaims(ctx context.Context, c *Claims) context.Context {
	return context.WithValue(ctx, claimsKey{}, c)
}

func claimsFrom(ctx context.Context) (*Claims, bool) {
	c, ok := ctx.Value(claimsKey{}).(*Claims)
	return c, ok
}

func withPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// principalFrom returns the request's principal. Handlers behind the
// authz middleware always have one; a missing one is a wiring bug and
// the handler must fail, never default to a wider scope.
func principalFrom(ctx context.Context) (*Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(*Principal)
	return p, ok
}
