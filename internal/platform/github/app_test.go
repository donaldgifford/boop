package github_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/time/rate"

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
