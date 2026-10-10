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

// Package fakegithub is an in-process GitHub for boopd's tests: the
// App endpoints (installations, mint, revoke), installation repository
// paging, the GraphQL config probe, the REST contents probe, repository
// lookups and /rate_limit. It serves the GHES path shape go-github uses
// for a non-github.com base URL (/api/v3/..., /api/graphql) and records
// mints and revokes for assertions.
package fakegithub

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Repo is one repository the installation can see.
type Repo struct {
	ID            int64
	NodeID        string
	Slug          string
	DefaultBranch string
	Fork          bool
	Archived      bool
	// Config is the config file's text; HasConfig says whether it exists.
	Config    string
	HasConfig bool
}

// Installation is one installation of the App.
type Installation struct {
	ID        int64
	Account   string
	Suspended bool
}

// Mint is one recorded token mint.
type Mint struct {
	InstallationID int64
	RepoIDs        []int64
	Token          string
}

// Rate is one /rate_limit resource.
type Rate struct {
	Limit     int
	Remaining int
	Reset     time.Time
}

// Server is the fake GitHub. Fields are guarded by its lock; use the
// setters and getters while it serves.
type Server struct {
	*httptest.Server
	// PEM is an App private key the fake accepts (it checks no
	// signature).
	PEM []byte

	mu            sync.Mutex
	installations []Installation
	repos         []Repo
	rates         map[string]Rate
	probeCost     int
	tokenTTL      time.Duration
	revokeStatus  int
	failPages     map[int]int // page -> remaining 502s
	mints         []Mint
	revokes       []string
	pageRequests  []int
	probeQueries  int
	rateReads     int
}

// NewFake returns a fake GitHub with one installation (id 1) and
// healthy rate limits that is not serving yet: mount Handler on any
// server. test/stub-github serves it over TLS in a cluster. BaseURL and
// the embedded httptest.Server are only set by New.
func NewFake() (*Server, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("fakegithub: generate key: %w", err)
	}
	reset := time.Now().Add(time.Hour).Truncate(time.Second)
	return &Server{
		PEM:           pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}),
		installations: []Installation{{ID: 1, Account: "acme"}},
		rates: map[string]Rate{
			"core":    {Limit: 5000, Remaining: 4900, Reset: reset},
			"graphql": {Limit: 5000, Remaining: 4900, Reset: reset},
		},
		probeCost:    1,
		tokenTTL:     time.Hour,
		revokeStatus: http.StatusNoContent,
		failPages:    make(map[int]int),
	}, nil
}

// New starts a fake GitHub (see NewFake) on a local httptest server; it
// closes with the test.
func New(tb testing.TB) *Server {
	tb.Helper()
	s, err := NewFake()
	if err != nil {
		tb.Fatal(err)
	}
	s.Server = httptest.NewServer(s.Handler())
	tb.Cleanup(s.Close)
	return s
}

// Handler serves the fake's API.
func (s *Server) Handler() http.Handler { return http.HandlerFunc(s.serve) }

// BaseURL is the API base URL to configure as the app's endpoint.
func (s *Server) BaseURL() string { return s.URL + "/" }

// SetInstallations replaces the App's installations.
func (s *Server) SetInstallations(in ...Installation) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.installations = in
}

// SetRepos replaces the repositories every installation sees.
func (s *Server) SetRepos(repos ...Repo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.repos = repos
}

// SetRate replaces one /rate_limit resource.
func (s *Server) SetRate(resource string, r Rate) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rates[resource] = r
}

// SetTokenTTL sets the expiry of minted tokens.
func (s *Server) SetTokenTTL(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokenTTL = d
}

// SetRevokeStatus sets the status DELETE /installation/token answers.
func (s *Server) SetRevokeStatus(code int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.revokeStatus = code
}

// FailPage makes repository page n answer 502 the next times requests.
func (s *Server) FailPage(n, times int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failPages[n] = times
}

// Mints returns the recorded mints.
func (s *Server) Mints() []Mint {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Mint(nil), s.mints...)
}

// Revokes returns the revoked tokens.
func (s *Server) Revokes() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.revokes...)
}

// PageRequests returns the repository page numbers requested, in order.
func (s *Server) PageRequests() []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]int(nil), s.pageRequests...)
}

// ProbeQueries returns how many GraphQL probe queries arrived.
func (s *Server) ProbeQueries() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.probeQueries
}

// RateReads returns how many /rate_limit reads arrived.
func (s *Server) RateReads() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rateReads
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Date", time.Now().UTC().Format(http.TimeFormat))
	if r.Method == http.MethodPost && r.URL.Path == "/api/graphql" {
		s.probe(w, r)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/v3")
	if strings.HasPrefix(path, "/app/") {
		s.serveApp(w, r, path)
		return
	}
	switch {
	case r.Method == http.MethodDelete && path == "/installation/token":
		s.revoke(w, r)
	case r.Method == http.MethodGet && path == "/installation/repositories":
		s.listRepos(w, r)
	case r.Method == http.MethodGet && path == "/rate_limit":
		s.rateLimit(w)
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/repositories/"):
		s.repoByID(w, strings.TrimPrefix(path, "/repositories/"))
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/repos/"):
		s.repoPath(w, strings.TrimPrefix(path, "/repos/"))
	default:
		notFound(w)
	}
}

// serveApp serves the JWT-authenticated /app endpoints.
func (s *Server) serveApp(w http.ResponseWriter, r *http.Request, path string) {
	id, underInstallations := strings.CutPrefix(path, "/app/installations/")
	id, isTokens := strings.CutSuffix(id, "/access_tokens")
	isMint := underInstallations && isTokens
	switch {
	case r.Method == http.MethodGet && path == "/app/installations":
		s.listInstallations(w)
	case r.Method == http.MethodPost && isMint:
		s.mint(w, r, id)
	default:
		notFound(w)
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// message is GitHub's error body.
func message(m string) map[string]string { return map[string]string{"message": m} }

// queryInt is a positive integer query parameter, or def.
func queryInt(r *http.Request, name string, def int) int {
	n, err := strconv.Atoi(r.URL.Query().Get(name))
	if err != nil || n < 1 {
		return def
	}
	return n
}

func notFound(w http.ResponseWriter) {
	writeJSON(w, http.StatusNotFound, message("Not Found"))
}

func (s *Server) listInstallations(w http.ResponseWriter) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]map[string]any, 0, len(s.installations))
	for _, in := range s.installations {
		m := map[string]any{"id": in.ID, "account": map[string]any{"login": in.Account}, "repository_selection": "all"}
		if in.Suspended {
			m["suspended_at"] = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
		}
		out = append(out, m)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) mint(w http.ResponseWriter, r *http.Request, id string) {
	instID, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		notFound(w)
		return
	}
	var body struct {
		RepositoryIDs []int64 `json:"repository_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, message(err.Error()))
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tok := fmt.Sprintf("ghs_fake_%d_%d", instID, len(s.mints)+1)
	s.mints = append(s.mints, Mint{InstallationID: instID, RepoIDs: body.RepositoryIDs, Token: tok})
	writeJSON(w, http.StatusCreated, map[string]any{
		"token":      tok,
		"expires_at": time.Now().Add(s.tokenTTL).UTC().Format(time.RFC3339),
	})
}

func (s *Server) revoke(w http.ResponseWriter, r *http.Request) {
	tok := strings.TrimPrefix(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), "token ")
	s.mu.Lock()
	defer s.mu.Unlock()
	s.revokes = append(s.revokes, tok)
	if s.revokeStatus != http.StatusNoContent {
		writeJSON(w, s.revokeStatus, message("revoke failed"))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func repoJSON(r *Repo) map[string]any {
	owner, name, _ := strings.Cut(r.Slug, "/")
	return map[string]any{
		"id": r.ID, "node_id": r.NodeID, "full_name": r.Slug, "name": name,
		"owner":          map[string]any{"login": owner},
		"default_branch": r.DefaultBranch, "fork": r.Fork, "archived": r.Archived,
	}
}

func (s *Server) listRepos(w http.ResponseWriter, r *http.Request) {
	page := queryInt(r, "page", 1)
	per := queryInt(r, "per_page", 30)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pageRequests = append(s.pageRequests, page)
	if s.failPages[page] > 0 {
		s.failPages[page]--
		writeJSON(w, http.StatusBadGateway, message("bad gateway"))
		return
	}
	lo := min((page-1)*per, len(s.repos))
	hi := min(lo+per, len(s.repos))
	out := make([]map[string]any, 0, hi-lo)
	for i := lo; i < hi; i++ {
		out = append(out, repoJSON(&s.repos[i]))
	}
	if hi < len(s.repos) {
		w.Header().Set("Link", fmt.Sprintf(`<%s/api/v3/installation/repositories?per_page=%d&page=%d>; rel="next"`, s.URL, per, page+1))
	}
	writeJSON(w, http.StatusOK, map[string]any{"total_count": len(s.repos), "repositories": out})
}

func (s *Server) rateLimit(w http.ResponseWriter) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rateReads++
	res := make(map[string]any, len(s.rates))
	for k, v := range s.rates {
		res[k] = map[string]any{"limit": v.Limit, "remaining": v.Remaining, "used": v.Limit - v.Remaining, "reset": v.Reset.Unix()}
	}
	writeJSON(w, http.StatusOK, map[string]any{"resources": res})
}

func (s *Server) probe(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Variables struct {
			IDs []string `json:"ids"`
		} `json:"variables"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, message(err.Error()))
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.probeQueries++
	byNode := make(map[string]*Repo, len(s.repos))
	for i := range s.repos {
		byNode[s.repos[i].NodeID] = &s.repos[i]
	}
	nodes := make([]any, 0, len(req.Variables.IDs))
	for _, id := range req.Variables.IDs {
		repo, ok := byNode[id]
		switch {
		case !ok:
			nodes = append(nodes, nil)
		case repo.HasConfig:
			nodes = append(nodes, map[string]any{"id": id, "config": map[string]any{"byteSize": len(repo.Config), "text": repo.Config}})
		default:
			nodes = append(nodes, map[string]any{"id": id, "config": nil})
		}
	}
	g := s.rates["graphql"]
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{
		"rateLimit": map[string]any{"cost": s.probeCost, "remaining": g.Remaining, "resetAt": g.Reset.UTC().Format(time.RFC3339)},
		"nodes":     nodes,
	}})
}

func (s *Server) repoByID(w http.ResponseWriter, id string) {
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		notFound(w)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.repos {
		if s.repos[i].ID == n {
			writeJSON(w, http.StatusOK, repoJSON(&s.repos[i]))
			return
		}
	}
	notFound(w)
}

// repoPath serves /repos/{owner}/{name} and
// /repos/{owner}/{name}/contents/{path}.
func (s *Server) repoPath(w http.ResponseWriter, rest string) {
	parts := strings.SplitN(rest, "/", 4)
	if len(parts) < 2 {
		notFound(w)
		return
	}
	slug := parts[0] + "/" + parts[1]
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.repos {
		repo := &s.repos[i]
		if repo.Slug != slug {
			continue
		}
		switch {
		case len(parts) == 2:
			writeJSON(w, http.StatusOK, repoJSON(repo))
		case len(parts) == 4 && parts[2] == "contents" && repo.HasConfig:
			writeJSON(w, http.StatusOK, map[string]any{"type": "file", "name": parts[3], "path": parts[3], "size": len(repo.Config)})
		default:
			notFound(w)
		}
		return
	}
	notFound(w)
}
