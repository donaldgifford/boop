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
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	gogithub "github.com/google/go-github/v62/github"

	"github.com/donaldgifford/boop/internal/platform"
)

const (
	// probeBatch is how many node ids one GraphQL probe carries. It is a
	// design choice, not a documented limit (DESIGN-0001 § DiscoveryWorkflow).
	probeBatch = 100

	// maxConfigBytes bounds the config text parsed for extends; a larger
	// file is reported unparsed.
	maxConfigBytes = 64 << 10
)

// ConfigProbe is one repository's probe answer.
type ConfigProbe struct {
	// Exists is true when one of the config files is on the default
	// branch.
	Exists bool
	// Path is the first config file present, in precedence order: the
	// one Renovate reads.
	Path string
	// Extends is that file's extends list.
	Extends []string
	// ParseErr is why the file's extends could not be read: above 64 KB,
	// binary, or not JSON5. Extends is then empty and the profile comes
	// from the report alone. The repository is still onboarded; Renovate
	// reports its own config errors.
	ParseErr error
}

// ProbeResult is the outcome of probing a set of repositories.
type ProbeResult struct {
	// Configs holds one answer per requested node id. A node the
	// installation cannot see, or that is not a repository, has
	// Exists false.
	Configs map[string]ConfigProbe
	// Queries is how many requests the probe sent.
	Queries int
	// Cost is the sum of rateLimit.cost over the GraphQL queries; the
	// pass records it (boopd_discovery_probe_cost). Zero for REST.
	Cost int
	// Remaining and ResetAt are the GraphQL budget after the last query.
	Remaining int
	ResetAt   time.Time
}

type graphqlRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
}

type probeResponse struct {
	Data *struct {
		RateLimit struct {
			Cost      int       `json:"cost"`
			Remaining int       `json:"remaining"`
			ResetAt   time.Time `json:"resetAt"`
		} `json:"rateLimit"`
		// Each node is a repository's id plus one field per path, c0 to
		// c<n-1>, decoded by probeNode.
		Nodes []map[string]json.RawMessage `json:"nodes"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// blob is one object(expression:) answer. A path that names a tree
// matches no Blob fragment and decodes with ByteSize nil.
type blob struct {
	ByteSize *int    `json:"byteSize"`
	Text     *string `json:"text"`
}

// probeQuery is the config probe for n paths: one aliased
// object(expression:) field per path, c<i> reading $e<i>. The
// expressions are variables, so no path is spliced into the query text.
func probeQuery(n int) string {
	var vars, fields strings.Builder
	for i := range n {
		fmt.Fprintf(&vars, ", $e%d: String!", i)
		fmt.Fprintf(&fields, "\n      c%d: object(expression: $e%d) { ... on Blob { byteSize text } }", i, i)
	}
	return "query($ids: [ID!]!" + vars.String() + ") {\n" +
		"  rateLimit { cost remaining resetAt }\n" +
		"  nodes(ids: $ids) {\n" +
		"    ... on Repository {\n" +
		"      id" + fields.String() + "\n" +
		"    }\n" +
		"  }\n" +
		"}"
}

// ProbeConfig asks GitHub's GraphQL API which config file each repository
// carries on its default branch (HEAD), one query per 100 node ids with
// every path in the same query, and parses extends from the first one
// present (DESIGN-0001 OQ3). paths is in precedence order; empty means
// the client's. Every query's rateLimit.cost is read and summed into the
// result.
func (c *Client) ProbeConfig(ctx context.Context, nodeIDs, paths []string) (*ProbeResult, error) {
	if len(paths) == 0 {
		paths = c.paths()
	}
	res := &ProbeResult{Configs: make(map[string]ConfigProbe, len(nodeIDs))}
	for _, id := range nodeIDs {
		res.Configs[id] = ConfigProbe{}
	}
	query := probeQuery(len(paths))
	for start := 0; start < len(nodeIDs); start += probeBatch {
		batch := nodeIDs[start:min(start+probeBatch, len(nodeIDs))]
		if err := c.probeBatch(ctx, query, batch, paths, res); err != nil {
			return nil, err
		}
	}
	return res, nil
}

func (c *Client) probeBatch(ctx context.Context, query string, ids, paths []string, res *ProbeResult) error {
	if err := c.wait(ctx); err != nil {
		return err
	}
	vars := map[string]any{"ids": ids}
	for i, p := range paths {
		vars["e"+strconv.Itoa(i)] = "HEAD:" + p
	}
	req, err := c.gh.NewRequest(http.MethodPost, c.graphqlURL(), &graphqlRequest{Query: query, Variables: vars})
	if err != nil {
		return fmt.Errorf("github: build probe request: %w", err)
	}
	var out probeResponse
	resp, err := c.gh.Do(ctx, req, &out)
	res.Queries++
	if err != nil {
		return classifyErr(resp, err)
	}
	if out.Data == nil {
		msgs := make([]string, 0, len(out.Errors))
		for _, e := range out.Errors {
			msgs = append(msgs, e.Message)
		}
		return fmt.Errorf("%w: github: probe returned no data: %s", platform.ErrTransient, strings.Join(msgs, "; "))
	}
	res.Cost += out.Data.RateLimit.Cost
	res.Remaining = out.Data.RateLimit.Remaining
	res.ResetAt = out.Data.RateLimit.ResetAt
	for _, n := range out.Data.Nodes {
		id, probe := probeNode(n, paths)
		if _, asked := res.Configs[id]; asked && probe.Exists {
			res.Configs[id] = probe
		}
	}
	return nil
}

// probeNode reads one node: its id and the first path that is a file.
func probeNode(n map[string]json.RawMessage, paths []string) (string, ConfigProbe) {
	var id string
	if raw, ok := n["id"]; !ok || json.Unmarshal(raw, &id) != nil {
		return "", ConfigProbe{}
	}
	for i, p := range paths {
		raw, ok := n["c"+strconv.Itoa(i)]
		if !ok {
			continue
		}
		var b *blob
		if json.Unmarshal(raw, &b) != nil || b == nil || b.ByteSize == nil {
			continue
		}
		text := ""
		if b.Text != nil {
			text = *b.Text
		}
		return id, presentProbe(p, text, *b.ByteSize)
	}
	return id, ConfigProbe{}
}

func presentProbe(p, text string, byteSize int) ConfigProbe {
	extends, err := parseExtends(text, byteSize)
	return ConfigProbe{Exists: true, Path: p, Extends: extends, ParseErr: err}
}

// ProbeConfigREST is the fallback behind discovery.probe: rest. For each
// repository it lists the directories the paths live in on the default
// branch (the root and .github by default) and fetches the first path
// present, so a repository costs at most one call per directory plus
// one. Answers are keyed by NodeID, as ProbeConfig's are.
func (c *Client) ProbeConfigREST(ctx context.Context, repos []platform.Repository, paths []string) (*ProbeResult, error) {
	if len(paths) == 0 {
		paths = c.paths()
	}
	res := &ProbeResult{Configs: make(map[string]ConfigProbe, len(repos))}
	for i := range repos {
		probe, calls, err := c.probeREST(ctx, &repos[i], paths)
		res.Queries += calls
		if err != nil {
			return nil, err
		}
		res.Configs[repos[i].NodeID] = probe
	}
	return res, nil
}

func (c *Client) probeREST(ctx context.Context, repo *platform.Repository, paths []string) (ConfigProbe, int, error) {
	found, calls, err := c.locateConfig(ctx, repo, paths)
	if err != nil || found == "" {
		return ConfigProbe{}, calls, err
	}
	owner, name, _ := splitSlug(repo.Slug)
	if err := c.wait(ctx); err != nil {
		return ConfigProbe{}, calls, err
	}
	opt := &gogithub.RepositoryContentGetOptions{Ref: repo.DefaultBranch}
	file, _, resp, err := c.gh.Repositories.GetContents(ctx, owner, name, found, opt)
	calls++
	if err != nil {
		return ConfigProbe{}, calls, classifyErr(resp, err)
	}
	if file == nil {
		return ConfigProbe{}, calls, nil
	}
	text, err := file.GetContent()
	if err != nil {
		// Undecodable content still counts as present, with no extends.
		return ConfigProbe{Exists: true, Path: found, ParseErr: fmt.Errorf("decode contents: %w", err)}, calls, nil
	}
	return presentProbe(found, text, file.GetSize()), calls, nil
}

// locateConfig returns the first of paths that is a file on the
// repository's default branch, or "" when none is. It lists each path's
// directory once; a missing directory lists empty. calls is how many
// requests it sent. GitHub lists at most 1,000 entries per directory.
func (c *Client) locateConfig(ctx context.Context, repo *platform.Repository, paths []string) (found string, calls int, err error) {
	owner, name, ok := splitSlug(repo.Slug)
	if !ok {
		return "", 0, fmt.Errorf("github: invalid slug %q", repo.Slug)
	}
	opt := &gogithub.RepositoryContentGetOptions{Ref: repo.DefaultBranch}
	listings := make(map[string]map[string]bool)
	for _, p := range paths {
		dir := path.Dir(p)
		if dir == "." {
			dir = ""
		}
		files, listed := listings[dir]
		if !listed {
			calls++
			if files, err = c.listFiles(ctx, owner, name, dir, opt); err != nil {
				return "", calls, err
			}
			listings[dir] = files
		}
		if files[path.Base(p)] {
			return p, calls, nil
		}
	}
	return "", calls, nil
}

// listFiles returns the names of the files in one directory; a missing
// directory has none.
func (c *Client) listFiles(ctx context.Context, owner, name, dir string, opt *gogithub.RepositoryContentGetOptions) (map[string]bool, error) {
	if err := c.wait(ctx); err != nil {
		return nil, err
	}
	_, entries, resp, err := c.gh.Repositories.GetContents(ctx, owner, name, dir, opt)
	if err != nil {
		if classified := classifyErr(resp, err); !errors.Is(classified, platform.ErrNotFound) {
			return nil, classified
		}
	}
	files := make(map[string]bool, len(entries))
	for _, e := range entries {
		if e.GetType() == "file" {
			files[e.GetName()] = true
		}
	}
	return files, nil
}

// errExtendsShape is parseExtends' error for an extends that is neither
// a string nor a list of strings.
var errExtendsShape = errors.New("extends is not a string or a list of strings")

// parseExtends returns the config's extends list, reading the text as
// JSON5 (which covers JSON and JSONC). Renovate accepts a string or an
// array of strings; a config without extends yields nil and no error.
func parseExtends(text string, byteSize int) ([]string, error) {
	if byteSize > maxConfigBytes || len(text) > maxConfigBytes {
		return nil, fmt.Errorf("config is %d bytes, above the %d-byte parse limit", max(byteSize, len(text)), maxConfigBytes)
	}
	if text == "" {
		return nil, errors.New("config has no text (empty or binary)")
	}
	norm, err := json5ToJSON(text)
	if err != nil {
		return nil, err
	}
	var cfg struct {
		Extends json.RawMessage `json:"extends"`
	}
	if err := json.Unmarshal([]byte(norm), &cfg); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}
	if len(cfg.Extends) == 0 || string(cfg.Extends) == "null" {
		return nil, nil
	}
	var list []string
	if err := json.Unmarshal(cfg.Extends, &list); err == nil {
		return list, nil
	}
	var one string
	if err := json.Unmarshal(cfg.Extends, &one); err == nil && one != "" {
		return []string{one}, nil
	}
	return nil, errExtendsShape
}

// graphqlURL is the GraphQL endpoint for the client's base URL:
// api.github.com/graphql on github.com, <host>/api/graphql on GHES.
func (c *Client) graphqlURL() string {
	base := c.gh.BaseURL
	if p, ok := strings.CutSuffix(base.Path, "/api/v3/"); ok {
		u := *base
		u.Path = p + "/api/graphql"
		return u.String()
	}
	return base.ResolveReference(&url.URL{Path: "graphql"}).String()
}
