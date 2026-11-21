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

package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	gogithub "github.com/google/go-github/v62/github"

	"github.com/donaldgifford/boop/internal/platform"
)

// searchPageSize is GitHub's largest search page.
const searchPageSize = 100

// PR states, as the store records them.
const (
	PRStateOpen   = "open"
	PRStateClosed = "closed"
	PRStateMerged = "merged"
)

// PullRequest is one bot pull request as GitHub describes it.
type PullRequest struct {
	RepoID    int64
	Number    int
	Title     string
	URL       string
	Branch    string
	State     string // PRStateOpen, PRStateClosed or PRStateMerged
	Labels    []string
	Automerge bool
	CreatedAt time.Time
	UpdatedAt time.Time
	MergedAt  *time.Time
	ClosedAt  *time.Time
}

// PRSearch is SearchOpenBotPRs' answer.
type PRSearch struct {
	PRs []PullRequest
	// Queries is how many GraphQL pages were read; Cost is the sum of
	// their rateLimit.cost.
	Queries, Cost int
}

// searchQuery is one page of the open bot PR search. Labels past the
// first 50 are not read.
const searchQuery = `query($q: String!, $first: Int!, $after: String) {
  rateLimit { cost remaining resetAt }
  search(query: $q, type: ISSUE, first: $first, after: $after) {
    pageInfo { hasNextPage endCursor }
    nodes {
      ... on PullRequest {
        number title url state headRefName createdAt updatedAt mergedAt closedAt
        autoMergeRequest { enabledAt }
        labels(first: 50) { nodes { name } }
        repository { databaseId }
      }
    }
  }
}`

type searchResponse struct {
	Data *struct {
		RateLimit struct {
			Cost int `json:"cost"`
		} `json:"rateLimit"`
		Search struct {
			PageInfo struct {
				HasNextPage bool   `json:"hasNextPage"`
				EndCursor   string `json:"endCursor"`
			} `json:"pageInfo"`
			Nodes []searchNode `json:"nodes"`
		} `json:"search"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

type searchNode struct {
	Number           int        `json:"number"`
	Title            string     `json:"title"`
	URL              string     `json:"url"`
	State            string     `json:"state"`
	HeadRefName      string     `json:"headRefName"`
	CreatedAt        time.Time  `json:"createdAt"`
	UpdatedAt        time.Time  `json:"updatedAt"`
	MergedAt         *time.Time `json:"mergedAt"`
	ClosedAt         *time.Time `json:"closedAt"`
	AutoMergeRequest *struct{}  `json:"autoMergeRequest"`
	Labels           struct {
		Nodes []struct {
			Name string `json:"name"`
		} `json:"nodes"`
	} `json:"labels"`
	Repository struct {
		DatabaseID int64 `json:"databaseId"`
	} `json:"repository"`
}

// OpenBotPRQuery is the search for the App's open pull requests on one
// account: is:pr is:open author:app/<slug> user:<account>. An
// installation token also searches public repositories elsewhere, so the
// account qualifier keeps the answer to the installation's own.
func OpenBotPRQuery(appSlug, account string) string {
	q := "is:pr is:open author:app/" + appSlug
	if account != "" {
		q += " user:" + account
	}
	return q
}

// SearchOpenBotPRs lists the App's open pull requests on account with
// one GraphQL search, 100 per page (DESIGN-0004 § Sources of PR,
// dashboard and dependency data). GitHub's search stops at 1000 results.
func (c *Client) SearchOpenBotPRs(ctx context.Context, appSlug, account string) (*PRSearch, error) {
	if appSlug == "" {
		return nil, errors.New("github: app slug required")
	}
	out := &PRSearch{}
	vars := map[string]any{"q": OpenBotPRQuery(appSlug, account), "first": searchPageSize}
	for {
		if err := c.wait(ctx); err != nil {
			return nil, err
		}
		req, err := c.gh.NewRequest(http.MethodPost, c.graphqlURL(), &graphqlRequest{Query: searchQuery, Variables: vars})
		if err != nil {
			return nil, fmt.Errorf("github: build search request: %w", err)
		}
		var page searchResponse
		resp, err := c.gh.Do(ctx, req, &page)
		out.Queries++
		if err != nil {
			return nil, classifyErr(resp, err)
		}
		if page.Data == nil {
			msgs := make([]string, 0, len(page.Errors))
			for _, e := range page.Errors {
				msgs = append(msgs, e.Message)
			}
			return nil, fmt.Errorf("%w: github: search returned no data: %s", platform.ErrTransient, strings.Join(msgs, "; "))
		}
		out.Cost += page.Data.RateLimit.Cost
		for i := range page.Data.Search.Nodes {
			if n := &page.Data.Search.Nodes[i]; n.Number > 0 {
				out.PRs = append(out.PRs, n.pullRequest())
			}
		}
		info := page.Data.Search.PageInfo
		if !info.HasNextPage || info.EndCursor == "" {
			return out, nil
		}
		vars["after"] = info.EndCursor
	}
}

func (n *searchNode) pullRequest() PullRequest {
	labels := make([]string, 0, len(n.Labels.Nodes))
	for _, l := range n.Labels.Nodes {
		labels = append(labels, l.Name)
	}
	return PullRequest{
		RepoID: n.Repository.DatabaseID, Number: n.Number, Title: n.Title, URL: n.URL, Branch: n.HeadRefName,
		State: strings.ToLower(n.State), Labels: labels, Automerge: n.AutoMergeRequest != nil,
		CreatedAt: n.CreatedAt, UpdatedAt: n.UpdatedAt, MergedAt: n.MergedAt, ClosedAt: n.ClosedAt,
	}
}

// GetPullRequest reads one pull request by repository id and number,
// for a PR that left the open set: whether it merged or closed, and
// when. A PR or repository that is gone is platform.ErrNotFound.
func (c *Client) GetPullRequest(ctx context.Context, repoID int64, number int) (*PullRequest, error) {
	if repoID <= 0 || number <= 0 {
		return nil, errors.New("github: repository id and pull request number required")
	}
	if err := c.wait(ctx); err != nil {
		return nil, err
	}
	req, err := c.gh.NewRequest(http.MethodGet, fmt.Sprintf("repositories/%d/pulls/%d", repoID, number), nil)
	if err != nil {
		return nil, fmt.Errorf("github: build pull request request: %w", err)
	}
	var pr gogithub.PullRequest
	resp, err := c.gh.Do(ctx, req, &pr)
	if err != nil {
		return nil, classifyErr(resp, err)
	}
	return fromREST(repoID, &pr), nil
}

func fromREST(repoID int64, pr *gogithub.PullRequest) *PullRequest {
	state := pr.GetState()
	if pr.GetMerged() || pr.MergedAt != nil {
		state = PRStateMerged
	}
	labels := make([]string, 0, len(pr.Labels))
	for _, l := range pr.Labels {
		labels = append(labels, l.GetName())
	}
	return &PullRequest{
		RepoID: repoID, Number: pr.GetNumber(), Title: pr.GetTitle(), URL: pr.GetHTMLURL(), Branch: pr.GetHead().GetRef(),
		State: state, Labels: labels, Automerge: pr.AutoMerge != nil,
		CreatedAt: pr.GetCreatedAt().Time, UpdatedAt: pr.GetUpdatedAt().Time,
		MergedAt: timeOf(pr.MergedAt), ClosedAt: timeOf(pr.ClosedAt),
	}
}

func timeOf(t *gogithub.Timestamp) *time.Time {
	if t == nil || t.IsZero() {
		return nil
	}
	v := t.Time
	return &v
}
