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

package github_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sync/atomic"
	"testing"

	"golang.org/x/time/rate"

	"github.com/donaldgifford/boop/internal/platform"
	ghclient "github.com/donaldgifford/boop/internal/platform/github"
	"github.com/donaldgifford/boop/test/fakegithub"
)

// splitFleet is n repositories, every third with a renovate.json5, served
// by a fake GitHub that rejects probes above limit lookups.
func splitFleet(t *testing.T, n, limit int) (*fakegithub.Server, []platform.Repository) {
	t.Helper()
	gh := fakegithub.New(t)
	gh.SetGraphQLLookupLimit(limit)
	fake := make([]fakegithub.Repo, n)
	repos := make([]platform.Repository, n)
	for i := range n {
		fake[i] = fakegithub.Repo{
			ID: int64(i + 1), NodeID: fmt.Sprintf("R_%03d", i), Slug: fmt.Sprintf("acme/r%03d", i), DefaultBranch: "main",
			HasConfig: i%3 == 0, ConfigPath: "renovate.json5", Config: `{extends: ["config:recommended"]}`,
		}
		repos[i] = platform.Repository{NodeID: fake[i].NodeID, Slug: fake[i].Slug, DefaultBranch: "main"}
	}
	gh.SetRepos(fake...)
	return gh, repos
}

func fakeClient(t *testing.T, gh *fakegithub.Server, opts ...ghclient.ClientOption) *ghclient.Client {
	t.Helper()
	opts = append([]ghclient.ClientOption{ghclient.WithRateLimit(rate.Inf, 1)}, opts...)
	c, err := ghclient.NewWithToken(ghclient.TokenAuth{Token: "t", BaseURL: gh.BaseURL()}, opts...)
	if err != nil {
		t.Fatalf("NewWithToken: %v", err)
	}
	return c
}

// wantConfigs probes repos with no lookup limit: the answers every
// split and fallback must reproduce.
func wantConfigs(t *testing.T, n int) map[string]ghclient.ConfigProbe {
	t.Helper()
	gh, repos := splitFleet(t, n, 0)
	res, err := fakeClient(t, gh).ProbeConfig(context.Background(), repos, nil, nil)
	if err != nil {
		t.Fatalf("ProbeConfig() without a limit: %v", err)
	}
	return res.Configs
}

func sameConfigs(t *testing.T, got, want map[string]ghclient.ConfigProbe) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("answered %d repos, want %d", len(got), len(want))
	}
	for id, w := range want {
		g := got[id]
		if g.Exists != w.Exists || g.Path != w.Path || !slices.Equal(g.Extends, w.Extends) {
			t.Errorf("Configs[%s] = %+v, want %+v", id, g, w)
		}
	}
}

func TestProbeConfig_SplitsOnTimeout(t *testing.T) {
	t.Parallel()
	// 10 default paths: one slice of 10 is 100 lookups, over a limit of 50.
	gh, repos := splitFleet(t, 10, 50)
	var hooks atomic.Int32
	res, err := fakeClient(t, gh).ProbeConfig(context.Background(), repos, nil, func() { hooks.Add(1) })
	if err != nil {
		t.Fatalf("ProbeConfig() err = %v", err)
	}
	if got, want := gh.ProbeSizes(), []int{10, 5, 5}; !slices.Equal(got, want) {
		t.Errorf("probe sizes = %v, want %v", got, want)
	}
	if res.Splits != 1 || res.Fallbacks != 0 {
		t.Errorf("Splits/Fallbacks = %d/%d, want 1/0", res.Splits, res.Fallbacks)
	}
	if int(hooks.Load()) != 3 {
		t.Errorf("onQuery ran %d times, want once per query (3)", hooks.Load())
	}
	sameConfigs(t, res.Configs, wantConfigs(t, 10))
}

func TestProbeConfig_FallsBackToREST(t *testing.T) {
	t.Parallel()
	// A limit below one repository's 10 paths: every repository ends on REST.
	gh, repos := splitFleet(t, 6, 5)
	var hooks atomic.Int32
	res, err := fakeClient(t, gh).ProbeConfig(context.Background(), repos, nil, func() { hooks.Add(1) })
	if err != nil {
		t.Fatalf("ProbeConfig() err = %v", err)
	}
	if res.Fallbacks != 6 {
		t.Errorf("Fallbacks = %d, want 6", res.Fallbacks)
	}
	if want := gh.ProbeQueries() + res.Fallbacks; int(hooks.Load()) != want {
		t.Errorf("onQuery ran %d times, want once per query and fallback (%d)", hooks.Load(), want)
	}
	if res.Queries <= gh.ProbeQueries() {
		t.Errorf("Queries = %d, want the %d GraphQL queries plus the REST calls", res.Queries, gh.ProbeQueries())
	}
	sameConfigs(t, res.Configs, wantConfigs(t, 6))
}

func TestProbeConfig_SplitDepthIsBounded(t *testing.T) {
	t.Parallel()
	// One path, a limit of 1: only single repositories pass, so the probe
	// is a full binary split of 10 into 10 leaves (2n-1 queries).
	gh, repos := splitFleet(t, 10, 1)
	res, err := fakeClient(t, gh, ghclient.WithConfigPaths([]string{"renovate.json5"})).
		ProbeConfig(context.Background(), repos, nil, nil)
	if err != nil {
		t.Fatalf("ProbeConfig() err = %v", err)
	}
	if q := gh.ProbeQueries(); q != 19 || res.Splits != 9 || res.Fallbacks != 0 {
		t.Errorf("queries/splits/fallbacks = %d/%d/%d, want 19/9/0", q, res.Splits, res.Fallbacks)
	}
}

func TestProbeConfig_TimeoutShapesSplit(t *testing.T) {
	t.Parallel()
	for _, shape := range []fakegithub.TimeoutShape{
		fakegithub.TimeoutBadGateway, fakegithub.TimeoutGatewayTimeout, fakegithub.TimeoutGraphQLError,
	} {
		t.Run(fmt.Sprint(shape), func(t *testing.T) {
			t.Parallel()
			gh, repos := splitFleet(t, 10, 50)
			gh.SetGraphQLTimeoutShape(shape)
			res, err := fakeClient(t, gh).ProbeConfig(context.Background(), repos, nil, nil)
			if err != nil {
				t.Fatalf("ProbeConfig() err = %v", err)
			}
			if res.Splits != 1 {
				t.Errorf("Splits = %d, want 1", res.Splits)
			}
		})
	}
}

// splitThen answers every multi-repository probe with 502 and a single
// one with status (and a JSON message); every REST call is a 404.
func splitThen(status int, queries *atomic.Int32) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/graphql" {
			http.NotFound(w, r)
			return
		}
		queries.Add(1)
		var req struct {
			Variables struct {
				IDs []string `json:"ids"`
			} `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if len(req.Variables.IDs) > 1 || status == http.StatusBadGateway {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"message":"no"}`))
	})
}

func TestProbeConfig_ErrorsInsideASplit(t *testing.T) {
	t.Parallel()
	t.Run("unauthorized", func(t *testing.T) {
		t.Parallel()
		var q atomic.Int32
		_, err := newProbeClient(t, splitThen(http.StatusUnauthorized, &q)).
			ProbeConfig(context.Background(), probeRepos(4), nil, nil)
		if !errors.Is(err, platform.ErrUnauthorized) {
			t.Errorf("ProbeConfig() err = %v, want ErrUnauthorized", err)
		}
	})
	t.Run("rate limited", func(t *testing.T) {
		t.Parallel()
		var q atomic.Int32
		_, err := newProbeClient(t, splitThen(http.StatusTooManyRequests, &q)).
			ProbeConfig(context.Background(), probeRepos(4), nil, nil)
		var limited *platform.RateLimitedError
		if !errors.As(err, &limited) {
			t.Errorf("ProbeConfig() err = %v, want RateLimitedError", err)
		}
	})
	t.Run("REST 404 reads as absent", func(t *testing.T) {
		t.Parallel()
		var q atomic.Int32
		res, err := newProbeClient(t, splitThen(http.StatusBadGateway, &q)).
			ProbeConfig(context.Background(), probeRepos(4), nil, nil)
		if err != nil {
			t.Fatalf("ProbeConfig() err = %v", err)
		}
		if res.Fallbacks != 4 {
			t.Errorf("Fallbacks = %d, want 4", res.Fallbacks)
		}
		for id, c := range res.Configs {
			if c.Exists {
				t.Errorf("Configs[%s] exists, want absent", id)
			}
		}
	})
}

func TestProbeConfig_OtherErrorsWithoutDataDoNotSplit(t *testing.T) {
	t.Parallel()
	var q atomic.Int32
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		q.Add(1)
		_, _ = w.Write([]byte(`{"data":null,"errors":[{"message":"something went wrong"}]}`))
	})
	_, err := newProbeClient(t, h).ProbeConfig(context.Background(), probeRepos(4), nil, nil)
	if !errors.Is(err, platform.ErrTransient) {
		t.Errorf("ProbeConfig() err = %v, want ErrTransient", err)
	}
	if q.Load() != 1 {
		t.Errorf("sent %d queries, want 1 (no split)", q.Load())
	}
}
