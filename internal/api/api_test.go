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

package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/donaldgifford/boop/api"
	boopapi "github.com/donaldgifford/boop/internal/api"
	"github.com/donaldgifford/boop/internal/api/apitest"
	"github.com/donaldgifford/boop/internal/api/gen"
	"github.com/donaldgifford/boop/internal/api/oidctest"
	"github.com/donaldgifford/boop/internal/store"
	"github.com/donaldgifford/boop/internal/store/db"
)

const audience = "boopd-api"

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// ownerReader knows two owners with one repository each, and answers
// Owners and Repositories by filtering them over the scope, the way the
// SQL predicate does. It records every scope. The other reads run
// against Postgres (TestAPI_* integration tests); calling one here
// panics on the nil embedded Reader.
type ownerReader struct {
	boopapi.Reader

	mu     sync.Mutex
	scopes []store.Scope
}

var fixtureRepos = []db.ListRepositoriesRow{
	{ID: 1, InstallationID: 10, Owner: "acme-web", Slug: "acme-web/site", State: "idle", SortSlug: "acme-web/site"},
	{ID: 2, InstallationID: 20, Owner: "acme-pay", Slug: "acme-pay/ledger", State: "idle", SortSlug: "acme-pay/ledger"},
}

func (r *ownerReader) Repositories(_ context.Context, scope store.Scope, p *db.ListRepositoriesParams) ([]db.ListRepositoriesRow, error) {
	r.mu.Lock()
	r.scopes = append(r.scopes, scope)
	r.mu.Unlock()
	var out []db.ListRepositoriesRow
	for i := range fixtureRepos {
		repo := &fixtureRepos[i]
		if (scope.All || slices.Contains(scope.Owners, repo.Owner)) && (p.ID == nil || *p.ID == repo.ID) {
			out = append(out, *repo)
		}
	}
	return out, nil
}

func (*ownerReader) Ping(context.Context) error { return nil }

func (r *ownerReader) Owners(_ context.Context, scope store.Scope) ([]string, error) {
	r.mu.Lock()
	r.scopes = append(r.scopes, scope)
	r.mu.Unlock()
	var out []string
	for _, o := range []string{"acme-pay", "acme-web"} {
		if scope.All || slices.Contains(scope.Owners, o) {
			out = append(out, o)
		}
	}
	return out, nil
}

// recorder is the API's Metrics, recorded.
type recorder struct {
	mu       sync.Mutex
	requests []string
	failures map[string]int
}

func (m *recorder) APIRequest(route, code string, _ time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requests = append(m.requests, route+" "+code)
}

func (m *recorder) APIAuthFailure(reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failures[reason]++
}

func (m *recorder) failed(reason string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.failures[reason]
}

const authzYAML = `
owners:
  platform: ["*"]
  team-web: [acme-web]
operators: [platform]
clients:
  ci-dashboard: [team-web]
`

// harness is an API with OIDC auth against a test issuer.
type harness struct {
	issuer  *oidctest.Issuer
	reader  *ownerReader
	metrics *recorder
	client  *apitest.Client
}

func newHarness(t *testing.T, opts ...func(*boopapi.Options)) *harness {
	t.Helper()
	iss := oidctest.Start(t, audience)
	path := filepath.Join(t.TempDir(), "authz.yaml")
	if err := os.WriteFile(path, []byte(authzYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	authz, err := boopapi.LoadAuthzConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	metrics := &recorder{failures: map[string]int{}}
	authn := boopapi.NewAuthenticator(&boopapi.AuthnConfig{
		Issuer: iss.URL, Audience: audience, NameClaim: "preferred_username", GroupsClaim: "groups",
	}, quiet, metrics)
	authn.Start(t.Context())
	reader := &ownerReader{}
	o := &boopapi.Options{Reader: reader, Authn: authn, Authz: authz, Logger: quiet, Metrics: metrics}
	for _, opt := range opts {
		opt(o)
	}
	return &harness{issuer: iss, reader: reader, metrics: metrics, client: apitest.New(t, o)}
}

func decode[T any](t *testing.T, r *apitest.Response) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(r.Body, &v); err != nil {
		t.Fatalf("decode %s: %v", r.Body, err)
	}
	return v
}

func TestMe_ReportsVisibilityAndOperator(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	tests := []struct {
		name     string
		token    string
		wantAll  bool
		want     []string
		operator bool
	}{
		{
			name: "platform sees every owner", token: h.issuer.Valid(t, "platform"), wantAll: true,
			want: []string{"acme-pay", "acme-web"}, operator: true,
		},
		{name: "team sees its owners", token: h.issuer.Valid(t, "team-web", "unknown-group"), want: []string{"acme-web"}},
		{name: "no matching group sees nothing", token: h.issuer.Valid(t, "unknown-group"), want: []string{}},
		{name: "no groups claim sees nothing", token: h.issuer.NoGroups(t), want: []string{}},
		{
			name: "machine client maps through azp", token: h.issuer.Mint(t, &oidctest.Token{Client: "ci-dashboard", NoGroups: true}),
			want: []string{"acme-web"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			resp := h.client.Get("/me", tt.token)
			if resp.Status != http.StatusOK {
				t.Fatalf("status = %d: %s", resp.Status, resp.Body)
			}
			me := decode[gen.Me](t, resp)
			if me.AllOwners != tt.wantAll || !slices.Equal(me.Owners, tt.want) || me.Operator != tt.operator || me.Subject == "" {
				t.Errorf("me = %+v, want all=%v owners=%v operator=%v", me, tt.wantAll, tt.want, tt.operator)
			}
		})
	}
}

// TestAuthn_RejectionsAre401WithReason: every refused token is a 401
// with a Bearer challenge and counts boopd_api_auth_failures_total under
// its own reason. An ID token is refused even when otherwise valid.
func TestAuthn_RejectionsAre401WithReason(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	tests := []struct {
		token, reason string
	}{
		{"", "missing"},
		{"not-a-jwt", "malformed"},
		{h.issuer.Expired(t), "expired"},
		{h.issuer.WrongAudience(t), "audience"},
		{h.issuer.WrongIssuer(t), "issuer"},
		{h.issuer.BadSignature(t), "signature"},
		{h.issuer.IDTokenShaped(t), "id_token"},
		{h.issuer.Mint(t, &oidctest.Token{HMAC: true}), "algorithm"},
		{h.issuer.Mint(t, &oidctest.Token{NotBefore: time.Now().Add(time.Hour)}), "not_yet_valid"},
	}
	for _, tt := range tests {
		before := h.metrics.failed(tt.reason)
		resp := h.client.Get("/me", tt.token)
		if resp.Status != http.StatusUnauthorized || !strings.HasPrefix(resp.Header.Get("WWW-Authenticate"), "Bearer") {
			t.Errorf("%s: status %d, WWW-Authenticate %q; want 401 with a Bearer challenge", tt.reason, resp.Status,
				resp.Header.Get("WWW-Authenticate"))
		}
		if h.metrics.failed(tt.reason) <= before {
			t.Errorf("%s: boopd_api_auth_failures_total{reason=%q} did not increment", tt.reason, tt.reason)
		}
	}
}

// TestAuthn_UnavailableBeforeDiscovery: until the issuer is discovered
// every protected request is 503, never served unauthenticated.
func TestAuthn_UnavailableBeforeDiscovery(t *testing.T) {
	t.Parallel()
	iss := oidctest.Start(t, audience)
	authn := boopapi.NewAuthenticator(&boopapi.AuthnConfig{Issuer: iss.URL, Audience: audience}, quiet, nil)
	c := apitest.New(t, &boopapi.Options{Reader: &ownerReader{}, Authn: authn, Logger: quiet})
	if resp := c.Get("/me", iss.Valid(t, "platform")); resp.Status != http.StatusServiceUnavailable {
		t.Errorf("/me before discovery = %d, want 503", resp.Status)
	}
}

func TestAuthn_ClockSkew(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	token := h.issuer.Mint(t, &oidctest.Token{Expiry: time.Now().Add(-30 * time.Second)})
	if resp := h.client.Get("/me", token); resp.Status != http.StatusOK {
		t.Errorf("token expired 30s ago = %d, want accepted within the 60s skew", resp.Status)
	}
}

// TestAuthz_Matrix: who passes the gates in front of each kind of
// operation. A refusal never reaches the handler; a pass does, and
// answers from the fake reader (recheck answers 503: no Temporal).
func TestAuthz_Matrix(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	platform, team, nobody := h.issuer.Valid(t, "platform"), h.issuer.Valid(t, "team-web"), h.issuer.Valid(t, "unknown-group")
	tests := []struct {
		name   string
		method string
		path   string
		token  string
		want   int
	}{
		{name: "no visible owner: list is 403", method: http.MethodGet, path: "/repositories", token: nobody, want: http.StatusForbidden},
		{name: "no visible owner: status answers", method: http.MethodGet, path: "/status", token: nobody, want: http.StatusOK},
		{name: "visible owner: list passes", method: http.MethodGet, path: "/repositories", token: team, want: http.StatusOK},
		{
			name: "invisible owner filter is 404", method: http.MethodGet, path: "/repositories?owner=acme-pay", token: team,
			want: http.StatusNotFound,
		},
		{
			name: "visible owner filter passes", method: http.MethodGet, path: "/repositories?owner=ACME-WEB", token: team,
			want: http.StatusOK,
		},
		{name: "invisible repository is 404", method: http.MethodGet, path: "/repositories/2", token: team, want: http.StatusNotFound},
		{name: "deliveries need every owner", method: http.MethodGet, path: "/deliveries", token: team, want: http.StatusForbidden},
		{name: "non-operator recheck is 403", method: http.MethodPost, path: "/repositories/1/recheck", token: team, want: http.StatusForbidden},
		{
			name: "no visible owner recheck is 403", method: http.MethodPost, path: "/repositories/1/recheck", token: nobody,
			want: http.StatusForbidden,
		},
		{
			name: "operator recheck of an unknown repository is 404", method: http.MethodPost, path: "/repositories/9/recheck",
			token: platform, want: http.StatusNotFound,
		},
		{
			name: "operator recheck passes the gates", method: http.MethodPost, path: "/repositories/1/recheck", token: platform,
			want: http.StatusServiceUnavailable,
		},
	}
	for _, tt := range tests {
		var resp *apitest.Response
		if tt.method == http.MethodPost {
			resp = h.client.Post(tt.path, tt.token)
		} else {
			resp = h.client.Get(tt.path, tt.token)
		}
		if resp.Status != tt.want {
			t.Errorf("%s: %s %s = %d, want %d: %s", tt.name, tt.method, tt.path, resp.Status, tt.want, resp.Body)
		}
		if resp.Status < 400 {
			continue
		}
		if p := decode[gen.Problem](t, resp); p.RequestId == nil || *p.RequestId == "" {
			t.Errorf("%s: problem without request_id: %s", tt.name, resp.Body)
		}
	}
}

// TestRepositories_ScopedInTheReader: the list is filtered by the scope
// the reader receives, never after it.
func TestRepositories_ScopedInTheReader(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	list := decode[gen.RepositoryList](t, h.client.Get("/repositories", h.issuer.Valid(t, "team-web")))
	if len(list.Items) != 1 || list.Items[0].Owner != "acme-web" {
		t.Errorf("team-web repositories = %+v, want acme-web's only", list.Items)
	}
	h.reader.mu.Lock()
	defer h.reader.mu.Unlock()
	if len(h.reader.scopes) == 0 || h.reader.scopes[0].All || !slices.Equal(h.reader.scopes[0].Owners, []string{"acme-web"}) {
		t.Errorf("reader scopes = %+v, want acme-web only", h.reader.scopes)
	}
}

func TestPublicRoutes(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	resp := h.client.Get("/openapi.yaml", "")
	if resp.Status != http.StatusOK || !bytes.Equal(resp.Body, api.Spec) {
		t.Errorf("openapi.yaml = %d, %.40s; want 200 and the embedded spec", resp.Status, resp.Body)
	}
	if resp := h.client.Get("/status", ""); resp.Status != http.StatusOK {
		t.Errorf("public /status = %d without a token, want 200", resp.Status)
	}

	closed := newHarness(t, func(o *boopapi.Options) { o.StatusRequiresAuth = true })
	if resp := closed.client.Get("/status", ""); resp.Status != http.StatusUnauthorized {
		t.Errorf("status_public = false: /status without a token = %d, want 401", resp.Status)
	}
	if resp := closed.client.Get("/openapi.yaml", ""); resp.Status != http.StatusOK {
		t.Errorf("status_public = false: openapi.yaml = %d, want 200", resp.Status)
	}
}

// TestAuthDisabled_IsAnonymousAllOwnersNoOperator: with no Authn every
// caller sees every owner and is no operator.
func TestAuthDisabled_IsAnonymousAllOwnersNoOperator(t *testing.T) {
	t.Parallel()
	c := apitest.New(t, &boopapi.Options{Reader: &ownerReader{}, Logger: quiet})
	me := decode[gen.Me](t, c.Get("/me", ""))
	if !me.AllOwners || me.Operator || me.Subject != "anonymous" {
		t.Errorf("me = %+v, want the anonymous all-owners non-operator", me)
	}
	if resp := c.Post("/repositories/1/recheck", ""); resp.Status != http.StatusForbidden {
		t.Errorf("anonymous recheck = %d, want 403", resp.Status)
	}
}

// TestRequestMetrics_LabelByRoute: boopd_api_requests_total carries the
// matched pattern, never the raw path.
func TestRequestMetrics_LabelByRoute(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.client.Get("/repositories/42", h.issuer.Valid(t, "platform"))
	h.client.Get("/me", "")
	h.metrics.mu.Lock()
	defer h.metrics.mu.Unlock()
	want := []string{"GET /api/v1/repositories/{id} 404", "unmatched 401"}
	if !slices.Equal(h.metrics.requests, want) {
		t.Errorf("requests = %v, want %v", h.metrics.requests, want)
	}
}
