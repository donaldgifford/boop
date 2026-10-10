package github_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"golang.org/x/time/rate"

	"github.com/donaldgifford/boop/internal/platform"
	ghclient "github.com/donaldgifford/boop/internal/platform/github"
)

// graphqlServer answers the config probe. configs maps node id to the
// file text; a node in missing returns null; every other node is a
// repository without the file. It logs each query's variables.
type graphqlServer struct {
	mu      sync.Mutex
	queries []graphqlVars
	configs map[string]string
	missing map[string]bool
	cost    int
}

type graphqlVars struct {
	IDs  []string `json:"ids"`
	Expr string   `json:"expr"`
}

func (s *graphqlServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != "/api/graphql" {
		http.NotFound(w, r)
		return
	}
	var req struct {
		Query     string      `json:"query"`
		Variables graphqlVars `json:"variables"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	s.queries = append(s.queries, req.Variables)
	s.mu.Unlock()

	nodes := make([]any, 0, len(req.Variables.IDs))
	for _, id := range req.Variables.IDs {
		switch text, ok := s.configs[id]; {
		case s.missing[id]:
			nodes = append(nodes, nil)
		case ok:
			nodes = append(nodes, map[string]any{"id": id, "config": map[string]any{"byteSize": len(text), "text": text}})
		default:
			nodes = append(nodes, map[string]any{"id": id, "config": nil})
		}
	}
	if err := json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
		"rateLimit": map[string]any{"cost": s.cost, "remaining": 4990, "resetAt": "2026-10-10T13:00:00Z"},
		"nodes":     nodes,
	}}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func newProbeClient(t *testing.T, h http.Handler) *ghclient.Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := ghclient.NewWithToken(ghclient.TokenAuth{Token: "t", BaseURL: srv.URL + "/"}, ghclient.WithRateLimit(rate.Inf, 1))
	if err != nil {
		t.Fatalf("NewWithToken: %v", err)
	}
	return c
}

func nodeIDs(n int) []string {
	ids := make([]string, n)
	for i := range ids {
		ids[i] = fmt.Sprintf("R_%03d", i)
	}
	return ids
}

func TestProbeConfig_Batches(t *testing.T) {
	t.Parallel()
	tests := []struct {
		repos       int
		wantQueries int
		wantSizes   []int
	}{
		{repos: 1, wantQueries: 1, wantSizes: []int{1}},
		{repos: 100, wantQueries: 1, wantSizes: []int{100}},
		{repos: 101, wantQueries: 2, wantSizes: []int{100, 1}},
		{repos: 250, wantQueries: 3, wantSizes: []int{100, 100, 50}},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprint(tt.repos), func(t *testing.T) {
			t.Parallel()
			fake := &graphqlServer{cost: 2}
			res, err := newProbeClient(t, fake).ProbeConfig(context.Background(), nodeIDs(tt.repos), "renovate.json")
			if err != nil {
				t.Fatalf("ProbeConfig(%d ids) err = %v", tt.repos, err)
			}
			if len(fake.queries) != tt.wantQueries || res.Queries != tt.wantQueries {
				t.Fatalf("ProbeConfig(%d ids) sent %d queries (result says %d), want %d",
					tt.repos, len(fake.queries), res.Queries, tt.wantQueries)
			}
			sizes := make([]int, 0, len(fake.queries))
			for _, q := range fake.queries {
				sizes = append(sizes, len(q.IDs))
				if q.Expr != "HEAD:renovate.json" {
					t.Errorf("query expr = %q, want HEAD:renovate.json", q.Expr)
				}
			}
			if !slices.Equal(sizes, tt.wantSizes) {
				t.Errorf("batch sizes = %v, want %v", sizes, tt.wantSizes)
			}
			if want := 2 * tt.wantQueries; res.Cost != want {
				t.Errorf("ProbeConfig(%d ids).Cost = %d, want %d (summed rateLimit.cost)", tt.repos, res.Cost, want)
			}
			if res.Remaining != 4990 || res.ResetAt.IsZero() {
				t.Errorf("ProbeConfig() Remaining/ResetAt = %d/%v, want 4990/non-zero", res.Remaining, res.ResetAt)
			}
			if len(res.Configs) != tt.repos {
				t.Errorf("ProbeConfig() answered %d repos, want %d", len(res.Configs), tt.repos)
			}
		})
	}
}

func TestProbeConfig_Answers(t *testing.T) {
	t.Parallel()
	fake := &graphqlServer{
		cost: 1,
		configs: map[string]string{
			"withExtends": `{"extends":["github>boop-bot/renovate-config:python"]}`,
			"noExtends":   `{"labels":["deps"]}`,
			"broken":      `{not json`,
		},
		missing: map[string]bool{"gone": true},
	}
	ids := []string{"withExtends", "noExtends", "broken", "absent", "gone"}
	res, err := newProbeClient(t, fake).ProbeConfig(context.Background(), ids, ".github/renovate.json")
	if err != nil {
		t.Fatalf("ProbeConfig() err = %v", err)
	}
	if fake.queries[0].Expr != "HEAD:.github/renovate.json" {
		t.Errorf("query expr = %q, want the configured path", fake.queries[0].Expr)
	}
	want := map[string]ghclient.ConfigProbe{
		"withExtends": {Exists: true, Extends: []string{"github>boop-bot/renovate-config:python"}},
		"noExtends":   {Exists: true},
		"broken":      {Exists: true},
		"absent":      {},
		"gone":        {},
	}
	for id, w := range want {
		got := res.Configs[id]
		if got.Exists != w.Exists || !slices.Equal(got.Extends, w.Extends) {
			t.Errorf("ProbeConfig()[%s] = %+v, want %+v", id, got, w)
		}
	}
}

func TestProbeConfig_ErrorsWithoutData(t *testing.T) {
	t.Parallel()
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":null,"errors":[{"message":"something went wrong"}]}`))
	})
	_, err := newProbeClient(t, h).ProbeConfig(context.Background(), []string{"a"}, "renovate.json")
	if !errors.Is(err, platform.ErrTransient) || !strings.Contains(err.Error(), "something went wrong") {
		t.Errorf("ProbeConfig() err = %v, want transient carrying the GraphQL message", err)
	}
}

func TestProbeConfig_Unauthorized(t *testing.T) {
	t.Parallel()
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"message":"Bad credentials"}`, http.StatusUnauthorized)
	})
	_, err := newProbeClient(t, h).ProbeConfig(context.Background(), []string{"a"}, "renovate.json")
	if !errors.Is(err, platform.ErrUnauthorized) {
		t.Errorf("ProbeConfig() err = %v, want ErrUnauthorized", err)
	}
}

func TestProbeConfigREST(t *testing.T) {
	t.Parallel()
	content := base64.StdEncoding.EncodeToString([]byte(`{"extends":["config:recommended"]}`))
	file := `{"type":"file","name":"renovate.json","size":34,"encoding":"base64","content":"` + content + `"}`
	var mu sync.Mutex
	var refs []string
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		refs = append(refs, r.URL.Path+"@"+r.URL.Query().Get("ref"))
		mu.Unlock()
		switch r.URL.Path {
		case "/api/v3/repos/o/has/contents/renovate.json":
			_, _ = w.Write([]byte(file))
		case "/api/v3/repos/o/broken/contents/renovate.json":
			http.Error(w, "boom", http.StatusBadGateway)
		default:
			http.NotFound(w, r)
		}
	})
	c := newProbeClient(t, h)

	repos := []platform.Repository{
		{NodeID: "N1", Slug: "o/has", DefaultBranch: "main"},
		{NodeID: "N2", Slug: "o/none", DefaultBranch: "trunk"},
	}
	res, err := c.ProbeConfigREST(context.Background(), repos, "")
	if err != nil {
		t.Fatalf("ProbeConfigREST() err = %v", err)
	}
	if got := res.Configs["N1"]; !got.Exists || !slices.Equal(got.Extends, []string{"config:recommended"}) {
		t.Errorf("ProbeConfigREST()[N1] = %+v, want present with config:recommended", got)
	}
	if got := res.Configs["N2"]; got.Exists {
		t.Errorf("ProbeConfigREST()[N2] = %+v, want absent", got)
	}
	if res.Queries != 2 || res.Cost != 0 {
		t.Errorf("ProbeConfigREST() Queries/Cost = %d/%d, want 2/0", res.Queries, res.Cost)
	}
	wantRefs := []string{"/api/v3/repos/o/has/contents/renovate.json@main", "/api/v3/repos/o/none/contents/renovate.json@trunk"}
	if !slices.Equal(refs, wantRefs) {
		t.Errorf("REST calls = %v, want %v", refs, wantRefs)
	}

	_, err = c.ProbeConfigREST(context.Background(), []platform.Repository{{NodeID: "N3", Slug: "o/broken", DefaultBranch: "main"}}, "")
	if !errors.Is(err, platform.ErrTransient) {
		t.Errorf("ProbeConfigREST(broken) err = %v, want ErrTransient", err)
	}
}
