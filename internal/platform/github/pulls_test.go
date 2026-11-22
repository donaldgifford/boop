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
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/donaldgifford/boop/internal/platform"
	ghclient "github.com/donaldgifford/boop/internal/platform/github"
	"github.com/donaldgifford/boop/test/fakegithub"
)

const testBot = "boop-bot[bot]"

// SearchOpenBotPRs pages the search and keeps only the App's open PRs on
// the account; GetPullRequest tells merged from closed.
func TestSearchOpenBotPRs_AgainstFake(t *testing.T) {
	t.Parallel()
	gh := fakegithub.New(t)
	gh.SetRepos(
		fakegithub.Repo{ID: 1, NodeID: "R_1", Slug: "acme/api", DefaultBranch: "main"},
		fakegithub.Repo{ID: 2, NodeID: "R_2", Slug: "other/lib", DefaultBranch: "main"},
	)
	at := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	merged := at.Add(time.Hour)
	var prs []fakegithub.PR
	for n := 1; n <= 150; n++ {
		prs = append(prs, fakegithub.PR{
			RepoID: 1, Number: n, Author: testBot, Title: fmt.Sprintf("Update dep %d", n), Branch: fmt.Sprintf("renovate/dep-%d", n),
			State: fakegithub.PROpen, CreatedAt: at, UpdatedAt: at, Labels: []string{"dependencies"}, Automerge: n == 1,
		})
	}
	prs = append(prs,
		fakegithub.PR{RepoID: 1, Number: 900, Author: "alice", State: fakegithub.PROpen, CreatedAt: at, UpdatedAt: at},
		fakegithub.PR{RepoID: 2, Number: 1, Author: testBot, State: fakegithub.PROpen, CreatedAt: at, UpdatedAt: at},
		fakegithub.PR{
			RepoID: 1, Number: 901, Author: testBot, State: fakegithub.PRMerged, Branch: "renovate/x",
			CreatedAt: at, UpdatedAt: merged, MergedAt: &merged, ClosedAt: &merged,
		},
	)
	gh.SetPullRequests(prs...)
	c := appClient(t, gh)

	got, err := c.SearchOpenBotPRs(context.Background(), "boop-bot", "acme")
	if err != nil {
		t.Fatalf("SearchOpenBotPRs: %v", err)
	}
	if len(got.PRs) != 150 || got.Queries != 2 || got.Cost != 2 {
		t.Fatalf("search = %d PRs over %d queries (cost %d), want 150 over 2", len(got.PRs), got.Queries, got.Cost)
	}
	first := got.PRs[0]
	if first.RepoID != 1 || first.Number != 1 || first.State != ghclient.PRStateOpen || !first.Automerge ||
		first.Branch != "renovate/dep-1" || !slices.Equal(first.Labels, []string{"dependencies"}) || !first.UpdatedAt.Equal(at) {
		t.Errorf("first PR = %+v", first)
	}
	if q := gh.SearchQueries()[0]; q != "is:pr is:open author:app/boop-bot user:acme" {
		t.Errorf("query = %q", q)
	}

	pr, err := c.GetPullRequest(context.Background(), 1, 901)
	if err != nil {
		t.Fatalf("GetPullRequest: %v", err)
	}
	if pr.State != ghclient.PRStateMerged || pr.MergedAt == nil || !pr.MergedAt.Equal(merged) || !pr.UpdatedAt.Equal(merged) {
		t.Errorf("merged PR = %+v", *pr)
	}
	if _, err := c.GetPullRequest(context.Background(), 1, 404); !errors.Is(err, platform.ErrNotFound) {
		t.Errorf("GetPullRequest(missing) err = %v, want ErrNotFound", err)
	}
}

func TestOpenBotPRQuery(t *testing.T) {
	t.Parallel()
	if got := ghclient.OpenBotPRQuery("boop-bot", ""); got != "is:pr is:open author:app/boop-bot" {
		t.Errorf("OpenBotPRQuery without an account = %q", got)
	}
}
