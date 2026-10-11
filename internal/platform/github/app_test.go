package github_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/time/rate"

	"github.com/donaldgifford/boop/internal/platform"
	ghclient "github.com/donaldgifford/boop/internal/platform/github"
)

// appServer records every request so tests can assert paths, methods,
// headers and bodies.
type appServer struct {
	mu       sync.Mutex
	requests []recorded
	handle   func(w http.ResponseWriter, r *http.Request, body string)
}

type recorded struct {
	Method string
	Path   string
	Query  string
	Auth   string
	Body   string
}

func (s *appServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	rec := recorded{
		Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery,
		Auth: r.Header.Get("Authorization"), Body: string(body),
	}
	s.mu.Lock()
	s.requests = append(s.requests, rec)
	s.mu.Unlock()
	s.handle(w, r, rec.Body)
}

func (s *appServer) log() []recorded {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]recorded(nil), s.requests...)
}

func newAppServer(t *testing.T, handle func(w http.ResponseWriter, r *http.Request, body string)) (*appServer, *httptest.Server) {
	t.Helper()
	s := &appServer{handle: handle}
	srv := httptest.NewServer(s)
	t.Cleanup(srv.Close)
	return s, srv
}

func newTestAppClient(t *testing.T, srv *httptest.Server) *ghclient.AppClient {
	t.Helper()
	c, err := ghclient.NewAppClient(1, generatePEM(t), srv.URL+"/", ghclient.WithAppRateLimit(rate.Inf, 1))
	if err != nil {
		t.Fatalf("NewAppClient: %v", err)
	}
	return c
}

func TestNewAppClient_Validates(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		appID int64
		key   []byte
	}{
		{name: "no app id", key: []byte("k")},
		{name: "no key", appID: 1},
		{name: "bad key", appID: 1, key: []byte("not a pem")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := ghclient.NewAppClient(tt.appID, tt.key, ""); err == nil {
				t.Errorf("NewAppClient(%d, %q) err = nil, want error", tt.appID, tt.key)
			}
		})
	}
}

func TestListInstallations_Pages(t *testing.T) {
	t.Parallel()

	var srvURL string
	s, srv := newAppServer(t, func(w http.ResponseWriter, r *http.Request, _ string) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v3/app/installations" {
			http.NotFound(w, r)
			return
		}
		switch r.URL.Query().Get("page") {
		case "", "1":
			w.Header().Set("Link", fmt.Sprintf(`<%s/api/v3/app/installations?page=2&per_page=100>; rel="next"`, srvURL))
			_, _ = w.Write([]byte(`[
  {"id":11,"account":{"login":"org-a"},"repository_selection":"all"},
  {"id":12,"account":{"login":"org-b"},"repository_selection":"selected","suspended_at":"2026-09-01T00:00:00Z"}
]`))
		case "2":
			_, _ = w.Write([]byte(`[{"id":13,"account":{"login":"user-c"},"repository_selection":"selected"}]`))
		default:
			http.NotFound(w, r)
		}
	})
	srvURL = srv.URL

	got, err := newTestAppClient(t, srv).ListInstallations(context.Background())
	if err != nil {
		t.Fatalf("ListInstallations() err = %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("ListInstallations() = %d installations, want 3", len(got))
	}
	if got[0].ID != 11 || got[0].Account != "org-a" || got[0].RepositorySelection != "all" || got[0].Suspended() {
		t.Errorf("installation 0 = %+v, want active org-a/all", got[0])
	}
	want := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	if !got[1].SuspendedAt.Equal(want) || !got[1].Suspended() {
		t.Errorf("installation 1 SuspendedAt = %v, want %v", got[1].SuspendedAt, want)
	}
	if got[2].ID != 13 || got[2].Account != "user-c" {
		t.Errorf("installation 2 = %+v, want 13/user-c", got[2])
	}

	log := s.log()
	if len(log) != 2 {
		t.Fatalf("server saw %d requests, want 2 pages", len(log))
	}
	for _, r := range log {
		if !strings.Contains(r.Query, "per_page=100") {
			t.Errorf("request %s?%s lacks per_page=100", r.Path, r.Query)
		}
		if !strings.HasPrefix(r.Auth, "Bearer ") {
			t.Errorf("request Authorization = %q, want App JWT bearer", r.Auth)
		}
	}
}

func TestMint_ScopesToRepositoryIDs(t *testing.T) {
	t.Parallel()

	// A long, opaque token: callers must not assume a length.
	longToken := "ghs_" + strings.Repeat("x", 300)
	s, srv := newAppServer(t, func(w http.ResponseWriter, r *http.Request, _ string) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v3/app/installations/42/access_tokens" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"token":"` + longToken + `","expires_at":"2026-10-10T13:00:00Z"}`))
	})

	tok, exp, err := newTestAppClient(t, srv).Mint(context.Background(), 42, []int64{1001, 2002})
	if err != nil {
		t.Fatalf("Mint() err = %v", err)
	}
	if tok != longToken {
		t.Errorf("Mint() token len = %d, want %d", len(tok), len(longToken))
	}
	if want := time.Date(2026, 10, 10, 13, 0, 0, 0, time.UTC); !exp.Equal(want) {
		t.Errorf("Mint() expiresAt = %v, want %v", exp, want)
	}

	log := s.log()
	if len(log) != 1 {
		t.Fatalf("server saw %d requests, want 1", len(log))
	}
	var body struct {
		RepositoryIDs []int64 `json:"repository_ids"`
	}
	if err := json.Unmarshal([]byte(log[0].Body), &body); err != nil {
		t.Fatalf("mint body %q: %v", log[0].Body, err)
	}
	if !slices.Equal(body.RepositoryIDs, []int64{1001, 2002}) {
		t.Errorf("mint body repository_ids = %v, want [1001 2002]", body.RepositoryIDs)
	}
	var raw map[string]any
	_ = json.Unmarshal([]byte(log[0].Body), &raw)
	if len(raw) != 1 {
		t.Errorf("mint body = %s, want only repository_ids", log[0].Body)
	}
	if !strings.HasPrefix(log[0].Auth, "Bearer ") || log[0].Auth == "Bearer "+longToken {
		t.Errorf("mint Authorization = %q, want the App JWT", log[0].Auth)
	}
}

func TestMint_Rejects(t *testing.T) {
	t.Parallel()
	_, srv := newAppServer(t, func(w http.ResponseWriter, _ *http.Request, _ string) {
		http.Error(w, `{"message":"Bad credentials"}`, http.StatusUnauthorized)
	})
	c := newTestAppClient(t, srv)

	tests := []struct {
		name    string
		inst    int64
		repoIDs []int64
		wantErr error
	}{
		{name: "no installation", repoIDs: []int64{1}},
		{name: "no repositories", inst: 1},
		{name: "unauthorized", inst: 1, repoIDs: []int64{1}, wantErr: platform.ErrUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, _, err := c.Mint(context.Background(), tt.inst, tt.repoIDs)
			if err == nil {
				t.Fatalf("Mint(%d, %v) err = nil, want error", tt.inst, tt.repoIDs)
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Errorf("Mint(%d, %v) err = %v, want %v", tt.inst, tt.repoIDs, err, tt.wantErr)
			}
		})
	}
}

func TestRevoke_DeletesWithTheToken(t *testing.T) {
	t.Parallel()
	s, srv := newAppServer(t, func(w http.ResponseWriter, r *http.Request, _ string) {
		if r.Method != http.MethodDelete || r.URL.Path != "/api/v3/installation/token" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	if err := newTestAppClient(t, srv).Revoke(context.Background(), "ghs_minted"); err != nil {
		t.Fatalf("Revoke() err = %v", err)
	}
	log := s.log()
	if len(log) != 1 {
		t.Fatalf("server saw %d requests, want 1", len(log))
	}
	if log[0].Method != http.MethodDelete || log[0].Path != "/api/v3/installation/token" {
		t.Errorf("revoke request = %s %s, want DELETE /installation/token", log[0].Method, log[0].Path)
	}
	if log[0].Auth != "Bearer ghs_minted" {
		t.Errorf("revoke Authorization = %q, want Bearer ghs_minted", log[0].Auth)
	}
}

func TestRevoke_EmptyToken(t *testing.T) {
	t.Parallel()
	_, srv := newAppServer(t, func(w http.ResponseWriter, r *http.Request, _ string) { http.NotFound(w, r) })
	if err := newTestAppClient(t, srv).Revoke(context.Background(), ""); err == nil {
		t.Error(`Revoke("") err = nil, want error`)
	}
}
