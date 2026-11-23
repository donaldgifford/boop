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

package fakegithub

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

// PR states.
const (
	PROpen   = "open"
	PRClosed = "closed"
	PRMerged = "merged"
)

// PR is one pull request in a repository the fake serves.
type PR struct {
	RepoID int64
	Number int
	// Author is the login, "<slug>[bot]" for an App.
	Author    string
	Title     string
	Branch    string
	State     string // PROpen, PRClosed or PRMerged
	Labels    []string
	Automerge bool
	CreatedAt time.Time
	UpdatedAt time.Time
	MergedAt  *time.Time
	ClosedAt  *time.Time
}

// Issue is one open issue in a repository the fake serves: a bot's
// Dependency Dashboard, for the issue search.
type Issue struct {
	RepoID int64
	Number int
	// Author is the login, "<slug>[bot]" for an App.
	Author    string
	Title     string
	Body      string
	UpdatedAt time.Time
}

// SetIssues replaces the fake's open issues.
func (s *Server) SetIssues(issues ...Issue) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.issues = slices.Clone(issues)
}

// SetPullRequests replaces the fake's pull requests.
func (s *Server) SetPullRequests(prs ...PR) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pulls = slices.Clone(prs)
}

// SearchQueries returns the q of every PR search that arrived.
func (s *Server) SearchQueries() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.searchQueries)
}

// PullReads returns how many single pull request reads arrived.
func (s *Server) PullReads() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pullReads
}

// isSearch reports whether a GraphQL body is a search, not the probe.
func isSearch(body []byte) bool { return strings.Contains(string(body), "search(") }

// searchFilter is the parsed search query.
type searchFilter struct {
	author, owner string
	open, issues  bool
}

func parseSearch(q string) searchFilter {
	var f searchFilter
	for _, tok := range strings.Fields(q) {
		switch {
		case tok == "is:open":
			f.open = true
		case tok == "is:issue":
			f.issues = true
		case strings.HasPrefix(tok, "author:app/"):
			f.author = strings.TrimPrefix(tok, "author:app/") + "[bot]"
		case strings.HasPrefix(tok, "user:"):
			f.owner = strings.TrimPrefix(tok, "user:")
		}
	}
	return f
}

func (f *searchFilter) match(pr *PR, repo *Repo) bool {
	return f.ownedBy(pr.Author, repo) && (!f.open || pr.State == PROpen)
}

func (f *searchFilter) ownedBy(author string, repo *Repo) bool {
	return repo != nil && author == f.author && (f.owner == "" || strings.HasPrefix(repo.Slug, f.owner+"/"))
}

type labelJSON struct {
	Name string `json:"name"`
}

type searchNodeJSON struct {
	Number           int    `json:"number"`
	Title            string `json:"title"`
	URL              string `json:"url"`
	Body             string `json:"body,omitempty"`
	State            string `json:"state"`
	HeadRefName      string `json:"headRefName"`
	CreatedAt        any    `json:"createdAt"`
	UpdatedAt        any    `json:"updatedAt"`
	MergedAt         any    `json:"mergedAt"`
	ClosedAt         any    `json:"closedAt"`
	AutoMergeRequest any    `json:"autoMergeRequest"`
	Labels           struct {
		Nodes []labelJSON `json:"nodes"`
	} `json:"labels"`
	Repository struct {
		DatabaseID int64 `json:"databaseId"`
	} `json:"repository"`
}

type pageInfoJSON struct {
	HasNextPage bool   `json:"hasNextPage"`
	EndCursor   string `json:"endCursor"`
}

type searchJSON struct {
	Data struct {
		RateLimit struct {
			Cost      int    `json:"cost"`
			Remaining int    `json:"remaining"`
			ResetAt   string `json:"resetAt"`
		} `json:"rateLimit"`
		Search struct {
			IssueCount int              `json:"issueCount"`
			PageInfo   pageInfoJSON     `json:"pageInfo"`
			Nodes      []searchNodeJSON `json:"nodes"`
		} `json:"search"`
	} `json:"data"`
}

// search answers the open bot PR or issue search: is:pr|is:issue is:open author:app/<slug>
// [user:<account>], paged by first and after (an index).
func (s *Server) search(w http.ResponseWriter, body []byte) {
	var req struct {
		Variables struct {
			Q     string `json:"q"`
			First int    `json:"first"`
			After string `json:"after"`
		} `json:"variables"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, message(err.Error()))
		return
	}
	f := parseSearch(req.Variables.Q)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.searchQueries = append(s.searchQueries, req.Variables.Q)
	var hits []searchNodeJSON
	if f.issues {
		for i := range s.issues {
			is := &s.issues[i]
			if repo := s.repoLocked(is.RepoID); f.ownedBy(is.Author, repo) {
				n := searchNodeJSON{Number: is.Number, Title: is.Title, Body: is.Body, UpdatedAt: is.UpdatedAt.UTC().Format(time.RFC3339)}
				n.Repository.DatabaseID = is.RepoID
				hits = append(hits, n)
			}
		}
	}
	for i := range s.pulls {
		pr := &s.pulls[i]
		if repo := s.repoLocked(pr.RepoID); !f.issues && f.match(pr, repo) {
			hits = append(hits, searchNode(pr, repo))
		}
	}
	first := req.Variables.First
	if first <= 0 || first > 100 {
		first = 100
	}
	lo := 0
	if req.Variables.After != "" {
		n, err := strconv.Atoi(req.Variables.After)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, message("after: "+err.Error()))
			return
		}
		lo = min(n, len(hits))
	}
	hi := min(lo+first, len(hits))
	var out searchJSON
	g := s.rates["graphql"]
	out.Data.RateLimit.Cost, out.Data.RateLimit.Remaining = 1, g.Remaining
	out.Data.RateLimit.ResetAt = g.Reset.UTC().Format(time.RFC3339)
	out.Data.Search.IssueCount = len(hits)
	out.Data.Search.PageInfo = pageInfoJSON{HasNextPage: hi < len(hits), EndCursor: strconv.Itoa(hi)}
	out.Data.Search.Nodes = append([]searchNodeJSON{}, hits[lo:hi]...)
	writeJSON(w, http.StatusOK, &out)
}

func labelsJSON(names []string) []labelJSON {
	out := make([]labelJSON, 0, len(names))
	for _, n := range names {
		out = append(out, labelJSON{Name: n})
	}
	return out
}

func searchNode(pr *PR, repo *Repo) searchNodeJSON {
	n := searchNodeJSON{
		Number: pr.Number, Title: pr.Title, URL: prURL(repo, pr), State: strings.ToUpper(pr.State), HeadRefName: pr.Branch,
		CreatedAt: rfc3339(&pr.CreatedAt), UpdatedAt: rfc3339(&pr.UpdatedAt), MergedAt: rfc3339(pr.MergedAt), ClosedAt: rfc3339(pr.ClosedAt),
	}
	if pr.Automerge {
		n.AutoMergeRequest = struct {
			EnabledAt string `json:"enabledAt"`
		}{pr.CreatedAt.UTC().Format(time.RFC3339)}
	}
	n.Labels.Nodes = labelsJSON(pr.Labels)
	n.Repository.DatabaseID = repo.ID
	return n
}

// pullJSON is the REST pull request, as much of it as the client reads.
type pullJSON struct {
	Number  int         `json:"number"`
	Title   string      `json:"title"`
	HTMLURL string      `json:"html_url"`
	State   string      `json:"state"`
	Merged  bool        `json:"merged"`
	Labels  []labelJSON `json:"labels"`
	Head    struct {
		Ref string `json:"ref"`
	} `json:"head"`
	User struct {
		Login string `json:"login"`
	} `json:"user"`
	AutoMerge any `json:"auto_merge"`
	CreatedAt any `json:"created_at"`
	UpdatedAt any `json:"updated_at"`
	MergedAt  any `json:"merged_at"`
	ClosedAt  any `json:"closed_at"`
}

// pull serves GET /repositories/{id}/pulls/{number}.
func (s *Server) pull(w http.ResponseWriter, rest string) {
	id, num, _ := strings.Cut(rest, "/pulls/")
	repoID, err1 := strconv.ParseInt(id, 10, 64)
	number, err2 := strconv.Atoi(num)
	if err1 != nil || err2 != nil {
		notFound(w)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pullReads++
	repo := s.repoLocked(repoID)
	i := slices.IndexFunc(s.pulls, func(p PR) bool { return p.RepoID == repoID && p.Number == number })
	if repo == nil || i < 0 {
		notFound(w)
		return
	}
	pr := &s.pulls[i]
	out := pullJSON{
		Number: pr.Number, Title: pr.Title, HTMLURL: prURL(repo, pr), State: pr.State, Merged: pr.State == PRMerged,
		Labels:    labelsJSON(pr.Labels),
		CreatedAt: rfc3339(&pr.CreatedAt), UpdatedAt: rfc3339(&pr.UpdatedAt), MergedAt: rfc3339(pr.MergedAt), ClosedAt: rfc3339(pr.ClosedAt),
	}
	if out.Merged {
		out.State = PRClosed
	}
	out.Head.Ref, out.User.Login = pr.Branch, pr.Author
	if pr.Automerge {
		out.AutoMerge = struct {
			MergeMethod string `json:"merge_method"`
		}{"squash"}
	}
	writeJSON(w, http.StatusOK, &out)
}

// repoLocked is the repository with id, or nil; s.mu is held.
func (s *Server) repoLocked(id int64) *Repo {
	for i := range s.repos {
		if s.repos[i].ID == id {
			return &s.repos[i]
		}
	}
	return nil
}

func prURL(repo *Repo, pr *PR) string {
	return "https://github.com/" + repo.Slug + "/pull/" + strconv.Itoa(pr.Number)
}

// rfc3339 is t in GitHub's format, or nil.
func rfc3339(t *time.Time) any {
	if t == nil || t.IsZero() {
		return nil
	}
	return t.UTC().Format(time.RFC3339)
}
