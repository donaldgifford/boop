package github_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"

	"golang.org/x/time/rate"

	"github.com/donaldgifford/boop/internal/platform"
	ghclient "github.com/donaldgifford/boop/internal/platform/github"
)

// graphqlServer answers the config probe. files maps node id to that
// repository's files (path to text); a node in missing returns null.
// Each aliased field c<i> answers the path in $e<i>. It logs each
// query's ids and expressions.
type graphqlServer struct {
	mu      sync.Mutex
	queries []probeQuery
	files   map[string]map[string]string
	trees   map[string][]string // node id -> paths that are directories
	missing map[string]bool
	cost    int
}

type probeQuery struct {
	ids   []string
	exprs []string // e0, e1, ... in order
}

func (s *graphqlServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != "/api/graphql" {
		http.NotFound(w, r)
		return
	}
	var req struct {
		Query     string                     `json:"query"`
		Variables map[string]json.RawMessage `json:"variables"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var q probeQuery
	_ = json.Unmarshal(req.Variables["ids"], &q.ids)
	for i := 0; ; i++ {
		raw, ok := req.Variables[fmt.Sprintf("e%d", i)]
		if !ok {
			break
		}
		var e string
		_ = json.Unmarshal(raw, &e)
		q.exprs = append(q.exprs, e)
		if !strings.Contains(req.Query, fmt.Sprintf("c%d: object(expression: $e%d)", i, i)) {
			http.Error(w, fmt.Sprintf("query lacks field c%d", i), http.StatusBadRequest)
			return
		}
	}
	s.mu.Lock()
	s.queries = append(s.queries, q)
	s.mu.Unlock()

	nodes := make([]any, 0, len(q.ids))
	for _, id := range q.ids {
		if s.missing[id] {
			nodes = append(nodes, nil)
			continue
		}
		node := map[string]any{"id": id}
		for i, e := range q.exprs {
			p := strings.TrimPrefix(e, "HEAD:")
			switch text, ok := s.files[id][p]; {
			case ok:
				node[fmt.Sprintf("c%d", i)] = map[string]any{"byteSize": len(text), "text": text}
			case slices.Contains(s.trees[id], p):
				node[fmt.Sprintf("c%d", i)] = map[string]any{} // a Tree matches no Blob fragment
			default:
				node[fmt.Sprintf("c%d", i)] = nil
			}
		}
		nodes = append(nodes, node)
	}
	if err := json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
		"rateLimit": map[string]any{"cost": s.cost, "remaining": 4990, "resetAt": "2026-10-10T13:00:00Z"},
		"nodes":     nodes,
	}}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// repoTree serves GET /repos/{owner}/{name}/contents/{path} for a set of
// repositories' files (slug to path to text): a directory lists its
// entries, a file returns its base64 content, anything else is a 404.
// fail maps a slug to a status every request for it returns. It logs
// each request as path@ref.
type repoTree struct {
	mu    sync.Mutex
	files map[string]map[string]string
	fail  map[string]int
	calls []string
}

func (t *repoTree) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	t.mu.Lock()
	t.calls = append(t.calls, r.URL.Path+"@"+r.URL.Query().Get("ref"))
	t.mu.Unlock()
	rest, ok := strings.CutPrefix(r.URL.Path, "/api/v3/repos/")
	parts := strings.SplitN(rest, "/", 4)
	if !ok || r.Method != http.MethodGet || len(parts) < 3 || parts[2] != "contents" {
		http.NotFound(w, r)
		return
	}
	slug := parts[0] + "/" + parts[1]
	if code := t.fail[slug]; code != 0 {
		http.Error(w, "boom", code)
		return
	}
	p := ""
	if len(parts) == 4 {
		p = strings.TrimSuffix(parts[3], "/")
	}
	files := t.files[slug]
	if text, ok := files[p]; ok {
		if err := json.NewEncoder(w).Encode(map[string]any{
			"type": "file", "name": path.Base(p), "path": p, "size": len(text),
			"encoding": "base64", "content": base64.StdEncoding.EncodeToString([]byte(text)),
		}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}
	entries := map[string]string{}
	for f := range files {
		rel := f
		if p != "" {
			var under bool
			if rel, under = strings.CutPrefix(f, p+"/"); !under {
				continue
			}
		}
		if name, _, nested := strings.Cut(rel, "/"); nested {
			entries[name] = "dir"
		} else {
			entries[name] = "file"
		}
	}
	if len(entries) == 0 && p != "" {
		http.NotFound(w, r)
		return
	}
	names := make([]string, 0, len(entries))
	for n := range entries {
		names = append(names, n)
	}
	sort.Strings(names)
	list := make([]map[string]any, 0, len(names))
	for _, n := range names {
		list = append(list, map[string]any{"type": entries[n], "name": n, "path": path.Join(p, n)})
	}
	if err := json.NewEncoder(w).Encode(list); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func newProbeClient(t *testing.T, h http.Handler, opts ...ghclient.ClientOption) *ghclient.Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	opts = append([]ghclient.ClientOption{ghclient.WithRateLimit(rate.Inf, 1)}, opts...)
	c, err := ghclient.NewWithToken(ghclient.TokenAuth{Token: "t", BaseURL: srv.URL + "/"}, opts...)
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

func defaultExprs() []string {
	out := make([]string, len(platform.DefaultConfigPaths))
	for i, p := range platform.DefaultConfigPaths {
		out[i] = "HEAD:" + p
	}
	return out
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
			res, err := newProbeClient(t, fake).ProbeConfig(context.Background(), nodeIDs(tt.repos), nil)
			if err != nil {
				t.Fatalf("ProbeConfig(%d ids) err = %v", tt.repos, err)
			}
			if len(fake.queries) != tt.wantQueries || res.Queries != tt.wantQueries {
				t.Fatalf("ProbeConfig(%d ids) sent %d queries (result says %d), want %d",
					tt.repos, len(fake.queries), res.Queries, tt.wantQueries)
			}
			sizes := make([]int, 0, len(fake.queries))
			for _, q := range fake.queries {
				sizes = append(sizes, len(q.ids))
				if !slices.Equal(q.exprs, defaultExprs()) {
					t.Errorf("query exprs = %q, want every default path at HEAD", q.exprs)
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
	const json5Config = `{
  $schema: 'https://docs.renovatebot.com/renovate-schema.json',
  // the shared preset first
  extends: [
    "github>donaldgifford/renovate-config",
    "github>donaldgifford/renovate-config:go",
  ],
}`
	fake := &graphqlServer{
		cost: 1,
		files: map[string]map[string]string{
			"json":       {"renovate.json": `{"extends":["github>boop-bot/renovate-config:python"]}`},
			"json5":      {"renovate.json5": json5Config},
			"dotgithub":  {".github/renovate.json5": `{extends: ["config:recommended"]}`},
			"renovaterc": {".renovaterc": `{"extends": "config:base"}`},
			// Renovate reads renovate.json before .github/renovate.json5.
			"both":      {".github/renovate.json5": `{extends: ["second"]}`, "renovate.json": `{"extends":["first"]}`},
			"noExtends": {"renovate.json": `{"labels":["deps"]}`},
			"broken":    {"renovate.json5": `{not: json5,`},
			"dirOnly":   {".github/renovate.json5": `{extends: ["under-dir"]}`},
		},
		trees:   map[string][]string{"dirOnly": {"renovate.json"}},
		missing: map[string]bool{"gone": true},
	}
	ids := []string{"json", "json5", "dotgithub", "renovaterc", "both", "noExtends", "broken", "dirOnly", "absent", "gone"}
	res, err := newProbeClient(t, fake).ProbeConfig(context.Background(), ids, nil)
	if err != nil {
		t.Fatalf("ProbeConfig() err = %v", err)
	}
	if len(fake.queries) != 1 {
		t.Fatalf("ProbeConfig() sent %d queries, want 1 for every path", len(fake.queries))
	}
	want := map[string]struct {
		path     string
		extends  []string
		parseErr bool
	}{
		"json":       {path: "renovate.json", extends: []string{"github>boop-bot/renovate-config:python"}},
		"json5":      {path: "renovate.json5", extends: []string{"github>donaldgifford/renovate-config", "github>donaldgifford/renovate-config:go"}},
		"dotgithub":  {path: ".github/renovate.json5", extends: []string{"config:recommended"}},
		"renovaterc": {path: ".renovaterc", extends: []string{"config:base"}},
		"both":       {path: "renovate.json", extends: []string{"first"}},
		"noExtends":  {path: "renovate.json"},
		"broken":     {path: "renovate.json5", parseErr: true},
		"dirOnly":    {path: ".github/renovate.json5", extends: []string{"under-dir"}},
		"absent":     {},
		"gone":       {},
	}
	for id, w := range want {
		got := res.Configs[id]
		if got.Exists != (w.path != "") || got.Path != w.path || !slices.Equal(got.Extends, w.extends) || (got.ParseErr != nil) != w.parseErr {
			t.Errorf("ProbeConfig()[%s] = %+v, want path %q extends %q parseErr %v", id, got, w.path, w.extends, w.parseErr)
		}
	}
}

func TestProbeConfig_ConfiguredPaths(t *testing.T) {
	t.Parallel()
	fake := &graphqlServer{cost: 1, files: map[string]map[string]string{
		"a": {"renovate.json": `{}`, "ci/renovate.json5": `{extends: ["x"]}`},
	}}
	c := newProbeClient(t, fake, ghclient.WithConfigPaths([]string{"ci/renovate.json5"}))
	res, err := c.ProbeConfig(context.Background(), []string{"a"}, nil)
	if err != nil {
		t.Fatalf("ProbeConfig() err = %v", err)
	}
	if got := fake.queries[0].exprs; !slices.Equal(got, []string{"HEAD:ci/renovate.json5"}) {
		t.Errorf("query exprs = %q, want only the configured path", got)
	}
	if got := res.Configs["a"]; got.Path != "ci/renovate.json5" || !slices.Equal(got.Extends, []string{"x"}) {
		t.Errorf("ProbeConfig()[a] = %+v, want ci/renovate.json5 extending x", got)
	}
}

func TestProbeConfig_ErrorsWithoutData(t *testing.T) {
	t.Parallel()
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":null,"errors":[{"message":"something went wrong"}]}`))
	})
	_, err := newProbeClient(t, h).ProbeConfig(context.Background(), []string{"a"}, nil)
	if !errors.Is(err, platform.ErrTransient) || !strings.Contains(err.Error(), "something went wrong") {
		t.Errorf("ProbeConfig() err = %v, want transient carrying the GraphQL message", err)
	}
}

func TestProbeConfig_Unauthorized(t *testing.T) {
	t.Parallel()
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"message":"Bad credentials"}`, http.StatusUnauthorized)
	})
	_, err := newProbeClient(t, h).ProbeConfig(context.Background(), []string{"a"}, nil)
	if !errors.Is(err, platform.ErrUnauthorized) {
		t.Errorf("ProbeConfig() err = %v, want ErrUnauthorized", err)
	}
}

func TestProbeConfigREST(t *testing.T) {
	t.Parallel()
	tree := &repoTree{
		files: map[string]map[string]string{
			"o/json5":  {"renovate.json5": `{extends: ["config:recommended",],}`, "go.mod": "module x"},
			"o/github": {".github/renovate.json": `{"extends":["a"]}`, ".github/workflows/ci.yml": "on: push"},
			"o/none":   {"README.md": "hi"},
		},
		fail: map[string]int{"o/broken": http.StatusBadGateway},
	}
	c := newProbeClient(t, tree)

	repos := []platform.Repository{
		{NodeID: "N1", Slug: "o/json5", DefaultBranch: "main"},
		{NodeID: "N2", Slug: "o/github", DefaultBranch: "main"},
		{NodeID: "N3", Slug: "o/none", DefaultBranch: "trunk"},
	}
	res, err := c.ProbeConfigREST(context.Background(), repos, nil)
	if err != nil {
		t.Fatalf("ProbeConfigREST() err = %v", err)
	}
	if got := res.Configs["N1"]; got.Path != "renovate.json5" || !slices.Equal(got.Extends, []string{"config:recommended"}) {
		t.Errorf("ProbeConfigREST()[N1] = %+v, want renovate.json5 with config:recommended", got)
	}
	if got := res.Configs["N2"]; got.Path != ".github/renovate.json" || !slices.Equal(got.Extends, []string{"a"}) {
		t.Errorf("ProbeConfigREST()[N2] = %+v, want .github/renovate.json with a", got)
	}
	if got := res.Configs["N3"]; got.Exists {
		t.Errorf("ProbeConfigREST()[N3] = %+v, want absent", got)
	}
	const base = "/api/v3/repos/"
	wantCalls := []string{
		base + "o/json5/contents/@main", base + "o/json5/contents/renovate.json5@main",
		base + "o/github/contents/@main", base + "o/github/contents/.github@main", base + "o/github/contents/.github/renovate.json@main",
		base + "o/none/contents/@trunk", base + "o/none/contents/.github@trunk",
	}
	if !slices.Equal(tree.calls, wantCalls) {
		t.Errorf("REST calls = %q, want %q", tree.calls, wantCalls)
	}
	if res.Queries != len(wantCalls) || res.Cost != 0 {
		t.Errorf("ProbeConfigREST() Queries/Cost = %d/%d, want %d/0", res.Queries, res.Cost, len(wantCalls))
	}

	_, err = c.ProbeConfigREST(context.Background(), []platform.Repository{{NodeID: "N4", Slug: "o/broken", DefaultBranch: "main"}}, nil)
	if !errors.Is(err, platform.ErrTransient) {
		t.Errorf("ProbeConfigREST(broken) err = %v, want ErrTransient", err)
	}
}
